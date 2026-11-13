package bootstrap

import (
	"ael/internal/core"
	"ael/internal/projectmode"
	"ael/internal/store"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

type TransferPlan struct {
	Schema     int                   `json:"schema"`
	Root       string                `json:"root"`
	Data       string                `json:"data_dir"`
	Digest     string                `json:"digest"`
	Archive    core.AuthorityArchive `json:"archive"`
	Active     int                   `json:"unresolved_responsibilities"`
	Locks      int                   `json:"workspace_locks"`
	Pending    int                   `json:"pending_projections"`
	Workspaces map[string]string     `json:"source_workspaces"`
}

type archiveReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// ScanTransfer opens only the selected existing Local runtime in read-only
// mode. It does not recover Git records, flush projections, or create a DB.
func ScanTransfer(root, source string) (TransferPlan, error) {
	var plan TransferPlan
	if !core.ValidArchiveSourceID(source) {
		return plan, core.Error("TRANSFER_SOURCE_REQUIRED", "provide a stable source instance ID")
	}
	root, err := Canonical(root)
	if err != nil {
		return plan, err
	}
	data := DataDir(root)
	if err = ValidateDataLocation(root, data); err != nil {
		return plan, err
	}
	path := filepath.Join(data, "runtime.db")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return plan, core.Error("LOCAL_RUNTIME_REQUIRED", "select an existing Local runtime explicitly")
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path}).String()+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return plan, err
	}
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return plan, err
	}
	defer tx.Rollback()
	plan, err = scanTransfer(root, data, source, tx)
	if err != nil {
		return plan, err
	}
	return plan, tx.Commit()
}

func scanTransfer(root, data, source string, reader archiveReader) (TransferPlan, error) {
	plan := TransferPlan{Schema: 1, Root: root, Data: data, Workspaces: map[string]string{}}
	config, err := Read(root)
	if err != nil {
		return plan, err
	}
	plan.Archive = core.AuthorityArchive{Schema: 1, Project: config.ProjectID, Source: source, Version: config.Version, Tasks: []core.ArchivedTask{}, Files: []core.ArchivedFile{}}
	rows, err := reader.QueryContext(context.Background(), "SELECT body FROM tasks ORDER BY id")
	if err != nil {
		return plan, err
	}
	for rows.Next() {
		var raw string
		var task core.Task
		if err = rows.Scan(&raw); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(raw), &task); err != nil {
			break
		}
		if err = core.Validate(task.Definition); err != nil {
			break
		}
		for _, attempt := range task.Attempts {
			if attempt.Status == "running" || attempt.Status == "interrupted" {
				plan.Active++
			}
		}
		identity, _ := json.Marshal([]string{config.ProjectID, source, task.Definition.ID})
		mapped := "import-" + store.Hash(identity)[:32]
		archived := core.ArchiveTask(task, mapped)
		plan.Archive.Tasks = append(plan.Archive.Tasks, archived)
		workspace := task.CandidateWorkspace
		if workspace == "" {
			workspace = root
		}
		plan.Workspaces[task.Definition.ID] = workspace
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return plan, err
	}
	for _, query := range []struct {
		SQL string
		Out *int
	}{{"SELECT count(*) FROM workspace_locks", &plan.Locks}, {"SELECT count(*) FROM outbox WHERE state IN ('pending','delete_pending')", &plan.Pending}} {
		if err = reader.QueryRowContext(context.Background(), query.SQL).Scan(query.Out); err != nil {
			return plan, err
		}
	}
	service := &core.Service{Root: root, Store: &store.Store{Dir: data}}
	for _, task := range plan.Archive.Tasks {
		workspace := plan.Workspaces[task.OriginalID]
		if workspace != root {
			var registered string
			if err := reader.QueryRowContext(context.Background(), "SELECT body FROM metadata WHERE key=?", "workspace:"+workspace).Scan(&registered); err != nil {
				return plan, core.Error("TRANSFER_WORKSPACE_UNBOUND", "task artifact workspace has no explicit binding")
			}
		}
		service.Root = workspace
		files, err := archiveFiles(service, task)
		if err != nil {
			return plan, err
		}
		plan.Archive.Files = append(plan.Archive.Files, files...)
	}
	body, err := json.Marshal(plan)
	if err != nil {
		return plan, err
	}
	plan.Digest = store.Hash(body)
	return plan, nil
}

