package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"bytes"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// reviewImage stores a user-selected image in the project's private runtime,
// outside the workspace fingerprint. Uploading does not record a review.
func (h *Handler) reviewImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "METHOD", http.StatusMethodNotAllowed)
		return
	}
	task, err := h.Service.Get(r.URL.Query().Get("id"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	if task.Status != "open" || task.Candidate == "" {
		reply(w, nil, core.Error("REVIEW_IMAGE_NOT_ALLOWED", "an open task with a candidate is required"))
		return
	}
	const limit = 10 << 20
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || len(body) == 0 || len(body) > limit {
		reply(w, nil, core.Error("REVIEW_IMAGE_SIZE", "choose a PNG or JPEG no larger than 10 MB"))
		return
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || (format != "png" && format != "jpeg") || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 25_000_000 {
		reply(w, nil, core.Error("REVIEW_IMAGE_INVALID", "choose a readable PNG or JPEG image"))
		return
	}
	if _, decoded, decodeErr := image.Decode(bytes.NewReader(body)); decodeErr != nil || decoded != format {
		reply(w, nil, core.Error("REVIEW_IMAGE_INVALID", "choose a readable PNG or JPEG image"))
		return
	}
	ext := ".png"
	if format == "jpeg" {
		ext = ".jpg"
	}
	rel := filepath.Join("artifacts", "manual-review", store.Hash([]byte(task.Definition.ID)), store.Hash(body)+ext)
	path, err := store.SafePath(h.Service.Store.Dir, rel)
	if err == nil {
		if existing, e := os.ReadFile(path); e == nil && bytes.Equal(existing, body) {
			// Content-addressed retries return the existing file.
		} else if e != nil && !os.IsNotExist(e) {
			err = e
		} else {
			err = store.Atomic(path, body, 0600)
		}
	}
	if err != nil {
		reply(w, nil, err)
		return
	}
	reply(w, map[string]string{"ref": "runtime:" + filepath.ToSlash(rel)}, nil)
}
