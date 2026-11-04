package bootstrap

import (
	"ael/internal/core"
	"ael/internal/store"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type UpdateOptions struct {
	From       string
	Prefix     string
	DryRun     bool
	AckStopped bool
}
type BinaryVersion struct {
	Version string `json:"version"`
	Build   string `json:"build"`
}
type UpdateReport struct {
	Operation       string            `json:"operation"`
	Status          string            `json:"status"`
	Target          string            `json:"target"`
	Source          string            `json:"source"`
	Previous        BinaryVersion     `json:"previous"`
	Available       BinaryVersion     `json:"available"`
	DataDir         string            `json:"data_dir,omitempty"`
	BackupDir       string            `json:"backup_dir,omitempty"`
	Files           []string          `json:"files"`
	RestartRequired bool              `json:"restart_required"`
	Warnings        []string          `json:"warnings"`
	Validation      *UpdateValidation `json:"validation,omitempty"`
}
type UpdateValidation struct {
	SourceSHA256 string            `json:"source_sha256"`
	Assets       map[string]string `json:"assets"`
}
type installation struct {
	Version string    `json:"version"`
	Build   string    `json:"build"`
	SHA256  string    `json:"sha256"`
	Source  string    `json:"source"`
	Bundle  string    `json:"bundle"`
	Updated time.Time `json:"updated"`
}

var releaseVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func compareVersions(a, b string) (int, error) {
	if !releaseVersion.MatchString(a) || !releaseVersion.MatchString(b) {
		return 0, core.Error("INVALID_RELEASE_VERSION", a+" / "+b)
	}
	for i, x := range strings.Split(a, ".") {
		left, e := strconv.ParseUint(x, 10, 64)
		if e != nil {
			return 0, e
		}
		right, e := strconv.ParseUint(strings.Split(b, ".")[i], 10, 64)
		if e != nil {
			return 0, e
		}
		if left < right {
			return -1, nil
		}
		if left > right {
			return 1, nil
		}
	}
	return 0, nil
}
func prefixPath(p string) (string, error) {
	if p == "" {
		p = os.Getenv("AEL_INSTALL_PREFIX")
	}
	if p == "" {
		exe, e := os.Executable()
		if e == nil {
			exe, _ = filepath.EvalSymlinks(exe)
			if filepath.Base(filepath.Dir(exe)) == "bin" {
				candidate := filepath.Dir(filepath.Dir(exe))
				if _, e := os.Stat(filepath.Join(candidate, "share/ael/install.sha256")); e == nil {
					p = candidate
				}
			}
		}
		if p == "" {
			if candidate, e := exec.LookPath("ael"); e == nil {
				candidate, _ = filepath.EvalSymlinks(candidate)
				if filepath.Base(filepath.Dir(candidate)) == "bin" {
					prefix := filepath.Dir(filepath.Dir(candidate))
					if _, e = os.Stat(filepath.Join(prefix, "share/ael/install.sha256")); e == nil {
						p = prefix
					}
				}
			}
		}
		if p == "" {
			home, e := os.UserHomeDir()
			if e != nil {
				return "", e
			}
			p = filepath.Join(home, ".local")
		}
	}
	abs, e := filepath.Abs(p)
	if e != nil {
		return "", e
	}
	probe := abs
	suffix := []string{}
	for {
		if _, err := os.Lstat(probe); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", core.Error("INVALID_PREFIX", p)
		}
		suffix = append(suffix, filepath.Base(probe))
		probe = parent
	}
	resolved, e := filepath.EvalSymlinks(probe)
	if e != nil {
		return "", e
	}
	for i := len(suffix) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, suffix[i])
	}
	return resolved, nil
}
func readInstallation(prefix string) (installation, error) {
	var in installation
	p, e := store.SafePath(prefix, "share/ael/installation.json")
	if e != nil {
		return in, e
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return in, e
	}
	e = json.Unmarshal(b, &in)
	return in, e
}
func updateBundle(dir string) (Bundle, error) {
	b, e := readBundle(dir, "")
	if e != nil {
		return b, e
	}
	if !releaseVersion.MatchString(b.Version) {
		return b, core.Error("INVALID_RELEASE_VERSION", b.Version)
	}
	if len(b.Assets) != 4 {
		return b, core.Error("INCOMPLETE_BUNDLE", "updates require all four platform runtimes and notices")
	}
	paths := map[string]bool{b.Notice: true}
	for _, a := range b.Assets {
		if paths[a.Path] {
			return b, core.Error("INVALID_BUNDLE_PATH", "runtime and notice paths must be distinct: "+a.Path)
		}
		paths[a.Path] = true
	}
	return b, nil
}