func archiveFiles(service *core.Service, task core.ArchivedTask) ([]core.ArchivedFile, error) {
	refs := map[string]string{}
	expected := map[string][]string{}
	addExpected := func(ref, hash string) {
		if hash != "" && !slices.Contains(expected[ref], hash) {
			expected[ref] = append(expected[ref], hash)
		}
	}
	for _, evidence := range append(append([]core.Evidence{}, task.Snapshot.EvidenceHistory...), task.Snapshot.Evidence...) {
		for ref, hash := range evidence.ArtifactHashes {
			refs[ref] = "evidence"
			addExpected(ref, hash)
		}
		for _, ref := range evidence.Inputs {
			refs[ref] = "evidence_input"
		}
		for _, ref := range evidence.Artifacts {
			refs[ref] = "evidence"
		}
		for _, ref := range evidence.DiagnosticArtifacts {
			refs[ref] = "evidence"
		}
	}
	for _, attempt := range task.Snapshot.Attempts {
		for _, ref := range attempt.Outputs {
			refs[ref] = "output"
		}
	}
	for _, note := range task.Snapshot.HumanReviewNotes {
		for ref, hash := range note.ArtifactHashes {
			refs[ref] = "review"
			addExpected(ref, hash)
		}
		for _, ref := range note.Artifacts {
			refs[ref] = "review"
		}
	}
	if task.Snapshot.History != nil {
		for _, check := range task.Snapshot.History.Checks {
			for ref, hash := range check.ArtifactHashes {
				refs[ref] = "historical_evidence"
				addExpected(ref, hash)
			}
		}
	}
	for _, ref := range task.Snapshot.UnboundArtifacts {
		refs[ref] = "unbound_output"
	}
	if task.Snapshot.KnowledgeOutcome != nil {
		for _, ref := range task.Snapshot.KnowledgeOutcome.Artifacts {
			refs[ref] = "knowledge_outcome"
		}
	}
	if task.Snapshot.Delivery != nil && task.Snapshot.Delivery.Artifact != "" {
		refs[task.Snapshot.Delivery.Artifact] = "delivery_receipt"
	}
	for _, basis := range task.Snapshot.Basis {
		if len(basis.Version) != 64 {
			continue
		}
		ref := "runtime:knowledge-versions/" + store.Hash([]byte(basis.ID)) + "/" + basis.Version + ".json"
		refs[ref] = "knowledge_snapshot"
	}
	keys := make([]string, 0, len(refs))
	for ref := range refs {
		keys = append(keys, ref)
	}
	sort.Strings(keys)
	files := []core.ArchivedFile{}
	for _, ref := range keys {
		sort.Strings(expected[ref])
		file := core.ArchivedFile{TaskID: task.OriginalID, Ref: ref, Kind: refs[ref], Status: "missing", ExpectedHashes: expected[ref], HashMatch: "unrecorded"}
		bytes, err := service.Artifact(ref)
		if err == nil {
			file.Status, file.SHA256, file.Size = "available", store.Hash(bytes), int64(len(bytes))
			if len(file.ExpectedHashes) > 0 {
				file.HashMatch = "differs"
				if slices.Contains(file.ExpectedHashes, file.SHA256) {
					file.HashMatch = "matches_recorded_version"
				}
			}
		} else if strings.HasPrefix(err.Error(), "UNSAFE_") || strings.Contains(err.Error(), "SYMLINK") {
			file.Status = "unavailable_unsafe_path"
		}
		files = append(files, file)
	}
	return files, nil
}

