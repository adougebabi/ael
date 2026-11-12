package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureManifest() Manifest {
	data := []byte("fixture payload")
	sum := sha256.Sum256(data)
	assets := []Asset{}
	for _, pair := range [][2]string{{"client", "darwin-arm64"}, {"client", "linux-amd64"}, {"server", "linux-arm64"}, {"skill", "all"}} {
		assets = append(assets, Asset{Kind: pair[0], Platform: pair[1], Path: pair[0] + "-" + pair[1] + ".tar.gz", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))})
	}
	return Manifest{Schema: 1, Version: "1.2.3", Build: "fixture", Commit: strings.Repeat("a", 40), Publication: "released", Protocols: Protocols{1, 1, 1}, Assets: assets}
}

func TestLocalManifestVerification(t *testing.T) {
	root := t.TempDir()
	manifest := fixtureManifest()
	path := filepath.Join(root, "manifest.json")
	write := func() {
		t.Helper()
		raw, _ := json.Marshal(manifest)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, asset := range manifest.Assets {
		if err := os.WriteFile(filepath.Join(root, asset.Path), []byte("fixture payload"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	result, err := Verify(path)
	if err != nil || result.ContentIntegrity != "verified" || result.PublisherIdentity != "unverified" {
		t.Fatalf("local verification: %+v %v", result, err)
	}
	assetPath := filepath.Join(root, manifest.Assets[0].Path)
	os.WriteFile(assetPath, []byte("tampered content"), 0600)
	if _, err = Verify(path); err == nil {
		t.Fatal("accepted tamper")
	}
	os.Remove(assetPath)
	os.Symlink(filepath.Join(root, manifest.Assets[1].Path), assetPath)
	if _, err = Verify(path); err == nil {
		t.Fatal("accepted symlink")
	}
	for _, name := range []string{"../escape", "/absolute", "x\\escape"} {
		broken := fixtureManifest()
		broken.Assets[0].Path = name
		if broken.Validate() == nil {
			t.Fatalf("accepted path %q", name)
		}
	}
	broken := fixtureManifest()
	broken.Assets = append(broken.Assets, broken.Assets[0])
	if broken.Validate() == nil {
		t.Fatal("accepted duplicate")
	}
	broken = fixtureManifest()
	broken.Assets = broken.Assets[:2]
	if broken.Validate() == nil {
		t.Fatal("accepted missing server/skill")
	}
	for _, version := range []string{"1", "1.2", "v1.2.3", "1.02.3", "1.2.3 garbage"} {
		if ValidVersion(version) {
			t.Fatalf("accepted version %q", version)
		}
	}
	for _, version := range []string{"1.2.3", "1.2.3-rc.1", "1.2.3+build.1"} {
		if !ValidVersion(version) {
			t.Fatalf("rejected version %q", version)
		}
	}
}

// A real OpenSSH signer authenticates an actual HTTP catalog and asset stream.
func TestSignedReleaseSource(t *testing.T) {
	tool, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Fatal("OpenSSH is required for release acceptance")
	}
	root := t.TempDir()
	key := filepath.Join(root, "key")
	if output, err := exec.Command(tool, "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("key: %s %v", output, err)
	}
	pub, _ := os.ReadFile(key + ".pub")
	allowed := filepath.Join(root, "allowed_signers")
	os.WriteFile(allowed, append([]byte("publisher "), pub...), 0600)
	manifest := fixtureManifest()
	var catalog []RemoteRelease
	var manifestBytes, signature []byte
	payload := []byte("fixture payload")
	redirect := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases":
			json.NewEncoder(w).Encode(catalog)
		case "/manifest.json":
			w.Write(manifestBytes)
		case "/manifest.sig":
			w.Write(signature)
		default:
			if redirect != "" {
				http.Redirect(w, r, redirect, http.StatusFound)
				return
			}
			w.Write(payload)
		}
	}))
	defer server.Close()
	source := Source{ReleasesURL: server.URL + "/releases", ManifestAsset: "manifest.json", SignatureAsset: "manifest.sig", Trust: Trust{Type: "openssh", Identity: "publisher", AllowedSigners: allowed}}
	sign := func() {
		t.Helper()
		manifestBytes, _ = json.Marshal(manifest)
		path := filepath.Join(root, "manifest.json")
		os.WriteFile(path, manifestBytes, 0600)
		os.Remove(path + ".sig")
		if out, err := exec.Command(tool, "-Y", "sign", "-f", key, "-n", "ael-release", path).CombinedOutput(); err != nil {
			t.Fatalf("sign: %s %v", out, err)
		}
		signature, _ = os.ReadFile(path + ".sig")
	}
	release := func(tag string, pre bool) RemoteRelease {
		assets := []RemoteAsset{{"manifest.json", server.URL + "/manifest.json", int64(len(manifestBytes))}, {"manifest.sig", server.URL + "/manifest.sig", int64(len(signature))}}
		for _, asset := range manifest.Assets {
			assets = append(assets, RemoteAsset{asset.Path, server.URL + "/" + asset.Path, asset.Size})
		}
		return RemoteRelease{Tag: tag, Prerelease: pre, Assets: assets}
	}
	check := func() (Selection, error) {
		return source.Check(context.Background(), "client", "darwin-arm64", "stable")
	}
	sign()
	catalog = []RemoteRelease{}
	if result, err := check(); err != nil || result.Available || result.Status != "no_available_version" {
		t.Fatalf("empty: %+v %v", result, err)
	}
	catalog = []RemoteRelease{release("v1.2.3", false)}
	selected, err := check()
	if err != nil || !selected.Available || selected.Asset.Platform != "darwin-arm64" {
		t.Fatalf("valid: %+v %v", selected, err)
	}
	output := filepath.Join(root, "download.tar.gz")
	if err := source.Download(context.Background(), selected, output); err != nil {
		t.Fatal(err)
	}
	if err := source.Download(context.Background(), selected, output); err == nil {
		t.Fatal("overwrote output")
	}
	forged := selected
	forged.authenticatedSource = ""
	if err := source.Download(context.Background(), forged, output+"-forged"); err == nil {
		t.Fatal("accepted forged selection")
	}
	changed := selected
	copied := *selected.Asset
	changed.Asset = &copied
	changed.Asset.Path = "substituted.tar.gz"
	if err := source.Download(context.Background(), changed, output+"-changed"); err == nil {
		t.Fatal("accepted changed authenticated selection")
	}
	payload = []byte("tampered bytes!")
	if err := source.Download(context.Background(), selected, output+"-bad"); err == nil {
		t.Fatal("accepted tampered asset")
	}
	payload = []byte("fixture payload")
	redirect = "https://untrusted.invalid/asset"
	if err := source.Download(context.Background(), selected, output+"-redirect"); err == nil {
		t.Fatal("accepted untrusted redirect")
	}
	redirect = ""
	if _, err := source.Check(context.Background(), "client", "linux-arm64", "stable"); err == nil {
		t.Fatal("accepted missing platform")
	}
	catalog[0].Assets = catalog[0].Assets[:2]
	if _, err := check(); err == nil {
		t.Fatal("accepted missing asset")
	}
	catalog = []RemoteRelease{release("v1.2.3", false)}
	source.Trust.Identity = "wrong-publisher"
	if _, err := check(); err == nil {
		t.Fatal("accepted wrong signer identity")
	}
	source.Trust.Identity = "publisher"
	manifestBytes[0] = '['
	if _, err := check(); err == nil {
		t.Fatal("accepted tampered manifest")
	}
	manifest = fixtureManifest()
	manifest.Protocols.API = 999
	sign()
	catalog = []RemoteRelease{release("v1.2.3", false)}
	if _, err := check(); err == nil {
		t.Fatal("accepted unsupported signed manifest")
	}
	manifest = fixtureManifest()
	manifest.Version = "1.3.0-rc.1"
	sign()
	catalog = []RemoteRelease{release("v1.3.0-rc.1", true)}
	if result, err := check(); err != nil || result.Available {
		t.Fatalf("stable selected prerelease %+v %v", result, err)
	}
	if result, err := source.Check(context.Background(), "client", "linux-amd64", "prerelease"); err != nil || !result.Available || result.Manifest.Version != "1.3.0-rc.1" {
		t.Fatalf("prerelease %+v %v", result, err)
	}
	catalog[0].Draft = true
	if result, err := source.Check(context.Background(), "client", "linux-amd64", "prerelease"); err != nil || result.Available {
		t.Fatalf("selected draft %+v %v", result, err)
	}
}
