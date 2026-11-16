package bootstrap

import (
	"ael/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestRollbackReusesExactBackupAndKeepsBothCaches(t *testing.T) {
	prefix := t.TempDir()
	oldSource := clientPackageFixture(t, "1.0.0")
	if _, err := Upgrade(UpdateOptions{From: oldSource, Prefix: prefix}); err != nil {
		t.Fatal(err)
	}
	oldHash, _ := store.HashFile(filepath.Join(prefix, "bin/ael"))
	updated, err := Upgrade(UpdateOptions{From: clientPackageFixture(t, "2.0.0"), Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Upgrade(UpdateOptions{From: oldSource, Prefix: prefix}); err == nil {
		t.Fatal("ordinary upgrade allowed downgrade")
	}
	preview, err := Rollback(UpdateOptions{From: updated.BackupDir, Prefix: prefix, DryRun: true})
	if err != nil || preview.Status != "preview" {
		t.Fatalf("preview %+v %v", preview, err)
	}
	current, err := InstalledClient(prefix)
	if err != nil || current.Version.Version != "2.0.0" {
		t.Fatalf("preview changed installation %+v %v", current, err)
	}
	rolledBack, err := Rollback(UpdateOptions{From: updated.BackupDir, Prefix: prefix})
	if err != nil || rolledBack.Operation != "rollback" || rolledBack.Available.Version != "1.0.0" {
		t.Fatalf("rollback %+v %v", rolledBack, err)
	}
	if hash, _ := store.HashFile(filepath.Join(prefix, "bin/ael")); hash != oldHash {
		t.Fatal("matching old bytes not restored")
	}
	if _, err := CachedClient(prefix, "2.0.0", "client-fixture"); err != nil {
		t.Fatal("new cache removed", err)
	}
	repeated, err := Rollback(UpdateOptions{From: updated.BackupDir, Prefix: prefix})
	if err != nil || repeated.Status != "up_to_date" {
		t.Fatalf("rollback was not idempotent %+v %v", repeated, err)
	}
	os.WriteFile(filepath.Join(updated.BackupDir, "ael"), []byte("modified"), 0755)
	if _, err := Rollback(UpdateOptions{From: updated.BackupDir, Prefix: prefix}); err == nil {
		t.Fatal("modified backup accepted")
	}
}