// ExportTransfer freezes legacy-compatible SQL writes before taking the local
// recovery backup. It deliberately requires explicit host coordination.
func ExportTransfer(plan TransferPlan, out string, acknowledged bool) (map[string]any, error) {
	if !acknowledged {
		return nil, core.Error("TRANSFER_DRAIN_REQUIRED", "coordinate external executors and provide --ack-drained")
	}
	approved := plan.Digest
	plan.Digest = ""
	body, err := json.Marshal(plan)
	plan.Digest = approved
	if err != nil || store.Hash(body) != approved {
		return nil, core.Error("TRANSFER_PLAN_INVALID", "plan content differs from its approved digest")
	}
	if err = core.ValidateArchiveTransferLimits(plan.Archive); err != nil {
		return nil, err
	}
	root, err := Canonical(plan.Root)
	if err != nil || root != plan.Root || DataDir(root) != plan.Data || plan.Schema != 1 {
		return nil, core.Error("TRANSFER_PLAN_INVALID", "source runtime differs from the plan")
	}
	out, err = filepath.Abs(out)
	if err != nil {
		return nil, err
	}
	out, err = canonicalTransferDestination(out)
	data, dataErr := Canonical(plan.Data)
	if err != nil || dataErr != nil || out == root || strings.HasPrefix(out, root+string(os.PathSeparator)) || out == data || strings.HasPrefix(out, data+string(os.PathSeparator)) {
		return nil, core.Error("TRANSFER_OUTSIDE_REQUIRED", "choose a private export directory outside source data and checkout")
	}
	unlock, err := projectmode.Lock(root, true)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err = RefuseRemote(root); err != nil {
		return nil, err
	}
	if _, err = os.Stat(filepath.Join(plan.Data, "runtime.db")); err != nil {
		return nil, err
	}
	db, err := store.Open(plan.Data)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	err = db.Write(func(tx *store.Tx) error {
		if _, err := tx.Exec("CREATE TABLE IF NOT EXISTS authority_transfer(id INTEGER PRIMARY KEY CHECK(id=1),digest TEXT NOT NULL,source TEXT NOT NULL,export_dir TEXT NOT NULL)"); err != nil {
			return err
		}
		var digest, destination string
		err := tx.Row("SELECT digest,export_dir FROM authority_transfer WHERE id=1").Scan(&digest, &destination)
		if err == nil {
			if digest != plan.Digest || destination != out {
				return core.Error("TRANSFER_FROZEN", "resume the original frozen export")
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		current, err := scanTransfer(root, plan.Data, plan.Archive.Source, tx.Conn)
		if err != nil {
			return err
		}
		if current.Digest != plan.Digest {
			return core.Error("TRANSFER_PLAN_STALE", "rescan changed state before export")
		}
		if current.Active > 0 || current.Locks > 0 || current.Pending > 0 {
			return core.Error("TRANSFER_NOT_DRAINED", "resolve running/interrupted responsibilities, workspace locks and pending projections")
		}
		if _, err = os.Lstat(out); !os.IsNotExist(err) {
			return core.Error("TRANSFER_TARGET_EXISTS", "export requires a new directory")
		}
		for _, table := range []string{"tasks", "events", "submissions", "workspace_locks", "outbox", "metadata"} {
			for _, action := range []string{"INSERT", "UPDATE", "DELETE"} {
				statement := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS ael_authority_%s_%s BEFORE %s ON %s WHEN EXISTS(SELECT 1 FROM authority_transfer) BEGIN SELECT RAISE(ABORT,'LOCAL_AUTHORITY_FROZEN'); END`, table, action, action, table)
				if _, err = tx.Exec(statement); err != nil {
					return err
				}
			}
		}
		_, err = tx.Exec("INSERT INTO authority_transfer(id,digest,source,export_dir) VALUES(1,?,?,?)", plan.Digest, plan.Archive.Source, out)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(out, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(out)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, core.Error("TRANSFER_DIRECTORY_UNSAFE", "export directory must remain private")
	}
	backup := filepath.Join(out, "local-recovery.db")
	stagedBackup := filepath.Join(out, ".backup-"+core.ID(""))
	if err = db.Backup(stagedBackup); err != nil {
		return nil, err
	}
	defer os.Remove(stagedBackup)
	if err = store.CopyAtomic(stagedBackup, backup, 0600); err != nil {
		return nil, err
	}
	service := &core.Service{Root: root, Store: db}
	for _, file := range plan.Archive.Files {
		if file.Status != "available" {
			continue
		}
		object, err := store.SafePath(out, filepath.Join("objects", file.SHA256))
		if err != nil {
			return nil, err
		}
		if info, err := os.Lstat(object); err == nil {
			hash, hashErr := store.HashFile(object)
			if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != file.Size || hashErr != nil || hash != file.SHA256 {
				return nil, core.Error("TRANSFER_OBJECT_CONFLICT", "saved object differs from the frozen plan")
			}
			continue
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		service.Root = plan.Workspaces[file.TaskID]
		body, err := service.Artifact(file.Ref)
		if err != nil || store.Hash(body) != file.SHA256 || int64(len(body)) != file.Size {
			return nil, core.Error("TRANSFER_FILE_CHANGED", "source artifact differs from frozen plan")
		}
		if err = store.Atomic(object, body, 0600); err != nil {
			return nil, err
		}
	}
	body, err = json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = store.Atomic(filepath.Join(out, "plan.json"), append(body, '\n'), 0600); err != nil {
		return nil, err
	}
	backupHash, err := store.HashFile(backup)
	if err != nil {
		return nil, err
	}
	receipt := map[string]any{"schema": 1, "status": "frozen_exported", "digest": plan.Digest, "source": plan.Archive.Source, "tasks": len(plan.Archive.Tasks), "files": len(plan.Archive.Files), "backup_sha256": backupHash, "authority": "local_frozen", "host_execution_state": "externally_acknowledged"}
	body, _ = json.MarshalIndent(receipt, "", "  ")
	if err = store.Atomic(filepath.Join(out, "export-receipt.json"), append(body, '\n'), 0600); err != nil {
		return nil, err
	}
	return receipt, nil
}

func canonicalTransferDestination(path string) (string, error) {
	probe := filepath.Clean(path)
	suffix := []string{}
	for {
		if _, err := os.Lstat(probe); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", core.Error("TRANSFER_DIRECTORY_UNSAFE", "cannot resolve export location")
		}
		suffix = append(suffix, filepath.Base(probe))
		probe = parent
	}
	resolved, err := filepath.EvalSymlinks(probe)
	if err != nil {
		return "", err
	}
	for i := len(suffix) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, suffix[i])
	}
	return resolved, nil
}

func refuseFrozenAuthority(db *store.Store) error {
	var exists int
	if err := db.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='authority_transfer'").Scan(&exists); err != nil || exists == 0 {
		return err
	}
	if err := db.DB.QueryRow("SELECT count(*) FROM authority_transfer").Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return core.Error("LOCAL_AUTHORITY_FROZEN", "continue the explicit transfer or coordinated rollback")
	}
	return nil
}

func VerifyTransferExport(from string) (TransferPlan, error) {
	var plan TransferPlan
	path, err := store.SafePath(from, "plan.json")
	if err != nil {
		return plan, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return plan, err
	}
	if err = json.Unmarshal(raw, &plan); err != nil {
		return plan, err
	}
	digest := plan.Digest
	plan.Digest = ""
	body, err := json.Marshal(plan)
	plan.Digest = digest
	if err != nil || len(digest) != 64 || store.Hash(body) != digest || plan.Schema != 1 {
		return plan, core.Error("TRANSFER_PLAN_INVALID", "export plan digest differs")
	}
	path, err = store.SafePath(from, "export-receipt.json")
	if err != nil {
		return plan, err
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		return plan, err
	}
	var receipt struct {
		Schema       int    `json:"schema"`
		Status       string `json:"status"`
		Digest       string `json:"digest"`
		BackupSHA256 string `json:"backup_sha256"`
	}
	if err = json.Unmarshal(raw, &receipt); err != nil || receipt.Schema != 1 || receipt.Status != "frozen_exported" || receipt.Digest != digest {
		return plan, core.Error("TRANSFER_EXPORT_INCOMPLETE", "a completed frozen export receipt is required")
	}
	path, err = store.SafePath(from, "local-recovery.db")
	if err != nil {
		return plan, err
	}
	hash, err := store.HashFile(path)
	if err != nil || hash != receipt.BackupSHA256 {
		return plan, core.Error("TRANSFER_BACKUP_INVALID", "local recovery backup differs")
	}
	for _, file := range plan.Archive.Files {
		if file.Status != "available" {
			continue
		}
		if _, err = hex.DecodeString(file.SHA256); err != nil || len(file.SHA256) != 64 {
			return plan, core.Error("TRANSFER_PLAN_INVALID", "invalid object digest")
		}
		path, err = store.SafePath(from, filepath.Join("objects", file.SHA256))
		if err != nil {
			return plan, err
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() != file.Size {
			return plan, core.Error("TRANSFER_OBJECT_CONFLICT", "exported object is absent or differs")
		}
		hash, err := store.HashFile(path)
		if err != nil || hash != file.SHA256 {
			return plan, core.Error("TRANSFER_OBJECT_CONFLICT", "exported object bytes differ")
		}
	}
	return plan, nil
}

func VerifyFrozenTransfer(plan TransferPlan, root string) error {
	root, err := Canonical(root)
	if err != nil || root != plan.Root || DataDir(root) != plan.Data {
		return core.Error("TRANSFER_SOURCE_MISMATCH", "use the original frozen checkout and runtime")
	}
	config, err := Read(root)
	if err != nil || config.ProjectID != plan.Archive.Project {
		return core.Error("TRANSFER_SOURCE_MISMATCH", "source project identity changed")
	}
	path := filepath.Join(plan.Data, "runtime.db")
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path}).String()+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	var digest, source string
	if db.QueryRow("SELECT digest,source FROM authority_transfer WHERE id=1").Scan(&digest, &source) != nil || digest != plan.Digest || source != plan.Archive.Source {
		return core.Error("TRANSFER_FREEZE_REQUIRED", "source freeze differs from the completed export")
	}
	return nil
}

// ReleaseFrozenTransfer changes only the authority fence. The original runtime
// data stays in place, and a journal permits retry after binding removal fails.
func ReleaseFrozenTransfer(plan TransferPlan, root, bindingHash string) error {
	root, err := Canonical(root)
	if err != nil || root != plan.Root || DataDir(root) != plan.Data || len(bindingHash) != 64 {
		return core.Error("TRANSFER_SOURCE_MISMATCH", "rollback must use the original source and binding")
	}
	config, err := Read(root)
	if err != nil || config.ProjectID != plan.Archive.Project {
		return core.Error("TRANSFER_SOURCE_MISMATCH", "source project identity changed")
	}
	if _, err = os.Stat(filepath.Join(plan.Data, "runtime.db")); err != nil {
		return err
	}
	db, err := store.Open(plan.Data)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Write(func(tx *store.Tx) error {
		var frozen string
		err := tx.Row("SELECT digest FROM authority_transfer WHERE id=1").Scan(&frozen)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil && frozen != plan.Digest {
			return core.Error("TRANSFER_SOURCE_MISMATCH", "another source generation is frozen")
		}
		if _, err = tx.Exec("CREATE TABLE IF NOT EXISTS authority_transfer_releases(digest TEXT NOT NULL,binding_hash TEXT NOT NULL,PRIMARY KEY(digest,binding_hash))"); err != nil {
			return err
		}
		var prior string
		err = tx.Row("SELECT binding_hash FROM authority_transfer_releases WHERE digest=? AND binding_hash=?", plan.Digest, bindingHash).Scan(&prior)
		if err == nil {
			if frozen == plan.Digest {
				_, err = tx.Exec("DELETE FROM authority_transfer WHERE id=1 AND digest=?", plan.Digest)
				return err
			}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		if frozen != plan.Digest {
			return core.Error("TRANSFER_FREEZE_REQUIRED", "source freeze is absent")
		}
		if _, err = tx.Exec("INSERT INTO authority_transfer_releases(digest,binding_hash) VALUES(?,?)", plan.Digest, bindingHash); err != nil {
			return err
		}
		_, err = tx.Exec("DELETE FROM authority_transfer WHERE id=1 AND digest=?", plan.Digest)
		return err
	})
}
