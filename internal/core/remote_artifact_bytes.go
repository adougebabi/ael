package core

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strings"
	"unicode/utf8"
)

const RemoteArtifactFileLimit = 10 << 20
const RemoteJSONRequestLimit = 2 << 20

func ValidateRemoteArtifactBytes(data []byte, mimeType string) error {
	if len(data) == 0 || len(data) > RemoteArtifactFileLimit {
		return errors.New("ARTIFACT_INVALID")
	}
	switch mimeType {
	case "image/png", "image/jpeg":
		config, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 25_000_000 || (mimeType == "image/png" && format != "png") || (mimeType == "image/jpeg" && format != "jpeg") {
			return errors.New("ARTIFACT_MIME_INVALID")
		}
		if _, decoded, err := image.Decode(bytes.NewReader(data)); err != nil || decoded != format {
			return errors.New("ARTIFACT_MIME_INVALID")
		}
	case "text/plain":
		trimmed := strings.ToLower(strings.TrimSpace(string(data)))
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 || !strings.HasPrefix(http.DetectContentType(data), "text/plain") || strings.HasPrefix(trimmed, "<svg") || strings.HasPrefix(trimmed, "<?xml") {
			return errors.New("ARTIFACT_MIME_INVALID")
		}
	case "video/mp4":
		if http.DetectContentType(data) != "video/mp4" {
			return errors.New("ARTIFACT_MIME_INVALID")
		}
	case "application/octet-stream":
	default:
		return errors.New("ARTIFACT_MIME_INVALID")
	}
	return nil
}

// Historical artifacts retain their bytes even when the legacy media cannot
// safely be rendered by the current viewer.
func HistoricalArtifactType(data []byte) (string, string) {
	for _, format := range []struct{ mime, name string }{{"image/png", "historical.png"}, {"image/jpeg", "historical.jpg"}, {"text/plain", "historical.txt"}, {"video/mp4", "historical.mp4"}} {
		if ValidateRemoteArtifactBytes(data, format.mime) == nil {
			return format.mime, format.name
		}
	}
	return "application/octet-stream", "historical.bin"
}
