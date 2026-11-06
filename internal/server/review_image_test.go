package server

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewImageIsValidatedAndStoredOutsideCandidate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	if _, err := bootstrap.Init(root, false, false); err != nil {
		t.Fatal(err)
	}
	s, _, err := bootstrap.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Store.Close()
	n, err := s.Create(core.Definition{ID: "review-image", Title: "截图", Type: "bug", Goal: "verified", Acceptance: []string{"visual"}, Checks: []core.Check{{ID: "ui", Kind: "ui", Method: "human", Required: true, Covers: []string{"visual"}}}})
	if err != nil {
		t.Fatal(err)
	}
	for step := 0; step < 2; step++ {
		if step == 1 {
			if err = os.WriteFile(filepath.Join(root, "result.txt"), []byte("actual output"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		claim, err := s.Claim(n.Task.Definition.ID, "", "test", "", "")
		if err != nil {
			t.Fatal(err)
		}
		result := core.StepResult{Status: "pass", Summary: "completed"}
		if step == 1 {
			result.Artifacts = []string{"result.txt"}
		}
		n, err = s.Submit(core.Submission{Task: n.Task.Definition.ID, Attempt: claim.Attempt.ID, Token: claim.Attempt.Token, Key: core.ID("test-"), Result: result})
		if err != nil {
			t.Fatal(err)
		}
	}
	before, err := core.Fingerprint(root)
	if err != nil || before != n.Task.Candidate {
		t.Fatal(before, n.Task.Candidate, err)
	}
	h := New(s, "127.0.0.1:7331")
	post := func(body []byte, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://"+h.Host+"/api/review-image?id=review-image", bytes.NewReader(body))
		if authenticated {
			r.Header.Set("Authorization", "Bearer "+h.Token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	var imageData bytes.Buffer
	if err = png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if got := post(imageData.Bytes(), false); got.Code != 401 {
		t.Fatal("unauthenticated upload", got.Code)
	}
	if got := post([]byte("not an image"), true); got.Code != 409 || !strings.Contains(got.Body.String(), "REVIEW_IMAGE_INVALID") {
		t.Fatal("invalid image accepted", got.Code, got.Body.String())
	}
	first := post(imageData.Bytes(), true)
	var saved struct {
		Ref string `json:"ref"`
	}
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &saved) != nil || !strings.HasPrefix(saved.Ref, "runtime:artifacts/manual-review/") {
		t.Fatal("image not stored", first.Code, first.Body.String())
	}
	data, err := s.Artifact(saved.Ref)
	if err != nil || !bytes.Equal(data, imageData.Bytes()) {
		t.Fatal("stored evidence is not readable", err)
	}
	second := post(imageData.Bytes(), true)
	if second.Code != 200 || second.Body.String() != first.Body.String() {
		t.Fatal("retry changed the reference", first.Body.String(), second.Body.String())
	}
	after, err := core.Fingerprint(root)
	if err != nil || after != before {
		t.Fatal("upload changed the reviewed workspace", before, after, err)
	}
}
