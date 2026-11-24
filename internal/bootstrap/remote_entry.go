package bootstrap

import (
	"ael/assets"
	"ael/internal/core"
	"ael/internal/projectmode"
	"ael/internal/release"
	"ael/internal/store"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type RemoteEntryManifest struct {
	Schema        int               `json:"schema"`
	Version       string            `json:"version"`
	APIProtocol   int               `json:"api_protocol"`
	SkillProtocol int               `json:"skill_protocol"`
	Assets        map[string]string `json:"assets"`
}

type RemoteEntryState struct {
	Status        string   `json:"status"`
	Version       string   `json:"version,omitempty"`
	APIProtocol   int      `json:"api_protocol,omitempty"`
	SkillProtocol int      `json:"skill_protocol,omitempty"`
	Modified      []string `json:"modified"`
}

type remoteEntryIntent struct {
	Version     string            `json:"version"`
	BindingHash string            `json:"binding_sha256"`
	Before      map[string]string `json:"before"`
	Targets     map[string][]byte `json:"targets"`
	BackupDir   string            `json:"backup_dir,omitempty"`
}

const remoteEntryJournalLimit = 16 << 20

func readEntryBytes(root, relative string, limit int64) ([]byte, error) {
	path, err := store.SafePath(root, relative)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, core.Error("REMOTE_ENTRY_INVALID", "entry input exceeds its readable limit")
	}
	return raw, nil
}

func readEntryJSON(root, relative string, result any) error {
	limit := int64(1 << 20)
	if relative == ".ael/remote-entry-update.json" {
		limit = remoteEntryJournalLimit
	}
	raw, err := readEntryBytes(root, relative, limit)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(result); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return core.Error("REMOTE_ENTRY_INVALID", "trailing entry metadata")
	}
	return nil
}

func (manifest RemoteEntryManifest) validate() error {
	names := []string{"ael", "AGENTS:block", ".agents/skills/ael-work/SKILL.md", ".agents/skills/ael-work/references/workflow.md", ".agents/skills/ael-work/references/commands.md", ".agents/skills/ael-work/references/remote-agent.md"}
	if manifest.Schema != 1 || manifest.APIProtocol != 1 || manifest.SkillProtocol != 1 || !release.ValidVersion(manifest.Version) || (len(manifest.Assets) != len(names) && len(manifest.Assets) != len(names)+2) {
		return core.Error("REMOTE_ENTRY_INVALID", "incompatible or incomplete entry metadata")
	}
	if len(manifest.Assets) == len(names)+2 {
		names = append(names, "CLAUDE:block", ".claude/skills/ael-work/SKILL.md")
	}
	for _, name := range names {
		if len(manifest.Assets[name]) != 64 {
			return core.Error("REMOTE_ENTRY_INVALID", "required entry hash is missing")
		}
	}
	return nil
}

func RemoteEntryInfo(root string) (RemoteEntryState, error) {
	result := RemoteEntryState{Status: "not_installed", Modified: []string{}}
	var manifest RemoteEntryManifest
	err := readEntryJSON(root, ".ael/remote-assets.json", &manifest)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if err = manifest.validate(); err != nil {
		return result, err
	}
	result.Status, result.Version, result.APIProtocol, result.SkillProtocol = "current", manifest.Version, manifest.APIProtocol, manifest.SkillProtocol
	for relative, expected := range manifest.Assets {
		var actual string
		if relative == "AGENTS:block" || relative == "CLAUDE:block" {
			filename := strings.TrimSuffix(relative, ":block") + ".md"
			body, readErr := remoteManagedBlock(root, filename)
			if readErr == nil {
				actual = store.Hash([]byte(body))
			}
		} else {
			path, pathErr := store.SafePath(root, relative)
			if pathErr != nil {
				return result, pathErr
			}
			actual, _ = store.HashFile(path)
		}
		if actual != expected {
			result.Modified = append(result.Modified, relative)
		}
	}
	sort.Strings(result.Modified)
	if len(result.Modified) > 0 {
		result.Status = "modified"
	}
	return result, nil
}

