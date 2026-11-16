package server

import (
	"ael/internal/release"
	"context"
	"runtime"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

type clientReleaseCache struct {
	At   time.Time
	Data map[string]any
}

func (h *standaloneHandler) clientRelease(platform string, check bool) map[string]any {
	if platform == "" {
		platform = runtime.GOOS + "-" + runtime.GOARCH
	}
	result := map[string]any{"status": "no_configured_source", "available": false, "platform": platform, "publisher_identity": "unverified", "asset_downloaded": false}
	if h.releaseSource == "" {
		return result
	}
	if !slices.Contains([]string{"darwin-arm64", "darwin-amd64", "linux-arm64", "linux-amd64"}, platform) {
		result["status"] = "unsupported_platform"
		return result
	}
	h.releaseMu.Lock()
	cached, ok := h.releaseCache[platform]
	h.releaseMu.Unlock()
	if ok && time.Since(cached.At) < 2*time.Minute {
		return cached.Data
	}
	if !check {
		result["status"] = "configured_not_checked"
		return result
	}
	source, err := release.ReadSource(h.releaseSource)
	if err != nil {
		result["status"] = "source_unavailable"
	} else {
		result["catalog_url"], result["publisher_type"], result["publisher_identity_constraint"], result["publisher_issuer"] = source.ReleasesURL, source.Trust.Type, source.Trust.Identity, source.Trust.Issuer
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		selected, err := source.CheckVersion(ctx, "client", platform, "stable", h.recommendedClient)
		cancel()
		if err != nil {
			result["status"] = "verification_or_source_failed"
			code := strings.SplitN(err.Error(), ":", 2)[0]
			if standaloneID.MatchString(code) && strings.ToUpper(code) == code {
				result["error_code"] = code
			}
		} else {
			result["status"], result["available"], result["publisher_identity"] = selected.Status, selected.Available, selected.PublisherIdentity
			if selected.Available && semver.Compare("v"+selected.Manifest.Version, "v"+h.minimumClient) >= 0 {
				result["version"], result["build"], result["asset_name"], result["asset_sha256"], result["asset_size"] = selected.Manifest.Version, selected.Manifest.Build, selected.Asset.Path, selected.Asset.SHA256, selected.Asset.Size
			} else if selected.Available {
				result["status"], result["available"] = "release_below_minimum", false
			}
		}
	}
	result["checked_at"] = time.Now().UTC()
	h.releaseMu.Lock()
	h.releaseCache[platform] = clientReleaseCache{At: time.Now(), Data: result}
	h.releaseMu.Unlock()
	return result
}
