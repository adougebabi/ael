package server

import (
	"ael/internal/store"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func quiesceBackupFixture(t *testing.T, f artifactFixture) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for _, input := range []maintenanceInput{
		{Action: "drain", Reason: "backup fixture", Deadline: &deadline, RequestKey: "backup-drain"},
		{Action: "quiesce", Reason: "backup fixture", RequestKey: "backup-quiesce"},
	} {
		standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/admin/maintenance", input, f.admin, f.csrf, "", "http://server.test", "1"), 200)
	}
}

func TestCentralBackupRestorePreservesAllTablesAndImages(t *testing.T) {
	f := newArtifactFixture(t)
	data := fixturePNG(t)
	start := map[string]any{"task_id": "task", "expected_revision": f.revision, "filename": "snapshot.png", "mime": "image/png", "size": len(data), "sha256": store.Hash(data), "source": "fixture", "operation_id": "image", "idempotency_key": "image"}
	uploaded := standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/projects/shared/artifacts/uploads", start, nil, "", f.uploader, "", "1"), 201)
	path := "/api/v1/projects/shared/artifacts/uploads/" + uploaded["upload_id"].(string)
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", path+"/chunks", map[string]any{"offset": 0, "data": data}, nil, "", f.uploader, "", "1"), 200)
	artifact := standaloneJSON(t, standaloneRequest(t, f.h, "POST", path+"/finalize", map[string]string{"operation_id": "image", "idempotency_key": "image"}, nil, "", f.uploader, "", "1"), 200)
	quiesceBackupFixture(t, f)
	backup := filepath.Join(t.TempDir(), "export")
	manifest, err := BackupStandalone(f.s.store.Dir, backup, false)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.DataFormat != 2 || len(manifest.Files) < 4 {
		t.Fatalf("incomplete export %+v", manifest)
	}
	target := filepath.Join(t.TempDir(), "restored")
	target, err = canonicalServerPath(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = RestoreStandalone(backup, target); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStandalone(target)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err := restored.token(f.uploader); err == nil {
		t.Fatal("snapshot revived a usable project token")
	}
	state, err := restored.Maintenance()
	if err != nil || state.Mode != "quiesced" || state.Epoch <= manifest.Maintenance.Epoch {
		t.Fatalf("restore lost epoch %+v %v", state, err)
	}
	service, err := restored.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.Get("task")
	if err != nil || task.Definition.ID != "task" {
		t.Fatal("task missing", err)
	}
	if err = service.Store.Flush(); err != nil {
		t.Fatal("rebased projection failed", err)
	}
	var receipts int
	if err = service.Store.DB.QueryRow("SELECT count(*) FROM metadata WHERE key LIKE 'remote-operation:%'").Scan(&receipts); err != nil || receipts == 0 {
		t.Fatalf("operation history lost %d %v", receipts, err)
	}
	object := filepath.Join(target, "attachments", store.Hash([]byte("shared"))[:32], "objects", artifact["id"].(string))
	actual, err := os.ReadFile(object)
	if err != nil || store.Hash(actual) != store.Hash(data) {
		t.Fatalf("image bytes lost %v", err)
	}
	rows, err := service.Store.DB.Query("SELECT path FROM outbox")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		rows.Scan(&path)
		if !strings.HasPrefix(path, target+string(os.PathSeparator)) {
			t.Fatal("old server path survived", path)
		}
	}
}

func TestBackupTamperAndRestoreConflictPreserveTarget(t *testing.T) {
	f := newArtifactFixture(t)
	quiesceBackupFixture(t, f)
	backup := filepath.Join(t.TempDir(), "export")
	manifest, err := BackupStandalone(f.s.store.Dir, backup, false)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "untouched")
	file := manifest.Files[0]
	os.WriteFile(filepath.Join(backup, filepath.FromSlash(file.Path)), []byte("tampered"), 0600)
	if _, err := RestoreStandalone(backup, target); err == nil {
		t.Fatal("accepted altered backup")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("bad export changed restore target")
	}
}