func remoteEntryTargets(root string, owned map[string]string) (map[string][]byte, RemoteEntryManifest, error) {
	targets := map[string][]byte{}
	manifest := RemoteEntryManifest{Schema: 1, Version: Version, APIProtocol: 1, SkillProtocol: 1, Assets: map[string]string{}}
	launcher, err := assets.Files.ReadFile("project-launcher.sh")
	if err != nil {
		return nil, manifest, err
	}
	targets["ael"] = launcher
	for _, name := range []string{"SKILL.md", "references/workflow.md", "references/commands.md", "references/remote-agent.md"} {
		body, err := assets.Files.ReadFile("skills/ael-work/" + name)
		if err != nil {
			return nil, manifest, err
		}
		targets[".agents/skills/ael-work/"+name] = body
	}
	targets[".claude/skills/ael-work/SKILL.md"] = []byte("---\nname: ael-work\ndescription: Continue authorized AEL project engineering work or an explicitly requested AEL task.\n---\n\nHandle isolated mechanical formatting and typo corrections directly unless the user requests tracking. In portable clones omit --runner and use the registered checkout identity. For an authorized tracked engineering request, run `./ael session show --session HOST_SESSION_ID --json` and read the returned frozen `session.skill_path`. Prepare a missing tool session with `./ael session start --session HOST_SESSION_ID --runner RUNNER --json`, then read its frozen Skill. After compaction or resume, first run `./ael recovery check --session HOST_SESSION_ID --json`. Ordinary discussion and review follow the user request directly.\n")
	for path, body := range targets {
		manifest.Assets[path] = store.Hash(body)
	}
	for _, filename := range []string{"AGENTS.md", "CLAUDE.md"} {
		raw, err := readEntryBytes(root, filename, 12<<20)
		if err != nil && !os.IsNotExist(err) {
			return nil, manifest, err
		}
		text := string(raw)
		start, finish := strings.Index(text, begin), strings.Index(text, end)
		managed := block("remote")
		key := strings.TrimSuffix(filename, ".md") + ":block"
		if start >= 0 || finish >= 0 {
			if start < 0 || finish < start || strings.Count(text, begin) != 1 || strings.Count(text, end) != 1 {
				return nil, manifest, core.Error("ASSET_CONFLICT", "malformed "+filename+" managed block")
			}
			finish += len(end)
			if store.Hash([]byte(text[start:finish])) != owned[key] {
				return nil, manifest, core.Error("ASSET_CONFLICT", "user edited "+filename+" managed block; content preserved")
			}
			text = text[:start] + managed + text[finish:]
		} else {
			text = strings.TrimRight(text, "\n") + "\n\n" + managed + "\n"
		}
		targets[filename] = []byte(text)
		manifest.Assets[key] = store.Hash([]byte(managed))
	}
	body, _ := json.MarshalIndent(manifest, "", "  ")
	targets[".ael/remote-assets.json"] = append(body, '\n')
	return targets, manifest, nil
}

