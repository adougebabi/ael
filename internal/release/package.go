package release

import (
	"ael/internal/core"
	"ael/internal/store"
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

type PackageFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type ClientPackage struct {
	Version   string        `json:"version"`
	Build     string        `json:"build"`
	Commit    string        `json:"commit"`
	Kind      string        `json:"kind"`
	Platform  string        `json:"platform"`
	Protocols Protocols     `json:"protocols"`
	Files     []PackageFile `json:"files"`
}

// ReadClientPackage validates the exact independent client format before probing
// or installing its executable. Local input remains an explicitly chosen source.
func ReadClientPackage(root string) (ClientPackage, string, error) {
	var result ClientPackage
	path, err := store.SafePath(root, "package.json")
	if err != nil {
		return result, "", err
	}
	raw, err := readLocal(path, 1<<20)
	if err != nil {
		return result, "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&result); err != nil {
		return result, "", core.Error("CLIENT_PACKAGE_INVALID", "invalid package metadata")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || result.Kind != "client" || !ValidVersion(result.Version) || !platformSupported(result.Platform) || result.Build == "" || len(result.Build) > 200 || len(result.Files) != 3 {
		return result, "", core.Error("CLIENT_PACKAGE_INVALID", "incompatible client package")
	}
	if len(result.Commit) != 40 && len(result.Commit) != 64 {
		return result, "", core.Error("CLIENT_PACKAGE_INVALID", "full commit required")
	}
	if _, err = hex.DecodeString(result.Commit); err != nil {
		return result, "", core.Error("CLIENT_PACKAGE_INVALID", "invalid source commit")
	}
	if result.Protocols != (Protocols{APIProtocol, SkillProtocol, CLIProtocol}) {
		return result, "", core.Error("RELEASE_PROTOCOL_UNSUPPORTED", "client package protocol is incompatible")
	}
	wanted := map[string]bool{"ael": true, "THIRD_PARTY_NOTICES.txt": true, "README.md": true}
	for _, file := range result.Files {
		if !wanted[file.Path] || file.Size <= 0 || file.Size > 128<<20 || len(file.SHA256) != 64 {
			return result, "", core.Error("CLIENT_PACKAGE_INVALID", "invalid or duplicate package file")
		}
		delete(wanted, file.Path)
		path, err = store.SafePath(root, file.Path)
		if err != nil {
			return result, "", err
		}
		if err = VerifyFile(path, Asset{Path: file.Path, Size: file.Size, SHA256: file.SHA256}); err != nil {
			return result, "", err
		}
	}
	return result, store.Hash(raw), nil
}

// ExtractClientPackage accepts four flat regular files and bounds expanded bytes.
// The caller owns the empty temporary directory and removes it after installation.
func ExtractClientPackage(path, destination string) (ClientPackage, error) {
	var result ClientPackage
	entries, err := os.ReadDir(destination)
	if err != nil {
		return result, err
	}
	if len(entries) != 0 {
		return result, core.Error("CLIENT_STAGE_NOT_EMPTY", "choose an empty staging directory")
	}
	file, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return result, core.Error("CLIENT_PACKAGE_INVALID", "expected a gzip client archive")
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	wanted := map[string]bool{"ael": true, "THIRD_PARTY_NOTICES.txt": true, "README.md": true, "package.json": true}
	var total int64
	for {
		header, readErr := archive.Next()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return result, core.Error("CLIENT_PACKAGE_INVALID", "invalid archive")
		}
		total += header.Size
		if !wanted[header.Name] || header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Size > 128<<20 || total > 256<<20 {
			return result, core.Error("CLIENT_PACKAGE_INVALID", "unexpected, linked, duplicate or oversized archive entry")
		}
		delete(wanted, header.Name)
		mode := os.FileMode(0644)
		if header.Name == "ael" {
			mode = 0755
		}
		target, openErr := os.OpenFile(filepath.Join(destination, header.Name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if openErr != nil {
			return result, openErr
		}
		_, copyErr := io.CopyN(target, archive, header.Size)
		closeErr := target.Close()
		if copyErr != nil {
			return result, copyErr
		}
		if closeErr != nil {
			return result, closeErr
		}
	}
	if len(wanted) != 0 {
		return result, core.Error("CLIENT_PACKAGE_INVALID", "required client files are missing")
	}
	// Consume tar padding so gzip footer errors remain observable after tar EOF.
	tail, tailErr := io.ReadAll(io.LimitReader(compressed, (1<<20)+1))
	if tailErr != nil || len(tail) > 1<<20 || len(bytes.Trim(tail, "\x00")) != 0 {
		return result, core.Error("CLIENT_PACKAGE_INVALID", "invalid compressed footer or trailing archive content")
	}
	result, _, err = ReadClientPackage(destination)
	return result, err
}
