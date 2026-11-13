package bootstrap

import (
	"ael/internal/remote"
	"ael/internal/store"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func remoteEntryFixture(t *testing.T) string {
	t.Helper()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, ".ael"), 0700)
	os.WriteFile(filepath.Join(root, ".ael/remote.json"), []byte(`{"schema":1,"profile":"central","project_id":"shared"}`), 0644)
	os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("User engineering rules\n"), 0644)
	return root
}

func TestRemoteEntryRecoveryAfterAGENTSWasWritten(t *testing.T) {
	root := remoteEntryFixture(t)
	assertInterruptedRemoteEntryRecovers(t, root)
}

func TestRemoteEntryRecoveryPreservesLargeAGENTS(t *testing.T) {
	root := remoteEntryFixture(t)
	os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(strings.Repeat("User rules.\n", 75000)), 0644)
	assertInterruptedRemoteEntryRecovers(t, root)
}

func assertInterruptedRemoteEntryRecovers(t *testing.T, root string) {
	t.Helper()
	targets, _, err := remoteEntryTargets(root, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	_, _, bindingHash, _ := remote.ReadBindingSnapshot(root)
	intent := remoteEntryIntent{Version: Version, BindingHash: bindingHash, Before: map[string]string{}, Targets: targets}
	for relative := range targets {
		hash, _ := store.HashFile(filepath.Join(root, relative))
		intent.Before[relative] = hash
	}
	raw, _ := json.Marshal(intent)
	os.WriteFile(filepath.Join(root, ".ael/remote-entry-update.json"), raw, 0600)
	os.WriteFile(filepath.Join(root, "AGENTS.md"), targets["AGENTS.md"], 0644)
	result, err := updateRemoteEntryTest(root, false, false)
	if err != nil || result.Status != "updated" {
		t.Fatalf("legitimate partial update did not recover %+v %v", result, err)
	}
	if info, err := RemoteEntryInfo(root); err != nil || info.Status != "current" {
		t.Fatalf("entry incomplete %+v %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(DataDir(root), "runtime.db")); !os.IsNotExist(err) {
		t.Fatal("Remote update created a Local database")
	}
}

func TestRemoteEntryPreservesUserContentAndOfflinePayload(t *testing.T) {
	root := remoteEntryFixture(t)
	old := []byte("#!/bin/sh\necho legacy\n")
	os.WriteFile(filepath.Join(root, "ael"), old, 0755)
	os.WriteFile(filepath.Join(root, ".ael/toolchain.lock"), []byte("legacy lock\n"), 0644)
	os.MkdirAll(filepath.Join(root, ".ael/bin/darwin-arm64"), 0755)
	os.WriteFile(filepath.Join(root, ".ael/bin/darwin-arm64/ael"), []byte("legacy payload"), 0755)
	owned, _ := json.Marshal(map[string]string{"ael": store.Hash(old)})
	os.WriteFile(filepath.Join(root, ".ael/assets.json"), owned, 0644)
	if _, err := updateRemoteEntryTest(root, false, false); err == nil || !strings.Contains(err.Error(), "REMOTE_ENTRY_MIGRATION_REQUIRED") {
		t.Fatalf("implicit migration accepted %v", err)
	}
	before, _ := os.ReadFile(filepath.Join(root, "ael"))
	if string(before) != string(old) {
		t.Fatal("rejected migration changed launcher")
	}
	if result, err := updateRemoteEntryTest(root, true, true); err != nil || result.Status != "preview" {
		t.Fatalf("preview %+v %v", result, err)
	}
	before, _ = os.ReadFile(filepath.Join(root, "ael"))
	if string(before) != string(old) {
		t.Fatal("preview changed launcher")
	}
	result, err := updateRemoteEntryTest(root, false, true)
	if err != nil || result.Status != "updated" || result.BackupDir == "" {
		t.Fatalf("migration %+v %v", result, err)
	}
	payload, _ := os.ReadFile(filepath.Join(root, ".ael/bin/darwin-arm64/ael"))
	if string(payload) != "legacy payload" {
		t.Fatal("offline payload changed")
	}
	agents, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(string(agents), "User engineering rules") || !strings.Contains(string(agents), "context show") {
		t.Fatal("user rules lost or wrong Remote command")
	}
	agents = append([]byte("Additional user instructions\n"), agents...)
	os.WriteFile(filepath.Join(root, "AGENTS.md"), agents, 0644)
	if result, err := updateRemoteEntryTest(root, false, false); err != nil || result.Status != "up_to_date" {
		t.Fatalf("outside-block edits were overwritten or blocked %+v %v", result, err)
	}
	os.WriteFile(filepath.Join(root, "ael"), []byte("user edited launcher"), 0755)
	if _, err := updateRemoteEntryTest(root, false, false); err == nil || !strings.Contains(err.Error(), "ASSET_CONFLICT") {
		t.Fatalf("user launcher overwritten %v", err)
	}
}

func updateRemoteEntryTest(root string, dry, migrate bool) (UpdateReport, error) {
	_, _, hash, err := remote.ReadBindingSnapshot(root)
	if err != nil {
		return UpdateReport{}, err
	}
	return UpdateRemoteEntry(root, dry, migrate, hash)
}
