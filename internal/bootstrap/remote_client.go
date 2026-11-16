package bootstrap

import (
	"ael/assets"
	"ael/internal/core"
	"ael/internal/release"
	"ael/internal/store"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func InstallationPrefix(prefix string) (string, error) { return prefixPath(prefix) }

// The launcher reads this machine-local path mapping as data, never as shell.
// No credential or task authority is saved in the checkout or this mapping.
func BindRemoteClient(root, executable, config string) (string, error) {
	root, err := Canonical(root)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(executable) || !filepath.IsAbs(config) || strings.ContainsAny(executable+config, "\r\n\x00") {
		return "", core.Error("REMOTE_CLIENT_PATH_INVALID", "client and configuration paths must be absolute single-line paths")
	}
	base, err := clientMappingBase(root)
	if err != nil {
		return "", err
	}
	path, err := store.SafePath(base, store.Hash([]byte(root))+".path")
	if err != nil {
		return "", err
	}
	if err = store.Atomic(path, []byte(executable+"\n"+config+"\n"), 0600); err != nil {
		return "", err
	}
	return path, nil
}

func clientMappingBase(root string) (string, error) {
	var err error
	base := os.Getenv("AEL_CLIENT_CONFIG_DIR")
	if base == "" {
		userConfig, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(userConfig, "ael", "clients")
	}
	if !filepath.IsAbs(base) {
		return "", core.Error("REMOTE_CLIENT_PATH_INVALID", "AEL_CLIENT_CONFIG_DIR must be absolute")
	}
	base, err = prefixPath(base)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, base)
	if err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return "", core.Error("REMOTE_CLIENT_PATH_INVALID", "machine client mapping must stay outside the checkout")
	}
	if err = os.MkdirAll(base, 0700); err != nil {
		return "", err
	}
	return base, nil
}

func BindRemoteSession(root, session, executable, config, pin string) (string, error) {
	root, err := Canonical(root)
	if err != nil {
		return "", err
	}
	if session == "" || session == "." || session == ".." || strings.ContainsAny(session, "/\\\r\n\x00") || strings.ContainsAny(executable+config+pin, "\r\n\x00") {
		return "", core.Error("REMOTE_CLIENT_PATH_INVALID", "session binding contains invalid paths")
	}
	base, err := clientMappingBase(root)
	if err != nil {
		return "", err
	}
	path, err := store.SafePath(base, filepath.Join("sessions", store.Hash([]byte(root)), session+".path"))
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	wanted := executable + "\n" + config + "\n" + pin + "\n"
	if exists, err := matchingSessionMapping(path, wanted); err != nil || exists {
		return path, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".session-mapping-")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.WriteString(wanted)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err = os.Link(file.Name(), path); os.IsExist(err) {
		_, err = matchingSessionMapping(path, wanted)
	}
	return path, err
}