// UpdateRemoteEntry writes only the selected project's entry and Skill assets.
// Its journal gives interrupted file updates the same hash/conflict semantics as
// the existing Local outbox, while Remote mode never opens a Local task database.
func UpdateRemoteEntry(root string, dry, migrate bool, expectedBindingHash string) (UpdateReport, error) {
	result := UpdateReport{Operation: "update", Files: []string{}, Warnings: []string{"Remote entry update is for subsequent sessions; active Agents keep their acquired instructions", "existing offline binaries and locks remain in place"}, RestartRequired: true}
	root, err := Canonical(root)
	if err != nil {
		return result, err
	}
	result.Target = root
	unlock, err := projectmode.Lock(root, !dry)
	if err != nil {
		return result, err
	}
	defer unlock()
	bindingPath, err := store.SafePath(root, ".ael/remote.json")
	if err != nil {
		return result, err
	}
	bindingHash, err := store.HashFile(bindingPath)
	if err != nil {
		return result, core.Error("REMOTE_BINDING_REQUIRED", "connect the selected project first")
	}
	if len(expectedBindingHash) != 64 || bindingHash != expectedBindingHash {
		return result, core.Error("REMOTE_BINDING_CHANGED", "project binding changed after validation")
	}
	if _, err = os.Lstat(filepath.Join(root, ".ael/project.yaml")); err == nil {
		if err = VerifyRemoteEntryCheckout(root); err != nil {
			return result, err
		}
	} else if !os.IsNotExist(err) {
		return result, err
	}
	var pending remoteEntryIntent
	recoveryErr := readEntryJSON(root, ".ael/remote-entry-update.json", &pending)
	recovering := recoveryErr == nil
	if recoveryErr != nil && !os.IsNotExist(recoveryErr) {
		return result, recoveryErr
	}
	if recovering && (pending.Version != Version || pending.BindingHash != bindingHash) {
		return result, core.Error("REMOTE_ENTRY_RECOVERY_REQUIRED", "retry with the exact original CLI assets and binding")
	}
	owned := map[string]string{}
	var prior RemoteEntryManifest
	err = readEntryJSON(root, ".ael/remote-assets.json", &prior)
	if err == nil {
		if err = prior.validate(); err != nil {
			return result, err
		}
		owned = prior.Assets
		result.Previous.Version = prior.Version
	} else if !os.IsNotExist(err) {
		return result, err
	} else {
		lock, err := store.SafePath(root, ".ael/toolchain.lock")
		if err != nil {
			return result, err
		}
		if _, err = os.Lstat(lock); err == nil {
			if !migrate && !recovering {
				return result, core.Error("REMOTE_ENTRY_MIGRATION_REQUIRED", "use --migrate-entry to replace the owned entry while preserving offline payloads")
			}
			if err = readEntryJSON(root, ".ael/assets.json", &owned); err != nil {
				return result, err
			}
		} else if !os.IsNotExist(err) {
			return result, err
		}
	}
	if recovering {
		for _, filename := range []string{"AGENTS.md", "CLAUDE.md"} {
			actual, readErr := readEntryBytes(root, filename, 12<<20)
			if readErr == nil && bytes.Equal(actual, pending.Targets[filename]) {
				owned[strings.TrimSuffix(filename, ".md")+":block"] = store.Hash([]byte(block("remote")))
			}
		}
	}
	targets, _, err := remoteEntryTargets(root, owned)
	if err != nil {
		return result, err
	}
	intent := remoteEntryIntent{Version: Version, BindingHash: bindingHash, Before: map[string]string{}, Targets: targets}
	if recovering {
		if len(pending.Targets) != len(targets) {
			return result, core.Error("REMOTE_ENTRY_RECOVERY_REQUIRED", "retry with the exact original CLI assets and project binding")
		}
		for path, body := range targets {
			if !bytes.Equal(pending.Targets[path], body) {
				return result, core.Error("REMOTE_ENTRY_RECOVERY_REQUIRED", "original entry inputs changed; preserve the pending update")
			}
		}
		intent = pending
		result.BackupDir = pending.BackupDir
	}
	changed := false
	for relative, wanted := range targets {
		path, err := store.SafePath(root, relative)
		if err != nil {
			return result, err
		}
		actual, err := store.HashFile(path)
		if err != nil && !os.IsNotExist(err) {
			return result, err
		}
		if recovering {
			if actual != intent.Before[relative] && actual != store.Hash(wanted) {
				return result, core.Error("ASSET_CONFLICT", relative+" changed after the interrupted update")
			}
		} else {
			if actual != "" && actual != store.Hash(wanted) && relative != "AGENTS.md" && relative != "CLAUDE.md" && relative != ".ael/remote-assets.json" && actual != owned[relative] {
				return result, core.Error("ASSET_CONFLICT", relative+" user content preserved")
			}
			intent.Before[relative] = actual
		}
		changed = changed || actual != store.Hash(wanted)
		result.Files = append(result.Files, relative)
	}
	sort.Strings(result.Files)
	result.Available = BinaryVersion{Version: Version, Build: Build, OutputProtocol: 1, APIProtocol: 1, SkillProtocol: 1}
	previewIntent, _ := json.MarshalIndent(intent, "", "  ")
	if len(previewIntent) > remoteEntryJournalLimit {
		return result, core.Error("REMOTE_ENTRY_INVALID", "entry update exceeds its readable journal limit")
	}
	if dry {
		result.Status = "preview"
		return result, nil
	}
	if !changed && !recovering {
		result.Status = "up_to_date"
		result.RestartRequired = false
		return result, nil
	}
	if !recovering {
		before := map[string]string{}
		for path, hash := range intent.Before {
			if hash != "" {
				before[path] = hash
			}
		}
		if len(before) > 0 {
			intent.BackupDir, err = backupAssets(root, before)
			if err != nil {
				return result, err
			}
			result.BackupDir = intent.BackupDir
		}
		raw, _ := json.MarshalIndent(intent, "", "  ")
		if len(raw) > remoteEntryJournalLimit {
			return result, core.Error("REMOTE_ENTRY_INVALID", "entry update exceeds its readable journal limit")
		}
		path, err := store.SafePath(root, ".ael/remote-entry-update.json")
		if err != nil {
			return result, err
		}
		if err = store.Atomic(path, raw, 0600); err != nil {
			return result, err
		}
	}
	// Commit the owned manifest after every visible asset has reached its target.
	paths := append([]string{}, result.Files...)
	sort.SliceStable(paths, func(i, j int) bool {
		return paths[i] != ".ael/remote-assets.json" && paths[j] == ".ael/remote-assets.json"
	})
	for _, relative := range paths {
		path, err := store.SafePath(root, relative)
		if err != nil {
			return result, err
		}
		actual, hashErr := store.HashFile(path)
		if hashErr != nil && !os.IsNotExist(hashErr) {
			return result, hashErr
		}
		wanted := intent.Targets[relative]
		if actual == store.Hash(wanted) {
			continue
		}
		if actual != intent.Before[relative] {
			return result, core.Error("ASSET_CONFLICT", relative+" changed during entry update")
		}
		mode := os.FileMode(0644)
		if relative == "ael" {
			mode = 0755
		}
		if err = store.Atomic(path, wanted, mode); err != nil {
			return result, err
		}
	}
	if err = os.Remove(filepath.Join(root, ".ael/remote-entry-update.json")); err != nil {
		return result, err
	}
	result.Status = "updated"
	return result, nil
}

func remoteManagedBlock(root, filename string) (string, error) {
	raw, err := readEntryBytes(root, filename, 12<<20)
	if err != nil {
		return "", err
	}
	text := string(raw)
	start, finish := strings.Index(text, begin), strings.Index(text, end)
	if start < 0 || finish < start || strings.Count(text, begin) != 1 || strings.Count(text, end) != 1 {
		return "", core.Error("ASSET_CONFLICT", "invalid managed block in "+filename)
	}
	return text[start : finish+len(end)], nil
}

// A clone of a previously transferred project can carry the old Local files
// without carrying a Local runtime. An existing runtime still requires proof
// that the original authority was frozen.
func VerifyRemoteEntryCheckout(root string) error {
	db, err := projectReadDB(root)
	if err != nil {
		return err
	}
	if db != nil {
		db.Close()
		return VerifyTransferredCheckout(root)
	}
	var manifest RemoteEntryManifest
	if err := readEntryJSON(root, ".ael/remote-assets.json", &manifest); err != nil {
		return core.Error("LOCAL_PROJECT_EXISTS", "managed remote entry or verified Local transfer required")
	}
	return manifest.validate()
}
