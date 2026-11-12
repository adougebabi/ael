package release

import (
	"ael/internal/core"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

type Trust struct {
	Type           string `json:"type"`
	Identity       string `json:"identity"`
	Issuer         string `json:"issuer,omitempty"`
	AllowedSigners string `json:"allowed_signers,omitempty"`
	PublicKey      string `json:"public_key,omitempty"`
	Verifier       string `json:"verifier,omitempty"`
}

type Source struct {
	ReleasesURL    string   `json:"releases_url"`
	ManifestAsset  string   `json:"manifest_asset"`
	SignatureAsset string   `json:"signature_asset"`
	AllowedOrigins []string `json:"allowed_origins"`
	Trust          Trust    `json:"trust"`
}

type RemoteAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}
type RemoteRelease struct {
	Tag        string        `json:"tag_name"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Assets     []RemoteAsset `json:"assets"`
}
type Selection struct {
	Status              string    `json:"status"`
	Available           bool      `json:"available"`
	Manifest            *Manifest `json:"manifest,omitempty"`
	Asset               *Asset    `json:"asset,omitempty"`
	URL                 string    `json:"asset_url,omitempty"`
	PublisherIdentity   string    `json:"publisher_identity"`
	authenticatedSource string
}

func ReadSource(path string) (Source, error) {
	var source Source
	raw, err := readLocal(path, 64<<10)
	if err != nil {
		return source, err
	}
	if len(raw) > 64<<10 {
		return source, core.Error("RELEASE_SOURCE_INVALID", "source exceeds 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&source); err != nil {
		return source, core.Error("RELEASE_SOURCE_INVALID", "invalid source JSON")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return source, core.Error("RELEASE_SOURCE_INVALID", "trailing source data")
	}
	if !safeName(source.ManifestAsset) || !safeName(source.SignatureAsset) || source.ManifestAsset == source.SignatureAsset {
		return source, core.Error("RELEASE_SOURCE_INVALID", "explicit manifest and signature asset names are required")
	}
	for _, allowed := range source.AllowedOrigins {
		parsed, parseErr := url.Parse(allowed)
		if parseErr != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil || parsed.Host == "" {
			return source, core.Error("RELEASE_SOURCE_INVALID", "allowed_origins must contain only origins")
		}
	}
	if err = source.allowURL(source.ReleasesURL); err != nil {
		return source, err
	}
	switch source.Trust.Type {
	case "openssh":
		if source.Trust.Identity == "" || source.Trust.AllowedSigners == "" {
			return source, core.Error("RELEASE_TRUST_REQUIRED", "signer identity and allowed_signers are required")
		}
	case "cosign-keyless":
		if source.Trust.Identity == "" || source.Trust.Issuer == "" {
			return source, core.Error("RELEASE_TRUST_REQUIRED", "certificate identity and issuer are required")
		}
	case "cosign-key":
		if source.Trust.PublicKey == "" {
			return source, core.Error("RELEASE_TRUST_REQUIRED", "a trusted public key is required")
		}
	default:
		return source, core.Error("RELEASE_TRUST_REQUIRED", "select a supported signature verifier")
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return source, err
	}
	for _, value := range []*string{&source.Trust.AllowedSigners, &source.Trust.PublicKey} {
		if *value != "" && !filepath.IsAbs(*value) {
			*value = filepath.Join(base, *value)
		}
	}
	return source, nil
}

func origin(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Scheme + "://" + parsed.Host)
}

func (source Source) allowURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return core.Error("RELEASE_SOURCE_INVALID", "invalid download URL")
	}
	host := parsed.Hostname()
	loopback := strings.EqualFold(host, "localhost")
	if ip := net.ParseIP(host); ip != nil {
		loopback = ip.IsLoopback()
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopback) {
		return core.Error("RELEASE_SOURCE_INVALID", "HTTPS is required outside loopback")
	}
	allowed := origin(value) == origin(source.ReleasesURL)
	for _, candidate := range source.AllowedOrigins {
		allowed = allowed || origin(value) == origin(candidate)
	}
	if !allowed {
		return core.Error("RELEASE_SOURCE_INVALID", "download origin is not explicitly trusted")
	}
	return nil
}

func (source Source) client() *http.Client {
	return &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return core.Error("RELEASE_SOURCE_INVALID", "too many redirects")
		}
		return source.allowURL(request.URL.String())
	}}
}

func (source Source) get(ctx context.Context, value string) (*http.Response, error) {
	if err := source.allowURL(value); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, value, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json, application/octet-stream")
	request.Header.Set("User-Agent", "AEL-release-client")
	response, err := source.client().Do(request)
	if err != nil {
		return nil, core.Error("RELEASE_SOURCE_UNAVAILABLE", "cannot read the configured source")
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, core.Error("RELEASE_SOURCE_UNAVAILABLE", "source returned a non-success response")
	}
	return response, nil
}

func (source Source) read(ctx context.Context, value string, limit int64) ([]byte, error) {
	response, err := source.get(ctx, value)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, core.Error("RELEASE_SOURCE_INVALID", "response exceeds its limit")
	}
	return data, nil
}

func (source Source) verifyPublisher(ctx context.Context, manifest, signature []byte) error {
	dir, err := os.MkdirTemp("", "ael-release-signature-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	manifestFile, signatureFile := filepath.Join(dir, "manifest.json"), filepath.Join(dir, "signature")
	if err = os.WriteFile(manifestFile, manifest, 0600); err != nil {
		return err
	}
	if err = os.WriteFile(signatureFile, signature, 0600); err != nil {
		return err
	}
	verifier := source.Trust.Verifier
	args := []string{}
	switch source.Trust.Type {
	case "openssh":
		if verifier == "" {
			verifier = "ssh-keygen"
		}
		args = []string{"-Y", "verify", "-f", source.Trust.AllowedSigners, "-I", source.Trust.Identity, "-n", "ael-release", "-s", signatureFile}
	case "cosign-keyless":
		if verifier == "" {
			verifier = "cosign"
		}
		args = []string{"verify-blob", "--bundle", signatureFile, "--certificate-identity", source.Trust.Identity, "--certificate-oidc-issuer", source.Trust.Issuer, manifestFile}
	case "cosign-key":
		if verifier == "" {
			verifier = "cosign"
		}
		args = []string{"verify-blob", "--bundle", signatureFile, "--key", source.Trust.PublicKey, manifestFile}
	default:
		return core.Error("RELEASE_TRUST_REQUIRED", "signature policy is missing")
	}
	path, err := exec.LookPath(verifier)
	if err != nil {
		return core.Error("RELEASE_VERIFIER_MISSING", "the configured signature verifier is unavailable")
	}
	verifyContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(verifyContext, path, args...)
	command.Stdin = bytes.NewReader(manifest)
	if err = command.Run(); err != nil {
		return core.Error("RELEASE_IDENTITY_REJECTED", "manifest signature or publisher identity is invalid")
	}
	return nil
}

func releaseAsset(release RemoteRelease, name string) (RemoteAsset, error) {
	var selected RemoteAsset
	for _, asset := range release.Assets {
		if asset.Name != name {
			continue
		}
		if selected.Name != "" {
			return selected, core.Error("RELEASE_SOURCE_INVALID", "duplicate remote asset name")
		}
		selected = asset
	}
	if selected.Name == "" || selected.URL == "" || selected.Size <= 0 {
		return selected, core.Error("RELEASE_ASSET_MISSING", name)
	}
	return selected, nil
}

func (source Source) Check(ctx context.Context, kind, platform, channel string) (Selection, error) {
	result := Selection{Status: "no_available_version", PublisherIdentity: "unverified"}
	if channel != "stable" && channel != "prerelease" {
		return result, core.Error("RELEASE_CHANNEL_INVALID", "select stable or prerelease")
	}
	raw, err := source.read(ctx, source.ReleasesURL, 2<<20)
	if err != nil {
		return result, err
	}
	var releases []RemoteRelease
	if err = json.Unmarshal(raw, &releases); err != nil {
		return result, core.Error("RELEASE_SOURCE_INVALID", "expected release catalog JSON")
	}
	candidates := []RemoteRelease{}
	for _, release := range releases {
		version := strings.TrimPrefix(release.Tag, "v")
		if release.Draft || !ValidVersion(version) {
			continue
		}
		pre := semver.Prerelease("v"+version) != "" || release.Prerelease
		if channel == "stable" && pre {
			continue
		}
		candidates = append(candidates, release)
	}
	sort.SliceStable(candidates, func(a, b int) bool {
		return semver.Compare("v"+strings.TrimPrefix(candidates[a].Tag, "v"), "v"+strings.TrimPrefix(candidates[b].Tag, "v")) > 0
	})
	if len(candidates) == 0 {
		return result, nil
	}
	candidate := candidates[0]
	manifestAsset, err := releaseAsset(candidate, source.ManifestAsset)
	if err != nil {
		return result, err
	}
	signatureAsset, err := releaseAsset(candidate, source.SignatureAsset)
	if err != nil {
		return result, err
	}
	manifestBytes, err := source.read(ctx, manifestAsset.URL, 1<<20)
	if err != nil {
		return result, err
	}
	if int64(len(manifestBytes)) != manifestAsset.Size {
		return result, core.Error("RELEASE_CHECKSUM_MISMATCH", "manifest size differs from asset metadata")
	}
	signature, err := source.read(ctx, signatureAsset.URL, 1<<20)
	if err != nil {
		return result, err
	}
	if int64(len(signature)) != signatureAsset.Size {
		return result, core.Error("RELEASE_CHECKSUM_MISMATCH", "signature size differs from asset metadata")
	}
	if err = source.verifyPublisher(ctx, manifestBytes, signature); err != nil {
		return result, err
	}
	manifest, err := Decode(manifestBytes)
	if err != nil {
		return result, err
	}
	if manifest.Version != strings.TrimPrefix(candidate.Tag, "v") || manifest.Publication != "released" || manifest.Dirty {
		return result, core.Error("RELEASE_MANIFEST_INVALID", "release tag, clean source and publication state must agree")
	}
	asset, err := manifest.Select(kind, platform)
	if err != nil {
		return result, err
	}
	remoteAsset, err := releaseAsset(candidate, asset.Path)
	if err != nil {
		return result, err
	}
	if remoteAsset.Size != asset.Size {
		return result, core.Error("RELEASE_CHECKSUM_MISMATCH", "asset size differs from signed manifest")
	}
	if err = source.allowURL(remoteAsset.URL); err != nil {
		return result, err
	}
	result.Status, result.Available, result.Manifest, result.Asset, result.URL, result.PublisherIdentity = "available", true, &manifest, &asset, remoteAsset.URL, "verified"
	result.authenticatedSource = source.selectionIdentity(result)
	return result, nil
}

func (source Source) selectionIdentity(selection Selection) string {
	policy, _ := json.Marshal(source)
	selected, _ := json.Marshal(selection)
	return string(policy) + "\n" + string(selected)
}

func (source Source) Download(ctx context.Context, selection Selection, output string) error {
	if !selection.Available || selection.Asset == nil || selection.PublisherIdentity != "verified" || selection.authenticatedSource != source.selectionIdentity(selection) {
		return core.Error("RELEASE_TRUST_REQUIRED", "select an authenticated release first")
	}
	response, err := source.get(ctx, selection.URL)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	file, err := os.CreateTemp(filepath.Dir(output), ".ael-release-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	written, copyErr := io.Copy(file, io.LimitReader(response.Body, selection.Asset.Size+1))
	if copyErr == nil && written != selection.Asset.Size {
		copyErr = core.Error("RELEASE_CHECKSUM_MISMATCH", "download size differs")
	}
	if copyErr == nil {
		copyErr = file.Sync()
	}
	if closeErr := file.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return copyErr
	}
	if err = VerifyFile(file.Name(), *selection.Asset); err != nil {
		return err
	}
	if err = os.Link(file.Name(), output); err != nil {
		return core.Error("RELEASE_OUTPUT_UNAVAILABLE", "choose a new output file")
	}
	return nil
}
