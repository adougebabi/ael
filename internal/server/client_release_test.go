package server

import (
	"ael/internal/release"
	"ael/internal/store"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestClientReleaseMetadataRequiresVerifiedRealCatalogAssets(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("OpenSSH signing verifier unavailable")
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	if output, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("signer %v %s", err, output)
	}
	public, _ := os.ReadFile(key + ".pub")
	signers := filepath.Join(dir, "allowed_signers")
	os.WriteFile(signers, append([]byte("fixture "), public...), 0600)
	manifest := release.Manifest{Schema: 1, Version: "0.1.23", Build: "fixture-build", Commit: strings.Repeat("a", 40), Publication: "released", Protocols: release.Protocols{API: 1, Skill: 1, CLI: 1}, Assets: []release.Asset{
		{Kind: "client", Platform: runtime.GOOS + "-" + runtime.GOARCH, Path: "client.tar.gz", SHA256: store.Hash([]byte("fixture")), Size: 7},
		{Kind: "server", Platform: "linux-arm64", Path: "server.tar.gz", SHA256: store.Hash([]byte("fixture")), Size: 7},
		{Kind: "skill", Platform: "all", Path: "skill.tar.gz", SHA256: store.Hash([]byte("fixture")), Size: 7},
	}}
	raw, _ := json.Marshal(manifest)
	manifestPath := filepath.Join(dir, "manifest.json")
	os.WriteFile(manifestPath, raw, 0600)
	if output, err := exec.Command("ssh-keygen", "-Y", "sign", "-f", key, "-n", "ael-release", manifestPath).CombinedOutput(); err != nil {
		t.Fatalf("sign %v %s", err, output)
	}
	signature, _ := os.ReadFile(manifestPath + ".sig")
	assetDownloads := 0
	var sourceURL string
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases":
			json.NewEncoder(w).Encode([]release.RemoteRelease{{Tag: "v0.1.23", Assets: []release.RemoteAsset{{Name: "manifest.json", URL: sourceURL + "/manifest.json", Size: int64(len(raw))}, {Name: "manifest.sig", URL: sourceURL + "/manifest.sig", Size: int64(len(signature))}, {Name: "client.tar.gz", URL: sourceURL + "/client.tar.gz", Size: 7}}}})
		case "/manifest.json":
			w.Write(raw)
		case "/manifest.sig":
			w.Write(signature)
		case "/client.tar.gz":
			assetDownloads++
			w.Write([]byte("fixture"))
		default:
			w.WriteHeader(404)
		}
	}))
	defer endpoint.Close()
	sourceURL = endpoint.URL
	source := release.Source{ReleasesURL: endpoint.URL + "/releases", ManifestAsset: "manifest.json", SignatureAsset: "manifest.sig", Trust: release.Trust{Type: "openssh", Identity: "fixture", AllowedSigners: signers}}
	sourcePath := filepath.Join(dir, "source.json")
	data, _ := json.Marshal(source)
	os.WriteFile(sourcePath, data, 0600)
	s, err := OpenStandalone(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := s.Handler(StandaloneHTTPOptions{Origin: "http://server.test", ClientReleaseSource: sourcePath, MinimumClientVersion: "0.1.22"})
	if err != nil {
		t.Fatal(err)
	}
	response := standaloneJSON(t, standaloneRequest(t, h, "GET", "/api/v1/meta?updates=1", nil, nil, "", "", "", ""), http.StatusOK)
	if response["recommended_client_version"] != "0.1.23" || response["client_release"].(map[string]any)["publisher_identity"] != "verified" || assetDownloads != 0 {
		t.Fatalf("unverified recommendation %+v downloads=%d", response, assetDownloads)
	}
	raw = append(raw, ' ')
	h, err = s.Handler(StandaloneHTTPOptions{Origin: "http://server.test", ClientReleaseSource: sourcePath})
	if err != nil {
		t.Fatal(err)
	}
	response = standaloneJSON(t, standaloneRequest(t, h, "GET", "/api/v1/meta?updates=1", nil, nil, "", "", "", ""), http.StatusOK)
	if response["recommended_client_version"] != nil || response["client_release"].(map[string]any)["available"] != false {
		t.Fatalf("bad signature advertised %+v", response)
	}
	h, _ = s.Handler(StandaloneHTTPOptions{Origin: "http://server.test"})
	response = standaloneJSON(t, standaloneRequest(t, h, "GET", "/api/v1/meta?updates=1", nil, nil, "", "", "", ""), http.StatusOK)
	if response["client_release"].(map[string]any)["status"] != "no_configured_source" {
		t.Fatalf("invented source %+v", response)
	}
}
