// Package release verifies AEL distribution content independently of project
// initialization and the legacy all-platform offline installer.
package release

import (
	"ael/internal/core"
	"ael/internal/store"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/semver"
)

const Schema = 1
const APIProtocol = 1
const SkillProtocol = 1
const CLIProtocol = 1

type Protocols struct {
	API   int `json:"api"`
	Skill int `json:"skill"`
	CLI   int `json:"cli_output"`
}

type Asset struct {
	Kind     string `json:"kind"`
	Platform string `json:"platform"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

type Manifest struct {
	Schema      int       `json:"schema"`
	Version     string    `json:"version"`
	Build       string    `json:"build"`
	Commit      string    `json:"commit"`
	Dirty       bool      `json:"dirty"`
	Publication string    `json:"publication"`
	Protocols   Protocols `json:"protocols"`
	Assets      []Asset   `json:"assets"`
}

type Verification struct {
	Manifest          Manifest `json:"manifest"`
	ContentIntegrity  string   `json:"content_integrity"`
	PublisherIdentity string   `json:"publisher_identity"`
	Files             []string `json:"files"`
}

func ValidVersion(version string) bool {
	base := strings.SplitN(version, "+", 2)[0]
	return !strings.HasPrefix(version, "v") && semver.IsValid("v"+version) && semver.Canonical("v"+base) == "v"+base
}

func platformSupported(platform string) bool {
	return slices.Contains([]string{"darwin-arm64", "darwin-amd64", "linux-arm64", "linux-amd64"}, platform)
}

func safeName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name && !strings.ContainsAny(name, "/\\\x00\r\n")
}

func (manifest Manifest) Validate() error {
	if manifest.Schema != Schema || !ValidVersion(manifest.Version) || manifest.Build == "" || len(manifest.Build) > 200 {
		return core.Error("RELEASE_MANIFEST_INVALID", "unsupported schema or version/build metadata")
	}
	if len(manifest.Commit) != 40 && len(manifest.Commit) != 64 {
		return core.Error("RELEASE_MANIFEST_INVALID", "a full source commit is required")
	}
	if _, err := hex.DecodeString(manifest.Commit); err != nil || strings.ToLower(manifest.Commit) != manifest.Commit {
		return core.Error("RELEASE_MANIFEST_INVALID", "invalid source commit")
	}
	if !slices.Contains([]string{"local_unpublished", "build_artifact", "draft", "released"}, manifest.Publication) {
		return core.Error("RELEASE_MANIFEST_INVALID", "explicit publication state is required")
	}
	if manifest.Protocols.API != APIProtocol || manifest.Protocols.Skill != SkillProtocol || manifest.Protocols.CLI != CLIProtocol {
		return core.Error("RELEASE_PROTOCOL_UNSUPPORTED", "API, Skill or CLI protocol is incompatible")
	}
	if len(manifest.Assets) == 0 || len(manifest.Assets) > 32 {
		return core.Error("RELEASE_MANIFEST_INVALID", "bounded assets are required")
	}
	names, targets := map[string]bool{}, map[string]bool{}
	kinds := map[string]bool{}
	for _, asset := range manifest.Assets {
		if !safeName(asset.Path) || names[asset.Path] || asset.Size <= 0 || asset.Size > 16<<30 || len(asset.SHA256) != 64 {
			return core.Error("RELEASE_MANIFEST_INVALID", "invalid or duplicate asset")
		}
		if _, err := hex.DecodeString(asset.SHA256); err != nil || strings.ToLower(asset.SHA256) != asset.SHA256 {
			return core.Error("RELEASE_MANIFEST_INVALID", "invalid SHA-256")
		}
		switch asset.Kind {
		case "client":
			if !platformSupported(asset.Platform) {
				return core.Error("RELEASE_PLATFORM_UNSUPPORTED", asset.Platform)
			}
		case "server", "server_image":
			if asset.Platform != "linux-arm64" && asset.Platform != "linux-amd64" {
				return core.Error("RELEASE_PLATFORM_UNSUPPORTED", asset.Platform)
			}
		case "skill", "legacy":
			if asset.Platform != "all" {
				return core.Error("RELEASE_PLATFORM_UNSUPPORTED", asset.Platform)
			}
		default:
			return core.Error("RELEASE_MANIFEST_INVALID", "unknown asset kind")
		}
		target := asset.Kind + ":" + asset.Platform
		if targets[target] {
			return core.Error("RELEASE_MANIFEST_INVALID", "duplicate kind/platform")
		}
		names[asset.Path], targets[target], kinds[asset.Kind] = true, true, true
	}
	if !kinds["client"] || !kinds["server"] || !kinds["skill"] {
		return core.Error("RELEASE_ASSET_MISSING", "client, Server and Skill distributions are required")
	}
	return nil
}

func Decode(data []byte) (Manifest, error) {
	var manifest Manifest
	if len(data) > 1<<20 {
		return manifest, core.Error("RELEASE_MANIFEST_INVALID", "manifest exceeds 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, core.Error("RELEASE_MANIFEST_INVALID", "invalid manifest JSON")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return manifest, core.Error("RELEASE_MANIFEST_INVALID", "trailing manifest data")
	}
	return manifest, manifest.Validate()
}

func Read(path string) (Manifest, []byte, error) {
	data, err := readLocal(path, 1<<20)
	if err != nil {
		return Manifest{}, nil, err
	}
	manifest, err := Decode(data)
	return manifest, data, err
}

func (manifest Manifest) Select(kind, platform string) (Asset, error) {
	if err := manifest.Validate(); err != nil {
		return Asset{}, err
	}
	if kind == "skill" || kind == "legacy" {
		platform = "all"
	}
	for _, asset := range manifest.Assets {
		if asset.Kind == kind && asset.Platform == platform {
			return asset, nil
		}
	}
	return Asset{}, core.Error("RELEASE_ASSET_MISSING", kind+" for "+platform)
}

func VerifyFile(path string, asset Asset) error {
	info, err := os.Lstat(path)
	if err != nil {
		return core.Error("RELEASE_ASSET_MISSING", asset.Path)
	}
	if !info.Mode().IsRegular() || info.Size() != asset.Size {
		return core.Error("RELEASE_CHECKSUM_MISMATCH", asset.Path)
	}
	hash, err := store.HashFile(path)
	if err != nil {
		return err
	}
	if hash != asset.SHA256 {
		return core.Error("RELEASE_CHECKSUM_MISMATCH", asset.Path)
	}
	return nil
}

func Verify(path string) (Verification, error) {
	manifest, _, err := Read(path)
	result := Verification{Manifest: manifest, ContentIntegrity: "unverified", PublisherIdentity: "unverified", Files: []string{}}
	if err != nil {
		return result, err
	}
	root, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return result, err
	}
	for _, asset := range manifest.Assets {
		file, err := store.SafePath(root, asset.Path)
		if err != nil {
			return result, err
		}
		if err = VerifyFile(file, asset); err != nil {
			return result, err
		}
		result.Files = append(result.Files, asset.Path)
	}
	result.ContentIntegrity = "verified"
	return result, nil
}

// readLocal bounds local input before allocation, including files supplied by users.
func readLocal(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, core.Error("RELEASE_INPUT_TOO_LARGE", "local input exceeds its limit")
	}
	return data, nil
}