func UnbindRemoteSession(root, session, pin string) error {
	root, err := Canonical(root)
	if err != nil {
		return err
	}
	if session == "" || session == "." || session == ".." || strings.ContainsAny(session, "/\\\r\n\x00") {
		return core.Error("REMOTE_CLIENT_PATH_INVALID", "invalid session identifier")
	}
	base, err := clientMappingBase(root)
	if err != nil {
		return err
	}
	path, err := store.SafePath(base, filepath.Join("sessions", store.Hash([]byte(root)), session+".path"))
	if err != nil {
		return err
	}
	if raw, readErr := os.ReadFile(path); readErr == nil {
		lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
		if len(lines) != 3 || lines[2] != pin {
			return core.Error("SESSION_BINDING_CONFLICT", "another tool session owns this launcher reference")
		}
	} else if os.IsNotExist(readErr) {
		return nil
	} else {
		return readErr
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func matchingSessionMapping(path, wanted string) (bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if string(raw) != wanted {
		return false, core.Error("SESSION_BINDING_CONFLICT", "another credential or tool session already owns this launcher reference")
	}
	return true, nil
}

type ClientTool struct {
	Executable string        `json:"executable"`
	SHA256     string        `json:"sha256"`
	Version    BinaryVersion `json:"version"`
}

// Standalone legacy entry points share a content-addressed executable in the
// existing machine client cache. Package installs use their versioned bundles.
func FreezeStandaloneClient(prefix string, tool ClientTool) (ClientTool, error) {
	root, err := prefixPath(prefix)
	if err != nil {
		return tool, err
	}
	if len(tool.SHA256) != 64 {
		return tool, core.Error("CLIENT_CACHE_MISMATCH", "standalone executable hash is missing")
	}
	dir, err := store.SafePath(root, "share/ael/clients/standalone/"+tool.SHA256)
	if err != nil {
		return tool, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return tool, err
	}
	destination, err := store.SafePath(dir, "ael")
	if err != nil {
		return tool, err
	}
	actual, readErr := store.HashFile(destination)
	if readErr != nil && !os.IsNotExist(readErr) {
		return tool, readErr
	}
	if os.IsNotExist(readErr) {
		stage, err := os.CreateTemp(dir, ".standalone-")
		if err != nil {
			return tool, err
		}
		stage.Close()
		defer os.Remove(stage.Name())
		if err = store.CopyAtomic(tool.Executable, stage.Name(), 0755); err != nil {
			return tool, err
		}
		if actual, err = store.HashFile(stage.Name()); err != nil || actual != tool.SHA256 {
			return tool, core.Error("CLIENT_CACHE_MISMATCH", "standalone executable changed while being cached")
		}
		if err = os.Link(stage.Name(), destination); err != nil && !os.IsExist(err) {
			return tool, err
		}
	}
	if actual, err = store.HashFile(destination); err != nil || actual != tool.SHA256 {
		return tool, core.Error("CLIENT_CACHE_MISMATCH", "standalone executable cache changed")
	}
	version, err := probeBinary(destination)
	if err != nil {
		return tool, err
	}
	tool.Executable, tool.Version = destination, version
	return tool, nil
}

func InstalledClient(prefix string) (ClientTool, error) {
	root, err := prefixPath(prefix)
	if err != nil {
		return ClientTool{}, err
	}
	installed, err := readInstallation(root)
	if err != nil {
		return ClientTool{}, err
	}
	if _, err = installedHash(root); err != nil {
		return ClientTool{}, err
	}
	tool, err := CachedClient(root, installed.Version, installed.Build)
	if err != nil {
		return tool, err
	}
	if tool.SHA256 != installed.SHA256 {
		return ClientTool{}, core.Error("CLIENT_CACHE_MISMATCH", "installed metadata differs from immutable cache")
	}
	return tool, nil
}

func CachedClient(prefix, version, build string) (ClientTool, error) {
	var tool ClientTool
	if !release.ValidVersion(version) {
		return tool, core.Error("CLIENT_VERSION_INVALID", "use an exact cached version")
	}
	root, err := prefixPath(prefix)
	if err != nil {
		return tool, err
	}
	for _, kind := range []string{"clients", "distribution"} {
		dir, err := store.SafePath(root, "share/ael/"+kind+"/"+version)
		if err != nil {
			return tool, err
		}
		if _, err = os.Stat(dir); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return tool, err
		}
		bundle, err := machineBundle(dir)
		if err != nil {
			return tool, err
		}
		asset, binary, err := sourceVersion(bundle)
		if err != nil {
			return tool, err
		}
		if binary.Version != version || (build != "" && binary.Build != build) {
			return tool, core.Error("CLIENT_VERSION_MISMATCH", "cached executable differs from the requested version/build")
		}
		executable, err := store.SafePath(dir, asset.Path)
		if err != nil {
			return tool, err
		}
		actual, err := store.HashFile(executable)
		if err != nil || actual != asset.SHA256 {
			return tool, core.Error("CLIENT_CACHE_MISMATCH", "cached executable content changed")
		}
		return ClientTool{Executable: executable, SHA256: actual, Version: binary}, nil
	}
	return tool, core.Error("CLIENT_VERSION_UNAVAILABLE", "the requested version is absent from the immutable installation cache")
}

// Only AEL-owned rules are frozen. Project specifications keep their original
// authority and are rechecked against Server task/context revisions separately.
func RemoteSessionAssets(root string) (string, string, map[string][]byte, error) {
	entry, err := RemoteEntryInfo(root)
	if err != nil {
		return "", "", nil, err
	}
	if entry.Status == "modified" {
		return "", "", nil, core.Error("ASSET_CONFLICT", "reconcile modified AEL-owned project assets before starting another tool session")
	}
	version := entry.Version
	var manifest RemoteEntryManifest
	if entry.Status == "not_installed" {
		if _, err := os.Stat(filepath.Join(root, ".ael/toolchain.lock")); err == nil {
			return "", "", nil, core.Error("REMOTE_ENTRY_MIGRATION_REQUIRED", "use the shared CLI update --migrate-entry before preparing tools through this legacy project entry")
		}
		version = Version
	} else {
		if err = readEntryJSON(root, ".ael/remote-assets.json", &manifest); err != nil {
			return "", "", nil, err
		}
	}
	files := map[string][]byte{}
	for _, name := range []string{"SKILL.md", "references/workflow.md", "references/commands.md", "references/remote-agent.md"} {
		var data []byte
		if entry.Status == "not_installed" {
			data, err = assets.Files.ReadFile("skills/ael-work/" + name)
		} else {
			data, err = readEntryBytes(root, ".agents/skills/ael-work/"+name, 1<<20)
		}
		if err != nil {
			return "", "", nil, err
		}
		if entry.Status != "not_installed" && store.Hash(data) != manifest.Assets[".agents/skills/ael-work/"+name] {
			return "", "", nil, core.Error("ASSET_CONFLICT", "AEL-owned assets changed during session preparation")
		}
		files[name] = data
	}
	managed := block("remote")
	if entry.Status != "not_installed" {
		managed, err = remoteManagedBlock(root, "AGENTS.md")
		if err != nil {
			return "", "", nil, err
		}
	}
	files["entry.md"] = []byte(managed)
	raw, _ := json.Marshal(files)
	return version, store.Hash(raw), files, nil
}
