package bootstrap

import (
	"ael/internal/core"
	"ael/internal/store"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func transferFixture(t *testing.T) (string, *store.Store) {
	t.Helper()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	root, err := Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(root, ".ael"), 0700); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(Config{Schema: 1, ProjectID: "old-project", Mode: "active", Version: "0.1.19"})
	if err = os.WriteFile(filepath.Join(root, ".ael/project.yaml"), config, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(DataDir(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	task := core.Task{Definition: core.Definition{ID: "same-name", Title: "Old result", Type: "feature", Goal: "verified", Revision: 2, Acceptance: []string{"actual result"}}, Plan: core.Plan{Revision: 2, Approved: true}, Status: "completed", Step: "done", Historical: true}
	task.Attempts = []core.Attempt{{ID: "old-attempt", Status: "passed", DefinitionRevision: 2, PlanRevision: 2, Token: "PRIVATE_OLD_LEASE", Session: "PRIVATE_MODEL_SESSION", PID: 122, OwnerPID: 133, OwnerIdentity: "old-owner", Workspace: "/private/work", Lease: time.Now().Add(time.Hour), Fence: 7, Outputs: []string{"runtime:result.png", "runtime:missing.png"}}}
	task.Evidence = []core.Evidence{{Check: "picture", Result: "pass", Candidate: "old-candidate", Revision: 2, Artifacts: []string{"runtime:result.png"}}}
	raw, _ := json.Marshal(task)
	if _, err = db.DB.Exec("INSERT INTO tasks(id,body,status,step,updated) VALUES(?,?,?,?,?)", task.Definition.ID, string(raw), task.Status, task.Step, ""); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(db.Dir, "result.png"), []byte("synthetic picture bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	return root, db
}

func TestAuthorityTransferScanMapsSourcesAndPreservesHistoricalFacts(t *testing.T) {
	root, db := transferFixture(t)
	before, _ := store.HashFile(filepath.Join(db.Dir, "runtime.db"))
	a, err := ScanTransfer(root, "mac-old")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ScanTransfer(root, "cloud-old")
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := ScanTransfer(root, "mac-old")
	if err != nil || repeat.Digest != a.Digest || a.Archive.Tasks[0].MappedID == b.Archive.Tasks[0].MappedID {
		t.Fatalf("unstable or colliding source mapping %v", err)
	}
	after, _ := store.HashFile(filepath.Join(db.Dir, "runtime.db"))
	if before != after {
		t.Fatal("read-only scan changed runtime")
	}
	raw, _ := json.Marshal(a)
	if strings.Contains(string(raw), "PRIVATE_") || strings.Contains(string(raw), "old-owner") || strings.Contains(string(raw), "/private/work") {
		t.Fatal("archive retained execution credentials")
	}
	snapshot := a.Archive.Tasks[0].Snapshot
	if snapshot.Plan.Approved || !snapshot.Attempts[0].Lease.IsZero() || snapshot.Attempts[0].Fence != 0 || snapshot.Attempts[0].DefinitionRevision != 2 || snapshot.Evidence[0].Candidate != "old-candidate" || !snapshot.Created.IsZero() {
		t.Fatal("history changed or authority retained")
	}
	if len(a.Archive.Files) != 2 || a.Archive.Files[0].Status != "missing" || a.Archive.Files[1].Status != "available" {
		t.Fatalf("missing artifact not reconciled %+v", a.Archive.Files)
	}
}

func TestAuthorityTransferExportFreezesLegacyWritersAndResumes(t *testing.T) {
	root, db := transferFixture(t)
	plan, err := ScanTransfer(root, "mac-old")
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "export")
	if _, err = ExportTransfer(plan, out, false); err == nil {
		t.Fatal("uncoordinated source exported")
	}
	if _, err = ExportTransfer(plan, out, true); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{"UPDATE tasks SET updated='changed'", "INSERT INTO events(task_id,kind,body,created) VALUES('','old','{}','')", "INSERT INTO metadata VALUES('old-writer','{}')", "INSERT INTO outbox VALUES('old','','','pending')"} {
		if _, err = db.DB.Exec(statement); err == nil || !strings.Contains(err.Error(), "LOCAL_AUTHORITY_FROZEN") {
			t.Fatalf("old writer escaped freeze %s %v", statement, err)
		}
	}
	if _, _, err = Open(root); err == nil || !strings.Contains(err.Error(), "LOCAL_AUTHORITY_FROZEN") {
		t.Fatalf("normal Local entry reopened frozen runtime %v", err)
	}
	if _, err = ExportTransfer(plan, out, true); err != nil {
		t.Fatalf("exact frozen export did not resume %v", err)
	}
	if _, err = ExportTransfer(plan, filepath.Join(t.TempDir(), "other"), true); err == nil {
		t.Fatal("frozen destination changed")
	}
	var receipt map[string]any
	raw, err := os.ReadFile(filepath.Join(out, "export-receipt.json"))
	if err != nil || json.Unmarshal(raw, &receipt) != nil || receipt["digest"] != plan.Digest {
		t.Fatal("export receipt missing")
	}
	hash, err := store.HashFile(filepath.Join(out, "local-recovery.db"))
	if err != nil || hash != receipt["backup_sha256"] {
		t.Fatal("local backup not reconciled")
	}
}

func TestAuthorityTransferRejectsActiveStaleAndTamperedPlans(t *testing.T) {
	root, db := transferFixture(t)
	plan, _ := ScanTransfer(root, "mac-old")
	plan.Archive.Tasks[0].Snapshot.Result = "tampered"
	if _, err := ExportTransfer(plan, filepath.Join(t.TempDir(), "export"), true); err == nil || !strings.Contains(err.Error(), "TRANSFER_PLAN_INVALID") {
		t.Fatalf("tampered plan accepted %v", err)
	}
	plan, _ = ScanTransfer(root, "mac-old")
	if _, err := db.DB.Exec("UPDATE tasks SET body=json_set(body,'$.attempts[0].status','interrupted')"); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportTransfer(plan, filepath.Join(t.TempDir(), "export"), true); err == nil || !strings.Contains(err.Error(), "TRANSFER_PLAN_STALE") {
		t.Fatalf("stale plan accepted %v", err)
	}
	plan, _ = ScanTransfer(root, "mac-old")
	if _, err := ExportTransfer(plan, filepath.Join(t.TempDir(), "export"), true); err == nil || !strings.Contains(err.Error(), "TRANSFER_NOT_DRAINED") {
		t.Fatalf("unknown interrupted responsibility accepted %v", err)
	}
}

func TestAuthorityTransferUsesBoundWorkspaceAndAllEvidenceReferences(t *testing.T) {
	root, db := transferFixture(t)
	workspace, _ := Canonical(t.TempDir())
	if err := os.WriteFile(filepath.Join(root, "actual.png"), []byte("wrong checkout picture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "actual.png"), []byte("correct bound picture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(db.Dir, "receipt.json"), []byte(`{"status":"success"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec("INSERT INTO metadata VALUES(?,?)", "workspace:"+workspace, `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec("UPDATE tasks SET body=json_set(body,'$.candidate_workspace',?,'$.evidence[0].artifacts',json('[\"actual.png\"]'),'$.evidence[0].inputs',json('[\"runtime:result.png\"]'),'$.delivery',json('{\"artifact\":\"runtime:receipt.json\"}'))", workspace); err != nil {
		t.Fatal(err)
	}
	plan, err := ScanTransfer(root, "mac-old")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]core.ArchivedFile{}
	for _, file := range plan.Archive.Files {
		files[file.Ref] = file
	}
	if files["actual.png"].SHA256 != store.Hash([]byte("correct bound picture")) || files["runtime:receipt.json"].Status != "available" || files["runtime:result.png"].Status != "available" {
		t.Fatalf("wrong workspace or omitted reference %+v", files)
	}
}

func TestAuthorityTransferResumesFromSavedObjectAfterSourceDisappears(t *testing.T) {
	root, db := transferFixture(t)
	plan, _ := ScanTransfer(root, "mac-old")
	out := filepath.Join(t.TempDir(), "export")
	if _, err := ExportTransfer(plan, out, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(out, "export-receipt.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(db.Dir, "result.png")); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportTransfer(plan, out, true); err != nil {
		t.Fatalf("saved verified object could not recover export %v", err)
	}
}

func TestAuthorityTransferReleaseJournalPreservesNewLocalFacts(t *testing.T) {
	root, db := transferFixture(t)
	plan, err := ScanTransfer(root, "mac-old")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ExportTransfer(plan, filepath.Join(t.TempDir(), "export"), true); err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 64)
	if err = ReleaseFrozenTransfer(plan, root, hash); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec("UPDATE tasks SET body=json_set(body,'$.result','new Local fact') WHERE id='same-name'"); err != nil {
		t.Fatal(err)
	}
	if err = ReleaseFrozenTransfer(plan, root, hash); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err = db.DB.QueryRow("SELECT body FROM tasks WHERE id='same-name'").Scan(&raw); err != nil || !strings.Contains(raw, "new Local fact") {
		t.Fatal("rollback retry overwrote new Local facts")
	}
}

func TestAuthorityTransferRejectsDeterministicLimitsBeforeFreezing(t *testing.T) {
	for _, kind := range []string{"file", "archive"} {
		t.Run(kind, func(t *testing.T) {
			root, db := transferFixture(t)
			if kind == "file" {
				if err := os.WriteFile(filepath.Join(db.Dir, "large.bin"), bytes.Repeat([]byte("x"), 11<<20), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := db.DB.Exec("UPDATE tasks SET body=json_insert(body,'$.attempts[0].outputs[#]','runtime:large.bin')"); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := db.DB.Exec("UPDATE tasks SET body=json_set(body,'$.result',?)", strings.Repeat("x", 2<<20)); err != nil {
					t.Fatal(err)
				}
			}
			plan, err := ScanTransfer(root, "mac-old")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = ExportTransfer(plan, filepath.Join(t.TempDir(), "export"), true); err == nil {
				t.Fatal("untransferable archive froze the source")
			}
			if err = refuseFrozenAuthority(db); err != nil {
				t.Fatalf("deterministic rejection left a frozen source %v", err)
			}
		})
	}
}
