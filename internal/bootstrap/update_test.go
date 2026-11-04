package bootstrap

import (
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// These shell programs are installer protocol fixtures, not real AEL releases.
// Real version-to-version installation is verified separately with built binaries.
func updateFixture(t *testing.T, version, build string) string {
	t.Helper()
	t.Setenv("AEL_BUNDLE_DIR", "")
	dir := t.TempDir()
	body := []byte(fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' '{\"version\":\"%s\",\"build\":\"%s\"}'\n", version, build))
	text := "version " + version + "\n"
	for _, platform := range []string{"darwin-arm64", "darwin-amd64", "linux-arm64", "linux-amd64"} {
		name := "ael-" + platform
		if e := os.WriteFile(filepath.Join(dir, name), body, 0755); e != nil {
			t.Fatal(e)
		}
		text += platform + " " + store.Hash(body) + " " + name + "\n"
	}
	notice := []byte("fixture notice")
	os.WriteFile(filepath.Join(dir, "NOTICE"), notice, 0644)
	text += "notice " + store.Hash(notice) + " NOTICE\n"
	os.WriteFile(filepath.Join(dir, "ael-toolchain.lock"), []byte(text), 0644)
	return dir
}

func TestUpgradeInterruptedReplacementRecovery(t *testing.T) {
	source := updateFixture(t, Version, "recovery-fixture")
	b := mustBundle(t, source)
	native, _ := nativeAsset(b)
	prefix := t.TempDir()
	os.MkdirAll(filepath.Join(prefix, "bin"), 0755)
	os.MkdirAll(filepath.Join(prefix, "share/ael"), 0755)
	// Inject the exact crash window after executable replacement, before metadata.
	store.CopyAtomic(filepath.Join(source, native.Path), filepath.Join(prefix, "bin/ael"), 0755)
	in := upgradeIntent{State: "installing", Next: installation{Version: Version, SHA256: native.SHA256, Source: b.Root}}
	raw, _ := json.Marshal(in)
	os.WriteFile(filepath.Join(prefix, "share/ael/upgrade-intent.json"), raw, 0600)
	r, e := Upgrade(UpdateOptions{From: source, Prefix: prefix})
	if e != nil || r.Status != "updated" {
		t.Fatal(r, e)
	}
	done, e := readUpgradeIntent(prefix)
	if e != nil || done.State != "complete" {
		t.Fatal(done, e)
	}
	if _, e = installedHash(prefix); e != nil {
		t.Fatal(e)
	}
}
func TestUpgradePreviewInstallSourceAndIdempotence(t *testing.T) {
	source := updateFixture(t, Version, "fixture-new")
	prefix := filepath.Join(t.TempDir(), "installation")
	r, e := Upgrade(UpdateOptions{From: source, Prefix: prefix, DryRun: true})
	if e != nil || r.Status != "preview" {
		t.Fatal(r, e)
	}
	if _, e = os.Stat(prefix); !os.IsNotExist(e) {
		t.Fatal("preview wrote installation")
	}
	r, e = Upgrade(UpdateOptions{From: source, Prefix: prefix})
	if e != nil || r.Status != "updated" {
		t.Fatal(r, e)
	}
	registry := filepath.Join(filepath.Dir(filepath.Dir(r.Target)), "share/ael/installation.json")
	before, _ := os.ReadFile(registry)
	in, e := readInstallation(filepath.Dir(filepath.Dir(r.Target)))
	if e != nil || in.Source != r.Source || in.Bundle == "" {
		t.Fatal(in, e)
	}
	for i := 0; i < 3; i++ {
		r, e = Upgrade(UpdateOptions{Prefix: prefix})
		if e != nil || r.Status != "up_to_date" {
			t.Fatal(r, e)
		}
	}
	after, _ := os.ReadFile(registry)
	if string(before) != string(after) {
		t.Fatal("no-op upgrade rewrote installation metadata")
	}
	b, e := sourceBundle(UpdateOptions{Prefix: prefix}, false)
	if e != nil || b.Root != in.Bundle {
		t.Fatal(b, e)
	}
	if e = os.RemoveAll(in.Bundle); e != nil {
		t.Fatal(e)
	}
	r, e = Upgrade(UpdateOptions{From: source, Prefix: prefix})
	if e != nil || r.Status != "updated" {
		t.Fatal("missing cached distribution was not repaired", r, e)
	}
	if _, e = sourceBundle(UpdateOptions{Prefix: prefix}, false); e != nil {
		t.Fatal(e)
	}
}
func TestUpgradeLegacyBackupAndUserProtection(t *testing.T) {
	oldSource := updateFixture(t, "0.1.3", "fixture-old")
	newSource := updateFixture(t, Version, "fixture-new")
	prefix := t.TempDir()
	os.MkdirAll(filepath.Join(prefix, "bin"), 0755)
	os.MkdirAll(filepath.Join(prefix, "share/ael"), 0755)
	native, _ := nativeAsset(mustBundle(t, oldSource))
	old, _ := os.ReadFile(filepath.Join(oldSource, native.Path))
	target := filepath.Join(prefix, "bin/ael")
	os.WriteFile(target, old, 0755)
	if _, e := Upgrade(UpdateOptions{From: newSource, Prefix: prefix}); e == nil || !strings.Contains(e.Error(), "UNMANAGED_INSTALLATION") {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(prefix, "share/ael/install.sha256"), []byte(store.Hash(old)+"\n"), 0644)
	r, e := Upgrade(UpdateOptions{From: newSource, Prefix: prefix})
	if e != nil || r.Previous.Version != "0.1.3" {
		t.Fatal(r, e)
	}
	backup, e := os.ReadFile(filepath.Join(r.BackupDir, "ael"))
	if e != nil || string(backup) != string(old) {
		t.Fatal("old executable not preserved", e)
	}
	os.WriteFile(target, []byte("user binary"), 0755)
	if _, e = Upgrade(UpdateOptions{From: newSource, Prefix: prefix}); e == nil || !strings.Contains(e.Error(), "INSTALLATION_MODIFIED") {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(target)
	if string(b) != "user binary" {
		t.Fatal("overwrote user binary")
	}
}
func mustBundle(t *testing.T, dir string) Bundle {
	t.Helper()
	b, e := updateBundle(dir)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestUpgradeImmutableVersionAndDowngrade(t *testing.T) {
	prefix := t.TempDir()
	one := updateFixture(t, Version, "one")
	two := updateFixture(t, Version, "two")
	if _, e := Upgrade(UpdateOptions{From: one, Prefix: prefix}); e != nil {
		t.Fatal(e)
	}
	if _, e := Upgrade(UpdateOptions{From: two, Prefix: prefix}); e == nil || !strings.Contains(e.Error(), "DISTRIBUTION_VERSION_CONFLICT") {
		t.Fatal(e)
	}
	if _, e := Upgrade(UpdateOptions{From: updateFixture(t, "0.1.3", "old"), Prefix: prefix}); e == nil || !strings.Contains(e.Error(), "DOWNGRADE_REFUSED") {
		t.Fatal(e)
	}
}
func TestUpdateOnlyInitializedProjectAndReadOnlyBusyCheck(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	if _, e := UpdateProject(empty, UpdateOptions{From: "missing"}); e == nil || !strings.Contains(e.Error(), "NOT_INITIALIZED") {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(empty, ".ael")); !os.IsNotExist(e) {
		t.Fatal("update initialized a project")
	}
	root := t.TempDir()
	if _, e := Init(root, false, false); e != nil {
		t.Fatal(e)
	}
	s, _, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	n, e := s.Create(core.Definition{Title: "isolated update fence test", Type: "bug", Goal: "verified", Acceptance: []string{"pass"}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Claim(n.Task.Definition.ID, "", "fixture-owner", "", ""); e != nil {
		t.Fatal(e)
	}
	s.Store.Close()
	before, _ := os.ReadFile(filepath.Join(DataDir(root), "runtime.db"))
	if _, e = UpdateProject(root, UpdateOptions{From: "missing", DryRun: true}); e == nil || !strings.Contains(e.Error(), "PROJECT_BUSY") {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(filepath.Join(DataDir(root), "runtime.db"))
	if store.Hash(before) != store.Hash(after) {
		t.Fatal("busy inspection changed DB")
	}
}
func TestProjectUpdateAssetsIdentityBackupsAndPreviewConflict(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	source := updateFixture(t, Version, "new")
	bundle := mustBundle(t, source)
	if _, e := InitProject(root, false, false, source); e != nil {
		t.Fatal(e)
	}
	config, _ := Read(root)
	id := config.ProjectID
	raw, _ := os.ReadFile(filepath.Join(root, ".ael/project.yaml"))
	var extra map[string]any
	json.Unmarshal(raw, &extra)
	extra["custom"] = map[string]any{"keep": true}
	extra["asset_version"] = "0.1.3"
	extra["mode"] = "shadow"
	raw, _ = json.Marshal(extra)
	os.WriteFile(filepath.Join(root, ".ael/project.yaml"), raw, 0644)
	config, _ = Read(root)
	skill := filepath.Join(root, ".agents/skills/ael-work/SKILL.md")
	original, _ := os.ReadFile(skill)
	os.WriteFile(skill, []byte("user skill"), 0644)
	r := UpdateReport{Operation: "update", Target: root, Available: BinaryVersion{Version: Version}}
	if _, e := applyProjectUpdate(root, UpdateOptions{DryRun: true}, bundle, config, r); e == nil || !strings.Contains(e.Error(), "ASSET_CONFLICT") {
		t.Fatal("preview missed skill conflict", e)
	}
	os.WriteFile(skill, original, 0644)
	result, e := applyProjectUpdate(root, UpdateOptions{}, bundle, config, r)
	if e != nil || result.Status != "updated" || result.BackupDir == "" {
		t.Fatal(result, e)
	}
	after, _ := Read(root)
	if after.ProjectID != id || after.Mode != "shadow" || after.Version != Version {
		t.Fatal(after)
	}
	raw, _ = os.ReadFile(filepath.Join(root, ".ael/project.yaml"))
	if !strings.Contains(string(raw), "custom") {
		t.Fatal("lost custom project config")
	}
	prior, _ := os.ReadFile(filepath.Join(result.BackupDir, "project/.ael/project.yaml"))
	if !strings.Contains(string(prior), "0.1.3") {
		t.Fatal("backup did not preserve old version")
	}
	if _, e = os.Stat(filepath.Join(root, ".ael/tool-update.lock")); !os.IsNotExist(e) {
		t.Fatal("update lock retained after success")
	}
}
func TestUpdateLockAndVersionComparison(t *testing.T) {
	root := t.TempDir()
	release, e := updateLock(root, ".ael/tool-update.lock")
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	if e = noUpdateLock(root, ".ael/tool-update.lock"); e == nil {
		t.Fatal("ignored updater lock")
	}
	if cmp, e := compareVersions("0.1.10", "0.1.9"); e != nil || cmp != 1 {
		t.Fatal(cmp, e)
	}
	if _, e = compareVersions("../../escape", "0.1.5"); e == nil {
		t.Fatal("unsafe version accepted")
	}
}

func TestUpdateDelegatesToSourceReleaseForPreview(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	if _, e := Init(root, false, false); e != nil {
		t.Fatal(e)
	}
	source := updateFixture(t, "0.1.8", "fixture-future")
	trace := filepath.Join(t.TempDir(), "delegated.txt")
	t.Setenv("AEL_DELEGATION_TRACE", trace)
	program := []byte(`#!/bin/sh
if [ "$1" = version ]; then printf '%s\n' '{"version":"0.1.8","build":"fixture-future"}'; exit 0; fi
printf '%s\n' "$@" > "$AEL_DELEGATION_TRACE"
shift
while [ "$#" -gt 0 ]; do
 case "$1" in --project) shift; target=$1 ;; esac
 shift
done
printf '{"operation":"update","status":"preview","target":"%s","available":{"version":"0.1.8","build":"fixture-future"},"files":[]}' "$target"
`)
	b := mustBundle(t, source)
	text := "version 0.1.8\n"
	for _, a := range b.Assets {
		os.WriteFile(filepath.Join(source, a.Path), program, 0755)
		text += a.Platform + " " + store.Hash(program) + " " + a.Path + "\n"
	}
	notice, _ := os.ReadFile(filepath.Join(source, "NOTICE"))
	text += "notice " + store.Hash(notice) + " NOTICE\n"
	os.WriteFile(filepath.Join(source, "ael-toolchain.lock"), []byte(text), 0644)
	r, e := UpdateProject(root, UpdateOptions{From: source, DryRun: true})
	if e != nil || r.Available.Version != "0.1.8" || r.Status != "preview" {
		t.Fatal(r, e)
	}
	called, _ := os.ReadFile(trace)
	if !strings.Contains(string(called), "--dry-run") || !strings.HasPrefix(string(called), "update\n") {
		t.Fatal(string(called))
	}
	config, _ := Read(root)
	if config.Version != Version {
		t.Fatal("preview changed project version")
	}
}

func TestMaintenanceSerializesClaimsAndLegacyWrites(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	if _, e := Init(root, false, false); e != nil {
		t.Fatal(e)
	}
	s, _, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Store.Close()
	n, e := s.Create(core.Definition{Title: "claim/update transaction race", Type: "bug", Goal: "verified", Acceptance: []string{"pass"}})
	if e != nil {
		t.Fatal(e)
	}
	ready, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s.ConstraintCheck = func(core.Definition) []string { once.Do(func() { close(ready) }); <-resume; return nil }
	outcome := make(chan error, 1)
	go func() { _, e := s.Claim(n.Task.Definition.ID, "", "racing-writer", "", ""); outcome <- e }()
	<-ready
	b := mustBundle(t, updateFixture(t, Version, "race-fixture"))
	gate, owner, e := beginMaintenance(root, b, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer gate.Close()
	// Legacy clients have no metadata check. Their existing SQL still hits the fence.
	e = gate.Write(func(tx *store.Tx) error {
		_, e := tx.Exec("INSERT INTO workspace_locks(workspace,attempt,task) VALUES('elsewhere','legacy','legacy')")
		return e
	})
	if e == nil || !strings.Contains(e.Error(), "PROJECT_UPDATING") {
		t.Fatal("legacy write crossed maintenance", e)
	}
	close(resume)
	if e = <-outcome; e == nil || !strings.Contains(e.Error(), "PROJECT_UPDATING") {
		t.Fatal("claim crossed maintenance", e)
	}
	if e = endMaintenance(gate, owner); e != nil {
		t.Fatal(e)
	}
}

func TestUpdateGenerationRejectsPreUpdateClaimAfterRelease(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	Init(root, false, false)
	s, _, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Store.Close()
	n, e := s.Create(core.Definition{Title: "generation fence", Type: "bug", Goal: "verified", Acceptance: []string{"pass"}})
	if e != nil {
		t.Fatal(e)
	}
	ready, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s.ConstraintCheck = func(core.Definition) []string { once.Do(func() { close(ready) }); <-resume; return nil }
	outcome := make(chan error, 1)
	go func() { _, e := s.Claim(n.Task.Definition.ID, "", "stale-writer", "", ""); outcome <- e }()
	<-ready
	b := mustBundle(t, updateFixture(t, Version, "generation-fixture"))
	gate, owner, e := beginMaintenance(root, b, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = endMaintenance(gate, owner); e != nil {
		t.Fatal(e)
	}
	gate.Close()
	close(resume)
	if e = <-outcome; e == nil || !strings.Contains(e.Error(), "PROJECT_CHANGED") {
		t.Fatal("stale pre-update input accepted", e)
	}
}

func TestUpdateFullReceiptDetectsStaleLockAndSkill(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	source := updateFixture(t, Version, "receipt-fixture")
	b := mustBundle(t, source)
	InitProject(root, false, false, source)
	config, _ := Read(root)
	proof, e := validateOwnAssets(root, b, config)
	if e != nil {
		t.Fatal(e)
	}
	for _, rel := range []string{"ael", ".ael/toolchain.lock", ".agents/skills/ael-work/SKILL.md", ".agents/skills/ael-work/references/workflow.md", ".ael/THIRD_PARTY_NOTICES.txt", ".ael/assets.json"} {
		p := filepath.Join(root, rel)
		raw, _ := os.ReadFile(p)
		os.WriteFile(p, append(raw, []byte("changed")...), 0644)
		if e = verifyValidation(root, b, config, proof); e == nil {
			t.Fatal("accepted stale asset", rel)
		}
		os.WriteFile(p, raw, 0644)
	}
	if e = verifyValidation(root, b, config, nil); e == nil {
		t.Fatal("missing full receipt accepted")
	}
}

func TestInterruptedProjectUpdateExactSourceRecovery(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	source := updateFixture(t, Version, "project-recovery-fixture")
	b := mustBundle(t, source)
	InitProject(root, false, false, source)
	config, _ := Read(root)
	before, _ := managedHashes(root)
	backup, e := backupAssets(root, before)
	if e != nil {
		t.Fatal(e)
	}
	gate, owner, e := beginMaintenance(root, b, nil)
	if e != nil {
		t.Fatal(e)
	}
	saveMaintenanceBackup(gate, owner, backup)
	// Explicit crash fixture: the updater owner is gone, and an owned file intent remains.
	owner.PID = 999999999
	owner.Identity = "stopped fixture"
	body, _ := json.Marshal(owner)
	e = gate.Write(func(tx *store.Tx) error {
		_, e := tx.Exec("UPDATE metadata SET body=? WHERE key='tool-update'", string(body))
		if e != nil {
			return e
		}
		raw, _ := os.ReadFile(filepath.Join(root, ".agents/skills/ael-work/SKILL.md"))
		return tx.File(filepath.Join(root, ".agents/skills/ael-work/SKILL.md"), raw)
	})
	if e != nil {
		t.Fatal(e)
	}
	gate.Close()
	if _, e = prepareMaintenance(root, false); e == nil || !strings.Contains(e.Error(), "UPDATE_RECOVERY_REQUIRED") {
		t.Fatal(e)
	}
	r, e := applyProjectUpdate(root, UpdateOptions{AckStopped: true}, b, config, UpdateReport{Operation: "update"})
	if e != nil || r.BackupDir != backup || r.Validation == nil {
		t.Fatal(r, e)
	}
	pending, e := readMaintenance(root)
	if e != nil || pending != nil {
		t.Fatal("maintenance not released", pending, e)
	}
}

func TestRecoveryBadBackupKeepsMaintenanceFence(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	source := updateFixture(t, Version, "bad-backup-fixture")
	b := mustBundle(t, source)
	InitProject(root, false, false, source)
	config, _ := Read(root)
	hashes, _ := managedHashes(root)
	backup, e := backupAssets(root, hashes)
	if e != nil {
		t.Fatal(e)
	}
	gate, owner, e := beginMaintenance(root, b, nil)
	if e != nil {
		t.Fatal(e)
	}
	saveMaintenanceBackup(gate, owner, backup)
	owner.PID = 999999999
	owner.Identity = "stopped crash fixture"
	raw, _ := json.Marshal(owner)
	if e = gate.Write(func(tx *store.Tx) error {
		_, e := tx.Exec("UPDATE metadata SET body=? WHERE key='tool-update'", string(raw))
		return e
	}); e != nil {
		t.Fatal(e)
	}
	gate.Close()
	os.Remove(filepath.Join(backup, "manifest.json"))
	if _, e = applyProjectUpdate(root, UpdateOptions{AckStopped: true}, b, config, UpdateReport{Operation: "update"}); e == nil {
		t.Fatal("bad recovery backup accepted")
	}
	remaining, e := readMaintenance(root)
	if e != nil || remaining == nil {
		t.Fatal("failed recovery released maintenance", remaining, e)
	}
}