func validateBundleSnapshot(b Bundle) error {
	current, e := updateBundle(b.Root)
	if e != nil {
		return e
	}
	if !sameDistribution(current, b) {
		return core.Error("SOURCE_CHANGED_DURING_UPDATE", b.Root)
	}
	return nil
}
func sourceBundle(o UpdateOptions, self bool) (Bundle, error) {
	if o.From != "" {
		return updateBundle(o.From)
	}
	if from := os.Getenv("AEL_BUNDLE_DIR"); from != "" {
		return updateBundle(from)
	}
	prefix, e := prefixPath(o.Prefix)
	if e != nil {
		return Bundle{}, e
	}
	if !self {
		if e = noUpdateLock(prefix, "share/ael/upgrade.lock"); e != nil {
			return Bundle{}, e
		}
	}
	in, e := readInstallation(prefix)
	if e == nil {
		source := in.Bundle
		if self {
			source = in.Source
		}
		if source == "" {
			return Bundle{}, core.Error("UPDATE_SOURCE_REQUIRED", "registered source is empty; supply --from DIR")
		}
		return updateBundle(source)
	}
	if !os.IsNotExist(e) {
		return Bundle{}, e
	}
	// A newly extracted distribution can bootstrap the local installation itself.
	exe, e := os.Executable()
	if e != nil {
		return Bundle{}, e
	}
	exe, e = filepath.EvalSymlinks(exe)
	if e != nil {
		return Bundle{}, e
	}
	dir := filepath.Dir(exe)
	for _, candidate := range []string{dir, filepath.Join(dir, "..", "dist")} {
		if _, e := os.Stat(filepath.Join(candidate, "ael-toolchain.lock")); e == nil {
			return updateBundle(candidate)
		} else if !os.IsNotExist(e) {
			return Bundle{}, e
		}
	}
	// Legacy managed installs already carry versioned distributions, but no registry.
	entries, e := os.ReadDir(filepath.Join(prefix, "share/ael/distribution"))
	if e != nil && !os.IsNotExist(e) {
		return Bundle{}, e
	}
	latest := ""
	for _, entry := range entries {
		if entry.IsDir() && releaseVersion.MatchString(entry.Name()) {
			if latest == "" {
				latest = entry.Name()
			} else if cmp, e := compareVersions(entry.Name(), latest); e == nil && cmp > 0 {
				latest = entry.Name()
			}
		}
	}
	if latest != "" {
		return updateBundle(filepath.Join(prefix, "share/ael/distribution", latest))
	}
	return Bundle{}, core.Error("UPDATE_SOURCE_REQUIRED", "run upgrade --from DISTRIBUTION to register a local release, or supply update --from DISTRIBUTION; no online release endpoint is configured")
}
func nativeAsset(b Bundle) (RuntimeAsset, error) {
	platform := runtime.GOOS + "-" + runtime.GOARCH
	for _, a := range b.Assets {
		if a.Platform == platform {
			return a, nil
		}
	}
	return RuntimeAsset{}, core.Error("UNSUPPORTED_PLATFORM", platform)
}
func probeBinary(path string) (BinaryVersion, error) {
	var v BinaryVersion
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, path, "version")
	cmd.Stderr = &stderr
	b, e := cmd.Output()
	if e != nil {
		return v, core.Error("UPDATE_BINARY_PROBE_FAILED", fmt.Sprintf("%s: %v; %s", path, e, strings.TrimSpace(stderr.String())))
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if e = dec.Decode(&v); e != nil {
		return v, core.Error("INVALID_BINARY_VERSION", path)
	}
	var extra any
	if dec.Decode(&extra) != io.EOF || !releaseVersion.MatchString(v.Version) {
		return v, core.Error("INVALID_BINARY_VERSION", path)
	}
	return v, nil
}
func sourceVersion(b Bundle) (RuntimeAsset, BinaryVersion, error) {
	a, e := nativeAsset(b)
	if e != nil {
		return a, BinaryVersion{}, e
	}
	p, e := store.SafePath(b.Root, a.Path)
	if e != nil {
		return a, BinaryVersion{}, e
	}
	v, e := probeBinary(p)
	if e == nil && v.Version != b.Version {
		e = core.Error("BUNDLE_VERSION_MISMATCH", v.Version+" != "+b.Version)
	}
	return a, v, e
}
func rejectDowngrade(previous, available string) error {
	if previous == "" {
		return nil
	}
	cmp, e := compareVersions(available, previous)
	if e != nil {
		return e
	}
	if cmp < 0 {
		return core.Error("DOWNGRADE_REFUSED", previous+" -> "+available+"; restore an explicit backup for rollback")
	}
	return nil
}
