package bootstrap

import (
	"ael/assets"
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func validationPaths(b Bundle) []string {
	out := []string{"ael", "AGENTS.md", ".ael/project.yaml", ".ael/assets.json", ".ael/toolchain.lock", ".ael/THIRD_PARTY_NOTICES.txt", ".ael/.gitignore", ".agents/skills/ael-work/SKILL.md", ".agents/skills/ael-migrate-legacy/SKILL.md", ".agents/skills/ael-work/references/workflow.md", ".agents/skills/ael-work/references/commands.md"}
	for _, a := range b.Assets {
		out = append(out, filepath.Join(".ael/bin", a.Platform, "ael"))
	}
	return out
}
func managedBlock(root string) (string, error) {
	p, e := store.SafePath(root, "AGENTS.md")
	if e != nil {
		return "", e
	}
	raw, e := os.ReadFile(p)
	if e != nil {
		return "", e
	}
	text := string(raw)
	start, finish := strings.Index(text, begin), strings.Index(text, end)
	if start < 0 || finish < start || strings.Count(text, begin) != 1 || strings.Count(text, end) != 1 {
		return "", core.Error("UPDATE_VERIFY_FAILED", "AGENTS managed block")
	}
	return text[start : finish+len(end)], nil
}

// Executed by the source release, using that release's own embedded assets.
func validateOwnAssets(root string, b Bundle, config Config) (*UpdateValidation, error) {
	if e := validateUpdatedProject(root, b, config); e != nil {
		return nil, e
	}
	wanted := bundleText(b)
	for _, name := range []string{"ael-work", "ael-migrate-legacy"} {
		body, e := assets.Files.ReadFile("skills/" + name + "/SKILL.md")
		if e != nil {
			return nil, e
		}
		wanted[".agents/skills/"+name+"/SKILL.md"] = body
	}
	for _, name := range []string{"workflow.md", "commands.md"} {
		body, e := assets.Files.ReadFile("skills/ael-work/references/" + name)
		if e != nil {
			return nil, e
		}
		wanted[".agents/skills/ael-work/references/"+name] = body
	}
	for rel, body := range wanted {
		p, e := store.SafePath(root, rel)
		if e != nil {
			return nil, e
		}
		hash, e := store.HashFile(p)
		if e != nil || hash != store.Hash(body) {
			return nil, core.Error("UPDATE_VERIFY_FAILED", rel)
		}
	}
	text, e := managedBlock(root)
	if e != nil {
		return nil, e
	}
	if text != block(config.Mode) {
		return nil, core.Error("UPDATE_VERIFY_FAILED", "source AGENTS entry not installed")
	}
	proof := &UpdateValidation{SourceSHA256: store.Hash(distributionLock(b)), Assets: map[string]string{}}
	for _, rel := range validationPaths(b) {
		p, e := store.SafePath(root, rel)
		if e != nil {
			return nil, e
		}
		hash, e := store.HashFile(p)
		if e != nil {
			return nil, e
		}
		proof.Assets[rel] = hash
	}
	if e = verifyValidation(root, b, config, proof); e != nil {
		return nil, e
	}
	return proof, nil
}

// The delegating client checks the complete receipt against disk, the locked
// package and the owned manifest. An incomplete source updater cannot report pass.
func verifyValidation(root string, b Bundle, before Config, proof *UpdateValidation) error {
	if proof == nil || proof.SourceSHA256 != store.Hash(distributionLock(b)) {
		return core.Error("UPDATE_VERIFY_FAILED", "missing or wrong source validation receipt")
	}
	if e := validateUpdatedProject(root, b, before); e != nil {
		return e
	}
	for _, rel := range validationPaths(b) {
		want := proof.Assets[rel]
		if len(want) != 64 {
			return core.Error("UPDATE_VERIFY_FAILED", "receipt omits "+rel)
		}
		p, e := store.SafePath(root, rel)
		if e != nil {
			return e
		}
		hash, e := store.HashFile(p)
		if e != nil || hash != want {
			return core.Error("UPDATE_VERIFY_FAILED", rel)
		}
	}
	lock, e := store.SafePath(root, ".ael/toolchain.lock")
	if e != nil {
		return e
	}
	raw, e := os.ReadFile(lock)
	if e != nil {
		return e
	}
	if string(raw) != string(projectLock(b)) {
		return core.Error("UPDATE_VERIFY_FAILED", "toolchain lock disagrees with source bundle")
	}
	raw, e = os.ReadFile(filepath.Join(root, ".ael/assets.json"))
	if e != nil {
		return e
	}
	var manifest map[string]string
	if e = json.Unmarshal(raw, &manifest); e != nil {
		return e
	}
	for rel, hash := range proof.Assets {
		if rel == ".ael/assets.json" || rel == "AGENTS.md" {
			continue
		}
		if manifest[rel] != hash {
			return core.Error("UPDATE_VERIFY_FAILED", "owned manifest disagrees with "+rel)
		}
	}
	text, e := managedBlock(root)
	if e != nil {
		return e
	}
	if manifest["AGENTS:block"] != store.Hash([]byte(text)) {
		return core.Error("UPDATE_VERIFY_FAILED", "AGENTS ownership manifest")
	}
	return nil
}
