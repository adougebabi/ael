package bootstrap

import (
	"ael/assets"
	"ael/internal/core"
	"ael/internal/store"
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type RuntimeAsset struct {
	Platform string `json:"platform"`
	SHA256   string `json:"sha256"`
	Path     string `json:"path"`
}
type Bundle struct {
	Client        bool           `json:"client_package,omitempty"`
	PackageBuild  string         `json:"package_build,omitempty"`
	PackageCommit string         `json:"package_commit,omitempty"`
	PackageSHA256 string         `json:"package_sha256,omitempty"`
	Version       string         `json:"version"`
	Root          string         `json:"source"`
	Assets        []RuntimeAsset `json:"platforms"`
	Notice        string         `json:"notice"`
	NoticeSHA256  string         `json:"notice_sha256"`
}

func ReadBundle(dir string) (Bundle, error) { return readBundle(dir, Version) }

func readBundle(dir, expectedVersion string) (Bundle, error) {
	var b Bundle
	root, e := filepath.Abs(dir)
	if e != nil {
		return b, e
	}
	root, e = filepath.EvalSymlinks(root)
	if e != nil {
		return b, e
	}
	b.Root = root
	name := "ael-toolchain.lock"
	if _, e = os.Stat(filepath.Join(root, name)); os.IsNotExist(e) {
		name = "toolchain.lock"
	}
	path, e := store.SafePath(root, name)
	if e != nil {
		return b, e
	}
	f, e := os.Open(path)
	if e != nil {
		return b, e
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	seen := map[string]bool{}
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "version" {
			if b.Version != "" {
				return b, core.Error("INVALID_BUNDLE", "duplicate version")
			}
			b.Version = fields[1]
			continue
		}
		if len(fields) != 3 || len(fields[1]) != 64 {
			return b, core.Error("INVALID_BUNDLE", line)
		}
		for _, c := range fields[1] {
			if !strings.ContainsRune("0123456789abcdef", c) {
				return b, core.Error("INVALID_BUNDLE_HASH", fields[0])
			}
		}
		if seen[fields[0]] {
			return b, core.Error("INVALID_BUNDLE", "duplicate asset")
		}
		seen[fields[0]] = true
		if fields[0] == "notice" {
			b.Notice = fields[2]
			b.NoticeSHA256 = fields[1]
		} else {
			if fields[0] != "darwin-arm64" && fields[0] != "darwin-amd64" && fields[0] != "linux-amd64" && fields[0] != "linux-arm64" {
				return b, core.Error("UNSUPPORTED_BUNDLE_PLATFORM", fields[0])
			}
			b.Assets = append(b.Assets, RuntimeAsset{fields[0], fields[1], fields[2]})
		}
		source, e := store.SafePath(root, fields[2])
		if e != nil {
			return b, e
		}
		hash, e := store.HashFile(source)
		if e != nil {
			return b, e
		}
		if hash != fields[1] {
			return b, core.Error("BUNDLE_CHECKSUM_MISMATCH", fields[2])
		}
	}
	if e = scan.Err(); e != nil {
		return b, e
	}
	if expectedVersion != "" && b.Version != expectedVersion {
		return b, core.Error("BUNDLE_VERSION_MISMATCH", b.Version+" != "+expectedVersion)
	}
	if len(b.Assets) == 0 || b.Notice == "" {
		return b, core.Error("INCOMPLETE_BUNDLE", "binaries and notices required")
	}
	sort.Slice(b.Assets, func(i, j int) bool { return b.Assets[i].Platform < b.Assets[j].Platform })
	return b, nil
}
func DiscoverBundle(explicit string) (Bundle, error) {
	if explicit != "" {
		return ReadBundle(explicit)
	}
	if value := os.Getenv("AEL_BUNDLE_DIR"); value != "" {
		return ReadBundle(value)
	}
	exe, e := os.Executable()
	if e != nil {
		return Bundle{}, e
	}
	exe, e = filepath.EvalSymlinks(exe)
	if e != nil {
		return Bundle{}, e
	}
	dir := filepath.Dir(exe)
	candidates := []string{dir, filepath.Join(dir, "..", "dist"), filepath.Join(dir, "..", "share", "ael", "distribution", Version), filepath.Join(dir, "..", "..")}
	for _, candidate := range candidates {
		if b, e := ReadBundle(candidate); e == nil {
			return b, nil
		}
	}
	return Bundle{}, core.Error("BUNDLE_MISSING", "use the complete AEL distribution or --bundle-dir <directory containing ael-toolchain.lock>")
}
func projectLock(b Bundle) []byte {
	text := "version " + b.Version + "\n"
	for _, asset := range b.Assets {
		text += fmt.Sprintf("%s %s bin/%s/ael\n", asset.Platform, asset.SHA256, asset.Platform)
	}
	notice, _ := os.ReadFile(filepath.Join(b.Root, b.Notice))
	text += "notice " + store.Hash(notice) + " THIRD_PARTY_NOTICES.txt\n"
	return []byte(text)
}
func bundleTargets(b Bundle) map[string]string {
	targets := map[string]string{}
	for _, a := range b.Assets {
		targets[".ael/bin/"+a.Platform+"/ael"] = a.SHA256
	}
	return targets
}
func bundleText(b Bundle) map[string][]byte {
	launcher, _ := assets.Files.ReadFile("project-launcher.sh")
	notice, _ := os.ReadFile(filepath.Join(b.Root, b.Notice))
	return map[string][]byte{"ael": launcher, ".ael/toolchain.lock": projectLock(b), ".ael/THIRD_PARTY_NOTICES.txt": notice, ".ael/.gitignore": []byte("# Versioned AEL runtime assets travel with the project.\n!bin/\n!bin/**\ntool-update.lock/\n")}
}
func bundlePreflight(root string, b Bundle) error {
	manifest := map[string]string{}
	if p, e := store.SafePath(root, ".ael/assets.json"); e != nil {
		return e
	} else if data, e := os.ReadFile(p); e == nil {
		if e = json.Unmarshal(data, &manifest); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	wanted := bundleTargets(b)
	for rel, data := range bundleText(b) {
		wanted[rel] = store.Hash(data)
	}
	for rel, hash := range wanted {
		path, e := store.SafePath(root, rel)
		if e != nil {
			return e
		}
		st, e := os.Lstat(path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		if !st.Mode().IsRegular() {
			return core.Error("ASSET_CONFLICT", rel)
		}
		current, e := store.HashFile(path)
		if e != nil {
			return e
		}
		if current != hash && manifest[rel] != current {
			return core.Error("ASSET_CONFLICT", rel+" user content preserved")
		}
	}
	return nil
}

// InitProject adds a complete, version-locked runtime bundle to the metadata initializer.
// Internal state-machine fixtures can still use Init without copying executables.
func InitProject(root string, dry, updating bool, source string) (Report, error) {
	canonical, e := Canonical(root)
	if e != nil {
		return Report{}, e
	}
	if e = RefuseRemote(canonical); e != nil {
		return Report{}, e
	}
	b, e := DiscoverBundle(source)
	if e != nil {
		return Report{}, e
	}
	if e = bundlePreflight(canonical, b); e != nil {
		return Report{}, e
	}
	report, e := Init(canonical, dry, updating)
	if e != nil {
		return report, e
	}
	report.Bundle = &b
	if dry {
		for rel := range bundleTargets(b) {
			report.Files = append(report.Files, rel)
		}
		for rel := range bundleText(b) {
			report.Files = append(report.Files, rel)
		}
		report.Warnings = append(report.Warnings, "project launcher uses bundled runtimes; no global AEL, Go, Node or download is needed")
		return report, nil
	}
	manifestPath, _ := store.SafePath(canonical, ".ael/assets.json")
	raw, e := os.ReadFile(manifestPath)
	if e != nil {
		return report, e
	}
	manifest := map[string]string{}
	if e = json.Unmarshal(raw, &manifest); e != nil {
		return report, e
	}
	journal := map[string]any{"version": b.Version, "source": b.Root, "state": "installing", "targets": bundleTargets(b)}
	data, _ := json.Marshal(journal)
	journalPath := filepath.Join(report.Data, "bundle-install.json")
	if e = store.Atomic(journalPath, data, 0600); e != nil {
		return report, e
	}
	for _, asset := range b.Assets {
		rel := ".ael/bin/" + asset.Platform + "/ael"
		dest, e := store.SafePath(canonical, rel)
		if e != nil {
			return report, e
		}
		if current, e := store.HashFile(dest); e != nil || current != asset.SHA256 {
			if e = store.CopyAtomic(filepath.Join(b.Root, asset.Path), dest, 0755); e != nil {
				return report, e
			}
		}
		actual, e := store.HashFile(dest)
		if e != nil || actual != asset.SHA256 {
			return report, core.Error("BUNDLE_COPY_MISMATCH", rel)
		}
		manifest[rel] = asset.SHA256
		report.Files = append(report.Files, rel)
	}
	s, e := store.Open(report.Data)
	if e != nil {
		return report, e
	}
	defer s.Close()
	e = s.Write(func(tx *store.Tx) error {
		for rel, data := range bundleText(b) {
			p, e := store.SafePath(canonical, rel)
			if e != nil {
				return e
			}
			if e = tx.File(p, data); e != nil {
				return e
			}
			manifest[rel] = store.Hash(data)
			report.Files = append(report.Files, rel)
		}
		path, err := store.SafePath(canonical, ".ael/project.yaml")
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		config := map[string]json.RawMessage{}
		if err = json.Unmarshal(raw, &config); err != nil {
			return err
		}
		config["asset_version"], _ = json.Marshal(Version)
		data, _ := json.MarshalIndent(config, "", "  ")
		if e = tx.File(path, data); e != nil {
			return e
		}
		manifest[".ael/project.yaml"] = store.Hash(data)
		encoded, _ := json.MarshalIndent(manifest, "", "  ")
		return tx.File(manifestPath, encoded)
	})
	if e == nil {
		e = s.Flush()
	}
	if e != nil {
		return report, e
	}
	if e = os.Chmod(filepath.Join(canonical, "ael"), 0755); e != nil {
		return report, e
	}
	journal["state"] = "complete"
	data, _ = json.Marshal(journal)
	if e = store.Atomic(journalPath, data, 0600); e != nil {
		return report, e
	}
	report.Config.Version = Version
	report.Warnings = append(report.Warnings, "commit ./ael, .ael/toolchain.lock and .ael/bin; runtime DBs remain local and are rebuilt from project files")
	return report, nil
}
