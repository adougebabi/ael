package bootstrap

import (
	"ael/internal/process"
	"ael/internal/store"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMachineUpgradeRecoveryRequiresStoppedExactOwner(t *testing.T) {
	prefix := t.TempDir()
	source := clientPackageFixture(t, Version)
	if _, err := Upgrade(UpdateOptions{From: source, Prefix: prefix}); err != nil {
		t.Fatal(err)
	}
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if child.ProcessState == nil {
			child.Process.Kill()
			child.Wait()
		}
	}()
	identity, err := process.Identity(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(prefix, "share/ael/upgrade.lock")
	os.Mkdir(lock, 0700)
	owner, _ := json.Marshal(map[string]any{"pid": child.Process.Pid, "identity": identity})
	os.WriteFile(filepath.Join(lock, "owner.json"), owner, 0600)
	if _, err := Upgrade(UpdateOptions{From: source, Prefix: prefix, AckStopped: true}); err == nil || !strings.Contains(err.Error(), "UPDATE_OWNER_ALIVE") {
		t.Fatalf("accepted live owner %v", err)
	}
	child.Process.Kill()
	child.Wait()
	if _, err := Upgrade(UpdateOptions{From: source, Prefix: prefix, AckStopped: true, DryRun: true}); err == nil {
		t.Fatal("dry-run removed stale owner")
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatal("preview removed lock", err)
	}
	if result, err := Upgrade(UpdateOptions{From: source, Prefix: prefix, AckStopped: true}); err != nil || result.Status != "up_to_date" {
		t.Fatalf("stopped updater did not recover %+v %v", result, err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatal("stale updater lock remains")
	}
}

func TestMachineUpgradePermissionFailureKeepsOldExecutable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses mode-bit write restrictions")
	}
	prefix := t.TempDir()
	old := clientPackageFixture(t, Version)
	if _, err := Upgrade(UpdateOptions{From: old, Prefix: prefix}); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(prefix, "bin/ael")
	before, _ := store.HashFile(binary)
	next := clientPackageFixture(t, "99.0.0")
	binDir := filepath.Dir(binary)
	os.Chmod(binDir, 0500)
	defer os.Chmod(binDir, 0700)
	if _, err := Upgrade(UpdateOptions{From: next, Prefix: prefix}); err == nil {
		t.Fatal("expected real filesystem permission rejection")
	}
	after, _ := store.HashFile(binary)
	if before != after {
		t.Fatal("failed upgrade changed old executable")
	}
	if version, err := probeBinary(binary); err != nil || version.Version != Version {
		t.Fatalf("old tool unavailable %+v %v", version, err)
	}
	os.Chmod(binDir, 0700)
	if result, err := Upgrade(UpdateOptions{From: next, Prefix: prefix}); err != nil || result.Status != "updated" {
		t.Fatalf("exact-source retry failed %+v %v", result, err)
	}
}