func TestFormatMigrationResumesAndBlocksOrdinaryOpen(t *testing.T) {
	f := newArtifactFixture(t)
	dir := f.s.store.Dir
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, serverMarker), []byte("AEL-STANDALONE-1\n"), 0600)
	if _, err := OpenStandalone(dir); err == nil || !strings.Contains(err.Error(), "SERVER_MIGRATION_REQUIRED") {
		t.Fatalf("legacy format silently opened %v", err)
	}
	if _, err := MigrateStandalone(dir, false); err == nil {
		t.Fatal("missing stopped legacy server declaration accepted")
	}
	raw, _ := json.Marshal(map[string]int{"from": 1, "to": 2})
	os.WriteFile(filepath.Join(dir, "server-migration.json"), raw, 0600)
	os.WriteFile(filepath.Join(dir, serverMarker), []byte("AEL-STANDALONE-2\n"), 0600)
	if _, err := OpenStandalone(dir); err == nil {
		t.Fatal("partially migrated service started")
	}
	result, err := MigrateStandalone(dir, true)
	if err != nil || result["status"] != "migrated_quiesced" {
		t.Fatalf("migration resume %+v %v", result, err)
	}
	opened, err := OpenStandalone(dir)
	if err != nil {
		t.Fatal(err)
	}
	opened.Close()
	os.WriteFile(filepath.Join(dir, serverMarker), []byte("AEL-STANDALONE-3\n"), 0600)
	if _, err := OpenStandalone(dir); err == nil || !strings.Contains(err.Error(), "SERVER_SCHEMA_UNSUPPORTED") {
		t.Fatalf("future schema opened %v", err)
	}
}

func TestRestoreAllowsEmptyVolumeAtHistoricalDataPath(t *testing.T) {
	f := newArtifactFixture(t)
	quiesceBackupFixture(t, f)
	backup := filepath.Join(t.TempDir(), "export")
	if _, err := BackupStandalone(f.s.store.Dir, backup, false); err != nil {
		t.Fatal(err)
	}
	original := f.s.store.Dir
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(original, original+"-retired"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(original, 0700); err != nil {
		t.Fatal(err)
	}
	if result, err := RestoreStandalone(backup, original); err != nil || result["status"] != "restored_quiesced" {
		t.Fatalf("historical empty volume blocked %+v %v", result, err)
	}
}

func TestRestoreResumeRejectsUnsafeMatchingFile(t *testing.T) {
	for _, kind := range []string{"hardlink", "permissions"} {
		t.Run(kind, func(t *testing.T) {
			f := newArtifactFixture(t)
			quiesceBackupFixture(t, f)
			backup := filepath.Join(t.TempDir(), "export")
			manifest, err := BackupStandalone(f.s.store.Dir, backup, false)
			if err != nil {
				t.Fatal(err)
			}
			_, hash, err := VerifyServerBackup(backup)
			if err != nil {
				t.Fatal(err)
			}
			target, err := canonicalServerPath(filepath.Join(t.TempDir(), "target"))
			if err != nil {
				t.Fatal(err)
			}
			os.Mkdir(target, 0700)
			stage := filepath.Join(target, restoreStageName)
			os.Mkdir(stage, 0700)
			for _, file := range manifest.Files {
				if err := store.CopyAtomic(filepath.Join(backup, filepath.FromSlash(file.Path)), filepath.Join(stage, filepath.FromSlash(file.Path)), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := prepareRestoredData(stage, manifest.SourceRoot, target); err != nil {
				t.Fatal(err)
			}
			files, err := serverInventory(stage)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeRestorePlan(target, serverRestorePlan{Schema: 1, BackupHash: hash, Phase: "prepared", Files: files}); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(target, "runtime.db")
			if kind == "hardlink" {
				outside := filepath.Join(t.TempDir(), "linked.db")
				store.CopyAtomic(filepath.Join(stage, "runtime.db"), outside, 0600)
				if err := os.Link(outside, destination); err != nil {
					t.Fatal(err)
				}
			} else {
				store.CopyAtomic(filepath.Join(stage, "runtime.db"), destination, 0644)
			}
			if _, err := RestoreStandalone(backup, target); err == nil {
				t.Fatal("unsafe matching file reported restored")
			}
			if _, err := os.Stat(filepath.Join(target, restorePlanName)); err != nil {
				t.Fatal("recovery ownership record disappeared")
			}
		})
	}
}

func TestRestoreAcceptsOwnedEmptyDockerVolumeMode(t *testing.T) {
	f := newArtifactFixture(t)
	quiesceBackupFixture(t, f)
	backup := filepath.Join(t.TempDir(), "export")
	if _, err := BackupStandalone(f.s.store.Dir, backup, false); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "volume")
	os.Mkdir(target, 0755)
	os.Chmod(target, 0755)
	if _, err := RestoreStandalone(backup, target); err != nil {
		t.Fatal("empty Docker volume rejected", err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("restored root did not become private", err)
	}
	populated := filepath.Join(t.TempDir(), "user")
	os.Mkdir(populated, 0755)
	os.Chmod(populated, 0755)
	os.WriteFile(filepath.Join(populated, "user.txt"), []byte("user"), 0644)
	if _, err := RestoreStandalone(backup, populated); err == nil {
		t.Fatal("populated target accepted")
	}
	info, _ = os.Stat(populated)
	if info.Mode().Perm() != 0755 {
		t.Fatal("rejected target permissions were changed")
	}
}
