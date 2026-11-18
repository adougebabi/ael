package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
)

type authorityImportInput struct {
	Archive        core.AuthorityArchive `json:"archive"`
	Digest         string                `json:"digest"`
	OperationID    string                `json:"operation_id"`
	IdempotencyKey string                `json:"idempotency_key"`
}

type authorityImportBinding struct {
	TaskID     string `json:"task_id"`
	Ref        string `json:"ref"`
	ArtifactID string `json:"artifact_id"`
}

type authorityImportControl struct {
	AckDrained     bool                     `json:"ack_drained,omitempty"`
	Digest         string                   `json:"digest"`
	OperationID    string                   `json:"operation_id"`
	IdempotencyKey string                   `json:"idempotency_key"`
	Bindings       []authorityImportBinding `json:"bindings,omitempty"`
}

type importedFile struct {
	CurrentStatus string `json:"current_status,omitempty"`
	TaskID        string `json:"task_id"`
	Ref           string `json:"ref"`
	SHA256        string `json:"sha256"`
	Size          int64  `json:"size"`
	Status        string `json:"status"`
	ArtifactID    string `json:"artifact_id"`
}

func importedFiles(reader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, id string) ([]importedFile, error) {
	rows, err := reader.QueryContext(context.Background(), "SELECT task_id,ref,sha256,size,status,artifact_id FROM authority_import_files WHERE import_id=? ORDER BY task_id,ref", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := []importedFile{}
	for rows.Next() {
		var file importedFile
		if err = rows.Scan(&file.TaskID, &file.Ref, &file.SHA256, &file.Size, &file.Status, &file.ArtifactID); err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, rows.Err()
}

func reconcileImportedFiles(tx *store.Tx, artifacts *artifactStore, id string, bindings []authorityImportBinding) (map[string]int, error) {
	files, err := importedFiles(tx.Conn, id)
	if err != nil {
		return nil, err
	}
	selected := map[string]string{}
	for _, binding := range bindings {
		key := binding.TaskID + "\x00" + binding.Ref
		if _, exists := selected[key]; exists {
			return nil, errors.New("IMPORT_BINDING_INVALID")
		}
		selected[key] = binding.ArtifactID
	}
	counts := map[string]int{"available": 0, "missing": 0}
	for _, file := range files {
		key := file.TaskID + "\x00" + file.Ref
		artifact, provided := selected[key]
		delete(selected, key)
		if file.Status != "available" {
			if provided {
				return nil, errors.New("IMPORT_BINDING_INVALID")
			}
			counts["missing"]++
			continue
		}
		if file.Size == 0 && file.SHA256 == store.Hash(nil) {
			if provided && artifact != "" {
				return nil, errors.New("IMPORT_BINDING_INVALID")
			}
			counts["available"]++
			continue
		}
		if !provided {
			artifact = file.ArtifactID
		}
		object, err := scanArtifact(tx.Row("SELECT "+artifactColumns+" FROM artifact_objects WHERE id=?", artifact))
		if err != nil || object.Status != "finalized" || object.TaskID != file.TaskID || object.Source != "authority-import:"+id || object.SHA256 != file.SHA256 || object.Size != file.Size {
			return nil, errors.New("IMPORT_FILE_UNAVAILABLE")
		}
		path, err := artifacts.contentPath(tx.Row, artifact)
		if err != nil {
			return nil, err
		}
		if _, err = artifactFileBytes(path, object.Size, object.SHA256, object.MIME); err != nil {
			return nil, errors.New("IMPORT_FILE_UNAVAILABLE")
		}
		if _, err = tx.Exec("UPDATE authority_import_files SET artifact_id=? WHERE import_id=? AND task_id=? AND ref=?", artifact, id, file.TaskID, file.Ref); err != nil {
			return nil, err
		}
		counts["available"]++
	}
	if len(selected) > 0 {
		return nil, errors.New("IMPORT_BINDING_INVALID")
	}
	return counts, nil
}

func (p *portableStore) controlAuthorityImport(principal standalonePrincipal, id, action string, input authorityImportControl, artifacts *artifactStore) (map[string]any, error) {
	return p.mutate(principal, "authority-import-"+id+"-"+action, input.OperationID, input.IdempotencyKey, input, func(tx *store.Tx) (map[string]any, error) {
		var digest, state, actor string
		err := tx.Row("SELECT digest,state,actor FROM authority_imports WHERE id=?", id).Scan(&digest, &state, &actor)
		if err == sql.ErrNoRows && action == "rollback-freeze" && input.AckDrained && len(input.Bindings) == 0 {
			if !artifactDigest.MatchString(input.Digest) || id != "import-"+input.Digest[:32] {
				return nil, errors.New("IMPORT_DIGEST_CONFLICT")
			}
			var previousActor string
			err = tx.Row("SELECT actor FROM authority_import_aborts WHERE id=?", id).Scan(&previousActor)
			if err == nil && previousActor != principal.UserID && !principal.Admin {
				return nil, errors.New("IMPORT_NOT_FOUND")
			}
			if err != nil && err != sql.ErrNoRows {
				return nil, err
			}
			if _, err = tx.Exec("INSERT INTO authority_import_aborts(id,digest,actor) VALUES(?,?,?) ON CONFLICT(id) DO NOTHING", id, input.Digest, principal.UserID); err != nil {
				return nil, err
			}
			return map[string]any{"id": id, "digest": input.Digest, "state": "rolled_back", "gate_effect": "none"}, nil
		}
		if err == sql.ErrNoRows && action == "rollback-complete" && input.AckDrained {
			var previousDigest, previousActor string
			if tx.Row("SELECT digest,actor FROM authority_import_aborts WHERE id=?", id).Scan(&previousDigest, &previousActor) == nil && previousDigest == input.Digest && (previousActor == principal.UserID || principal.Admin) {
				return map[string]any{"id": id, "digest": input.Digest, "state": "rolled_back", "gate_effect": "none"}, nil
			}
		}
		if err != nil {
			return nil, errors.New("IMPORT_NOT_FOUND")
		}
		if actor != principal.UserID && !principal.Admin {
			return nil, errors.New("IMPORT_NOT_FOUND")
		}
		if digest != input.Digest {
			return nil, errors.New("IMPORT_DIGEST_CONFLICT")
		}
		if action == "rollback-freeze" || action == "rollback-complete" {
			if !input.AckDrained || len(input.Bindings) != 0 {
				return nil, errors.New("IMPORT_DRAIN_REQUIRED")
			}
			next := "rolled_back"
			if action == "rollback-freeze" {
				if state != "active" && state != "importing" && state != "verified" {
					return nil, errors.New("IMPORT_STATE_CONFLICT")
				}
				next = "rollback_frozen"
			} else if state != "rollback_frozen" {
				return nil, errors.New("IMPORT_STATE_CONFLICT")
			}
			var baseline string
			includeArtifacts := true
			err := tx.Row("SELECT fingerprint FROM authority_import_baselines WHERE import_id=?", id).Scan(&baseline)
			if err == sql.ErrNoRows {
				includeArtifacts = false
				err = tx.Row("SELECT fingerprint FROM authority_import_stage_baselines WHERE import_id=?", id).Scan(&baseline)
			}
			if err != nil {
				return nil, errors.New("IMPORT_BASELINE_UNAVAILABLE")
			}
			current, err := authorityImportFingerprint(tx, id, includeArtifacts)
			if err != nil {
				return nil, err
			}
			if current != baseline {
				return nil, errors.New("IMPORT_NEW_FACTS")
			}
			if _, err := tx.Exec("UPDATE authority_imports SET state=? WHERE id=?", next, id); err != nil {
				return nil, err
			}
			return map[string]any{"id": id, "digest": digest, "state": next, "gate_effect": "none"}, nil
		}
		if action == "verify" && state != "importing" && state != "verified" {
			return nil, errors.New("IMPORT_STATE_CONFLICT")
		}
		if action == "commit" && state != "verified" {
			return nil, errors.New("IMPORT_STATE_CONFLICT")
		}
		counts, err := reconcileImportedFiles(tx, artifacts, id, input.Bindings)
		if err != nil {
			return nil, err
		}
		next := "verified"
		if action == "commit" {
			next = "active"
			fingerprint, err := authorityImportFingerprint(tx, id, true)
			if err != nil {
				return nil, err
			}
			if _, err = tx.Exec("INSERT INTO authority_import_baselines(import_id,fingerprint) VALUES(?,?)", id, fingerprint); err != nil {
				return nil, err
			}
		}
		if _, err = tx.Exec("UPDATE authority_imports SET state=? WHERE id=?", next, id); err != nil {
			return nil, err
		}
		return map[string]any{"id": id, "digest": digest, "state": next, "files": counts, "gate_effect": "none"}, nil
	})
}

func importArchiveDigest(archive core.AuthorityArchive) string {
	body, _ := json.Marshal(archive)
	return store.Hash(body)
}

func authorityImportError(w http.ResponseWriter, err error) {
	code := strings.SplitN(err.Error(), ":", 2)[0]
	if strings.HasPrefix(code, "IMPORT_") || code == "IDEMPOTENCY_CONFLICT" || code == "REMOTE_OPERATION_REQUIRED" {
		status := http.StatusConflict
		if code == "IMPORT_NOT_FOUND" {
			status = http.StatusNotFound
		}
		if code == "IMPORT_ARCHIVE_INVALID" || code == "REMOTE_OPERATION_REQUIRED" {
			status = http.StatusBadRequest
		}
		standaloneFail(w, status, code)
		return
	}
	standaloneTaskError(w, err)
}

func authorityImportSchema(service *core.Service) error {
	if err := artifactSchema(service); err != nil {
		return err
	}
	_, err := service.Store.DB.Exec(`
CREATE TABLE IF NOT EXISTS authority_imports(id TEXT PRIMARY KEY,source_project TEXT NOT NULL,source_id TEXT NOT NULL,digest TEXT NOT NULL,body TEXT NOT NULL,actor TEXT NOT NULL,state TEXT NOT NULL,UNIQUE(source_project,source_id));
CREATE TABLE IF NOT EXISTS authority_import_tasks(import_id TEXT NOT NULL,original_id TEXT NOT NULL,task_id TEXT NOT NULL UNIQUE,body_hash TEXT NOT NULL,PRIMARY KEY(import_id,original_id));
CREATE TABLE IF NOT EXISTS authority_import_files(import_id TEXT NOT NULL,task_id TEXT NOT NULL,ref TEXT NOT NULL,sha256 TEXT NOT NULL,size INTEGER NOT NULL,status TEXT NOT NULL,artifact_id TEXT NOT NULL DEFAULT '',PRIMARY KEY(import_id,task_id,ref));
CREATE TABLE IF NOT EXISTS authority_import_baselines(import_id TEXT PRIMARY KEY,fingerprint TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS authority_import_metadata(import_id TEXT NOT NULL,task_id TEXT NOT NULL,key TEXT NOT NULL,PRIMARY KEY(import_id,key));
CREATE TABLE IF NOT EXISTS authority_import_stage_baselines(import_id TEXT PRIMARY KEY,fingerprint TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS authority_import_aborts(id TEXT PRIMARY KEY,digest TEXT NOT NULL,actor TEXT NOT NULL);
CREATE TRIGGER IF NOT EXISTS ael_import_task_guard BEFORE UPDATE ON tasks WHEN EXISTS(SELECT 1 FROM authority_import_tasks t JOIN authority_imports i ON i.id=t.import_id WHERE t.task_id=OLD.id AND i.state!='active') BEGIN SELECT RAISE(ABORT,'IMPORT_NOT_ACTIVE'); END;
CREATE TRIGGER IF NOT EXISTS ael_import_delete_guard BEFORE DELETE ON tasks WHEN EXISTS(SELECT 1 FROM authority_import_tasks t JOIN authority_imports i ON i.id=t.import_id WHERE t.task_id=OLD.id AND i.state!='active') BEGIN SELECT RAISE(ABORT,'IMPORT_NOT_ACTIVE'); END;`)
	if err != nil {
		return err
	}
	for _, action := range []string{"INSERT", "UPDATE", "DELETE"} {
		rows := []string{"NEW"}
		if action == "DELETE" {
			rows = []string{"OLD"}
		}
		if action == "UPDATE" {
			rows = []string{"OLD", "NEW"}
		}
		predicates := []string{}
		for _, row := range rows {
			predicates = append(predicates, fmt.Sprintf(`EXISTS(SELECT 1 FROM authority_import_tasks t JOIN authority_imports i ON i.id=t.import_id WHERE i.state!='active' AND (instr(%s.key,t.task_id)>0 OR instr(%s.body,t.task_id)>0 OR EXISTS(SELECT 1 FROM authority_import_metadata m WHERE m.import_id=t.import_id AND m.task_id=t.task_id AND m.key=%s.key)))`, row, row, row))
		}
		statement := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS ael_import_metadata_%s BEFORE %s ON metadata WHEN %s BEGIN SELECT RAISE(ABORT,'IMPORT_NOT_ACTIVE'); END`, action, action, strings.Join(predicates, " OR "))
		if _, err = service.Store.DB.Exec(statement); err != nil {
			return err
		}
	}
	for _, table := range []string{"events", "portable_observations", "portable_contexts", "portable_checks", "findings", "remote_runner_attempts", "remote_workspace_holds", "artifact_uploads", "artifact_objects", "artifact_references"} {
		var exists int
		if err = service.Store.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			continue
		}
		for _, action := range []string{"INSERT", "UPDATE", "DELETE"} {
			row := "NEW"
			if action == "DELETE" {
				row = "OLD"
			}
			condition := "i.state!='active'"
			if table == "artifact_uploads" || table == "artifact_objects" {
				condition = "i.state IN ('rollback_frozen','rolled_back')"
			}
			if table == "artifact_uploads" && action == "UPDATE" {
				condition = "(i.state='rollback_frozen' OR (i.state='rolled_back' AND NEW.status NOT IN ('purging','purged')))"
			}
			statement := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS ael_import_data_%s_%s BEFORE %s ON %s WHEN EXISTS(SELECT 1 FROM authority_import_tasks t JOIN authority_imports i ON i.id=t.import_id WHERE t.task_id=%s.task_id AND %s) BEGIN SELECT RAISE(ABORT,'IMPORT_NOT_ACTIVE'); END`, table, action, action, table, row, condition)
			if _, err = service.Store.DB.Exec(statement); err != nil {
				return err
			}
		}
	}
	_, err = service.Store.DB.Exec(`
CREATE TRIGGER IF NOT EXISTS ael_import_upload_manifest BEFORE INSERT ON artifact_uploads WHEN EXISTS(SELECT 1 FROM authority_import_tasks t JOIN authority_imports i ON i.id=t.import_id WHERE t.task_id=NEW.task_id AND i.state IN ('importing','verified') AND NOT(i.state='importing' AND NEW.owner_id=i.actor AND NEW.source='authority-import:'||i.id AND EXISTS(SELECT 1 FROM authority_import_files f WHERE f.import_id=i.id AND f.task_id=NEW.task_id AND f.status='available' AND f.sha256=NEW.sha256 AND f.size=NEW.size))) BEGIN SELECT RAISE(ABORT,'IMPORT_NOT_ACTIVE'); END;
CREATE TRIGGER IF NOT EXISTS ael_import_object_manifest BEFORE INSERT ON artifact_objects WHEN EXISTS(SELECT 1 FROM authority_import_tasks t JOIN authority_imports i ON i.id=t.import_id WHERE t.task_id=NEW.task_id AND i.state IN ('importing','verified') AND NOT(NEW.source='authority-import:'||i.id AND EXISTS(SELECT 1 FROM authority_import_files f WHERE f.import_id=i.id AND f.task_id=NEW.task_id AND f.status='available' AND f.sha256=NEW.sha256 AND f.size=NEW.size))) BEGIN SELECT RAISE(ABORT,'IMPORT_NOT_ACTIVE'); END;`)
	if err != nil {
		return err
	}
	for _, related := range []struct{ table, taskQuery string }{
		{"finding_history", "SELECT task_id FROM findings WHERE id=%s.finding_id"},
		{"finding_occurrences", "SELECT task_id FROM findings WHERE id=%s.finding_id"},
		{"portable_context_sources", "SELECT task_id FROM portable_contexts WHERE id=%s.context_id"},
	} {
		for _, action := range []string{"INSERT", "UPDATE", "DELETE"} {
			row := "NEW"
			if action == "DELETE" {
				row = "OLD"
			}
			query := fmt.Sprintf(related.taskQuery, row)
			statement := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS ael_import_data_%s_%s BEFORE %s ON %s WHEN EXISTS(SELECT 1 FROM authority_import_tasks t JOIN authority_imports i ON i.id=t.import_id WHERE t.task_id IN (%s) AND i.state!='active') BEGIN SELECT RAISE(ABORT,'IMPORT_NOT_ACTIVE'); END`, related.table, action, action, related.table, query)
			if _, err = service.Store.DB.Exec(statement); err != nil {
				return err
			}
		}
	}
	return nil
}

func authorityImportFingerprint(tx *store.Tx, id string, includeArtifacts bool) (string, error) {
	tasks := "(SELECT task_id FROM authority_import_tasks WHERE import_id=?)"
	queries := []string{
		"SELECT * FROM tasks WHERE id IN " + tasks + " ORDER BY id",
		"SELECT * FROM events WHERE task_id IN " + tasks + " ORDER BY id",
		"SELECT * FROM artifact_references WHERE task_id IN " + tasks + " ORDER BY actor,idempotency_key",
		"SELECT * FROM portable_observations WHERE task_id IN " + tasks + " ORDER BY id",
		"SELECT * FROM portable_contexts WHERE task_id IN " + tasks + " ORDER BY id",
		"SELECT * FROM portable_checks WHERE task_id IN " + tasks + " ORDER BY id",
		"SELECT * FROM findings WHERE task_id IN " + tasks + " ORDER BY id",
		"SELECT * FROM finding_history WHERE finding_id IN (SELECT id FROM findings WHERE task_id IN " + tasks + ") ORDER BY id",
		"SELECT * FROM finding_occurrences WHERE finding_id IN (SELECT id FROM findings WHERE task_id IN " + tasks + ") ORDER BY id",
		"SELECT * FROM portable_context_sources WHERE context_id IN (SELECT id FROM portable_contexts WHERE task_id IN " + tasks + ") ORDER BY context_id,id",
		"SELECT * FROM remote_runner_attempts WHERE task_id IN " + tasks + " ORDER BY attempt_id",
		"SELECT * FROM remote_workspace_holds WHERE task_id IN " + tasks + " ORDER BY repository,workspace_id,attempt_id",
		"SELECT * FROM metadata WHERE EXISTS(SELECT 1 FROM authority_import_tasks t WHERE t.import_id=? AND (instr(metadata.key,t.task_id)>0 OR instr(metadata.body,t.task_id)>0 OR EXISTS(SELECT 1 FROM authority_import_metadata m WHERE m.import_id=t.import_id AND m.task_id=t.task_id AND m.key=metadata.key))) ORDER BY key",
	}
	if includeArtifacts {
		queries = append(queries, "SELECT * FROM artifact_uploads WHERE task_id IN "+tasks+" ORDER BY id", "SELECT * FROM artifact_objects WHERE task_id IN "+tasks+" ORDER BY id")
	}
	result := [][][]any{}
	for _, query := range queries {
		rows, err := tx.Conn.QueryContext(context.Background(), query, id)
		if err != nil {
			return "", err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return "", err
		}
		entries := [][]any{}
		for rows.Next() {
			values := make([]any, len(columns))
			targets := make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			if err = rows.Scan(targets...); err != nil {
				rows.Close()
				return "", err
			}
			entries = append(entries, values)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", err
		}
		result = append(result, entries)
	}
	body, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return store.Hash(body), nil
}

func validateAuthorityArchive(archive core.AuthorityArchive) error {
	if archive.Schema != 1 || archive.Project == "" || !core.ValidArchiveSourceID(archive.Source) {
		return errors.New("IMPORT_ARCHIVE_INVALID")
	}
	seen := map[string]bool{}
	for _, task := range archive.Tasks {
		identity, _ := json.Marshal([]string{archive.Project, archive.Source, task.OriginalID})
		if task.OriginalID != task.Snapshot.Definition.ID || task.MappedID != "import-"+store.Hash(identity)[:32] || seen[task.OriginalID] || core.Validate(task.Snapshot.Definition) != nil {
			return errors.New("IMPORT_ARCHIVE_INVALID")
		}
		if task.Snapshot.Status != "open" && task.Snapshot.Status != "completed" && task.Snapshot.Status != "cancelled" {
			return errors.New("IMPORT_ARCHIVE_INVALID")
		}
		canonical := core.ArchiveTask(task.Snapshot, task.MappedID)
		// Older exports omitted recovery provenance. Keep them readable without
		// manufacturing the missing authorization facts.
		canonical.Recovery = task.Recovery
		if task.Recovery != nil && task.Recovery.Paused != task.Snapshot.Paused {
			return errors.New("IMPORT_ARCHIVE_INVALID")
		}
		clean, _ := json.Marshal(canonical)
		provided, _ := json.Marshal(task)
		if string(clean) != string(provided) {
			return errors.New("IMPORT_ARCHIVE_INVALID")
		}
		seen[task.OriginalID] = true
	}
	for _, file := range archive.Files {
		if !seen[file.TaskID] || file.Ref == "" || (file.Status == "available" && (!artifactDigest.MatchString(file.SHA256) || file.Size < 0)) {
			return errors.New("IMPORT_ARCHIVE_INVALID")
		}
	}
	return nil
}

func (p *portableStore) stageAuthorityImport(principal standalonePrincipal, input authorityImportInput) (map[string]any, error) {
	if err := validateAuthorityArchive(input.Archive); err != nil {
		return nil, err
	}
	if input.Digest != importArchiveDigest(input.Archive) {
		return nil, errors.New("IMPORT_ARCHIVE_INVALID")
	}
	return p.mutate(principal, "authority-import", input.OperationID, input.IdempotencyKey, input, func(tx *store.Tx) (map[string]any, error) {
		archive := input.Archive
		id := "import-" + input.Digest[:32]
		var aborted int
		if err := tx.Row("SELECT count(*) FROM authority_import_aborts WHERE id=?", id).Scan(&aborted); err != nil {
			return nil, err
		}
		if aborted > 0 {
			return nil, errors.New("IMPORT_ABORTED")
		}
		var previous, digest, state string
		err := tx.Row("SELECT id,digest,state FROM authority_imports WHERE source_project=? AND source_id=?", archive.Project, archive.Source).Scan(&previous, &digest, &state)
		if err == nil {
			if digest != input.Digest {
				return nil, errors.New("IMPORT_SOURCE_CONFLICT")
			}
			return map[string]any{"id": previous, "digest": digest, "state": state, "gate_effect": "none"}, nil
		}
		if err != sql.ErrNoRows {
			return nil, err
		}
		// Rebuild portable summaries from the typed source snapshot. Caller-supplied
		// summaries never provide an independent status or permission channel.
		mapping := map[string]string{}
		pending := map[string]core.ArchivedTask{}
		for _, item := range archive.Tasks {
			clean := core.ArchiveTask(item.Snapshot, item.MappedID)
			mapping[item.OriginalID] = item.MappedID
			pending[item.OriginalID] = clean
		}
		keys := make([]string, 0, len(pending))
		for key := range pending {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		created := map[string]bool{}
		for len(pending) > 0 {
			progress := false
			for _, key := range keys {
				item, exists := pending[key]
				if !exists {
					continue
				}
				definition := item.Snapshot.Definition
				related := append([]string{}, definition.Dependencies...)
				if definition.Parent != "" {
					related = append(related, definition.Parent)
				}
				if definition.FeedbackOf != "" {
					related = append(related, definition.FeedbackOf)
				}
				ready := true
				for _, old := range related {
					if mapping[old] == "" {
						return nil, errors.New("IMPORT_REFERENCE_MISSING")
					}
					if !created[old] {
						ready = false
					}
				}
				if !ready {
					continue
				}
				definition.ID = item.MappedID
				definition.Parent = mapping[definition.Parent]
				definition.FeedbackOf = mapping[definition.FeedbackOf]
				definition.Dependencies = append([]string{}, definition.Dependencies...)
				for i, old := range definition.Dependencies {
					definition.Dependencies[i] = mapping[old]
				}
				if _, err = p.service.ImportArchivedTaskTx(tx, item, definition, id); err != nil {
					return nil, err
				}
				created[key] = true
				delete(pending, key)
				progress = true
			}
			if !progress {
				return nil, errors.New("IMPORT_RELATIONSHIP_CYCLE")
			}
		}
		// Insert the ledger after normal task creation so its SQL guard protects
		// subsequent external writes while the file reconciliation remains pending.
		for _, item := range archive.Tasks {
			if err = p.service.FinalizeArchivedTaskTx(tx, item, id); err != nil {
				return nil, err
			}
		}
		body, _ := json.Marshal(archive)
		if _, err = tx.Exec("INSERT INTO authority_imports(id,source_project,source_id,digest,body,actor,state) VALUES(?,?,?,?,?,?,'importing')", id, archive.Project, archive.Source, input.Digest, string(body), principal.UserID); err != nil {
			return nil, err
		}
		for _, item := range archive.Tasks {
			var body string
			if err = tx.Row("SELECT body FROM tasks WHERE id=?", item.MappedID).Scan(&body); err != nil {
				return nil, err
			}
			if _, err = tx.Exec("INSERT INTO authority_import_tasks(import_id,original_id,task_id,body_hash) VALUES(?,?,?,?)", id, item.OriginalID, item.MappedID, store.Hash([]byte(body))); err != nil {
				return nil, err
			}
			for _, key := range core.RemoteTaskMetadataKeys(item.MappedID) {
				if _, err = tx.Exec("INSERT INTO authority_import_metadata(import_id,task_id,key) VALUES(?,?,?)", id, item.MappedID, key); err != nil {
					return nil, err
				}
			}
		}
		for _, file := range archive.Files {
			if _, err = tx.Exec("INSERT INTO authority_import_files(import_id,task_id,ref,sha256,size,status) VALUES(?,?,?,?,?,?)", id, mapping[file.TaskID], file.Ref, file.SHA256, file.Size, file.Status); err != nil {
				return nil, err
			}
		}
		fingerprint, err := authorityImportFingerprint(tx, id, false)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec("INSERT INTO authority_import_stage_baselines(import_id,fingerprint) VALUES(?,?)", id, fingerprint); err != nil {
			return nil, err
		}
		return map[string]any{"id": id, "digest": input.Digest, "state": "importing", "mapping": mapping, "gate_effect": "none"}, nil
	})
}

func (h *standaloneHandler) remoteAuthorityImports(w http.ResponseWriter, r *http.Request, principal standalonePrincipal, project string, parts []string) {
	role, err := h.server.role(principal, project)
	if err != nil {
		standaloneFail(w, 404, "PROJECT_NOT_FOUND")
		return
	}
	if r.Method != http.MethodGet && role != "admin" && role != "executor" {
		standaloneFail(w, 403, "FORBIDDEN")
		return
	}
	service, err := h.server.projectService(project)
	if err != nil {
		standaloneFail(w, 500, "SERVER_ERROR")
		return
	}
	p, err := h.server.portable(project, service)
	if err == nil {
		_, err = h.server.artifacts(project, service)
	}
	if err == nil {
		err = authorityImportSchema(service)
	}
	if err != nil {
		standaloneFail(w, 500, "SERVER_ERROR")
		return
	}
	if len(parts) == 0 && r.Method == http.MethodPost {
		var input authorityImportInput
		if decodePortable(r, &input, p) != nil {
			standaloneFail(w, 400, "IMPORT_ARCHIVE_INVALID")
			return
		}
		result, err := p.stageAuthorityImport(principal, input)
		if err != nil {
			authorityImportError(w, err)
			return
		}
		if service.Store.Flush() != nil {
			standaloneFail(w, 503, "PROJECTION_BLOCKED")
			return
		}
		standaloneReply(w, 200, result)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		var body, digest, state string
		if service.Store.DB.QueryRow("SELECT body,digest,state FROM authority_imports WHERE id=?", parts[0]).Scan(&body, &digest, &state) != nil {
			if service.Store.DB.QueryRow("SELECT digest FROM authority_import_aborts WHERE id=?", parts[0]).Scan(&digest) == nil {
				standaloneReply(w, 200, map[string]any{"id": parts[0], "digest": digest, "state": "rolled_back", "gate_effect": "none", "archive": core.AuthorityArchive{Schema: 1, Tasks: []core.ArchivedTask{}, Files: []core.ArchivedFile{}}, "files": []importedFile{}})
				return
			}
			standaloneFail(w, 404, "IMPORT_NOT_FOUND")
			return
		}
		var archive core.AuthorityArchive
		if json.Unmarshal([]byte(body), &archive) != nil {
			standaloneFail(w, 500, "IMPORT_ARCHIVE_INVALID")
			return
		}
		files, err := importedFiles(service.Store.DB, parts[0])
		if err != nil {
			standaloneFail(w, 500, "SERVER_ERROR")
			return
		}
		if taskID := r.URL.Query().Get("task_id"); taskID != "" {
			original := ""
			selected := []core.ArchivedTask{}
			for _, task := range archive.Tasks {
				if task.MappedID == taskID {
					selected = append(selected, task)
					original = task.OriginalID
				}
			}
			if original == "" {
				standaloneFail(w, 404, "TASK_NOT_FOUND")
				return
			}
			archive.Tasks = selected
			sourceFiles := []core.ArchivedFile{}
			for _, file := range archive.Files {
				if file.TaskID == original {
					sourceFiles = append(sourceFiles, file)
				}
			}
			archive.Files = sourceFiles
			mappedFiles := []importedFile{}
			for _, file := range files {
				if file.TaskID == taskID {
					mappedFiles = append(mappedFiles, file)
				}
			}
			files = mappedFiles
		}
		artifacts, err := h.server.artifacts(project, service)
		if err != nil {
			standaloneFail(w, 500, "SERVER_ERROR")
			return
		}
		for index := range files {
			file := &files[index]
			file.CurrentStatus = "unavailable"
			if file.Status != "available" {
				continue
			}
			if file.Size == 0 && file.SHA256 == store.Hash(nil) {
				file.CurrentStatus = "inline_empty"
				continue
			}
			if file.ArtifactID == "" {
				file.CurrentStatus = "pending"
				continue
			}
			object, err := artifacts.get(file.ArtifactID)
			if err != nil || object.Status != "finalized" || object.SHA256 != file.SHA256 || object.Size != file.Size {
				continue
			}
			path, err := artifacts.contentPath(service.Store.DB.QueryRow, file.ArtifactID)
			if err != nil || standaloneSafeDatabaseFile(path) != nil {
				continue
			}
			info, err := os.Stat(path)
			if err == nil && info.Size() == file.Size {
				file.CurrentStatus = "available"
			}
		}
		standaloneReply(w, 200, map[string]any{"id": parts[0], "digest": digest, "state": state, "archive": archive, "files": files, "gate_effect": "none"})
		return
	}
	if len(parts) == 2 && (parts[1] == "verify" || parts[1] == "commit" || parts[1] == "rollback-freeze" || parts[1] == "rollback-complete") && r.Method == http.MethodPost {
		var input authorityImportControl
		if decodePortable(r, &input, p) != nil {
			standaloneFail(w, 400, "IMPORT_ARCHIVE_INVALID")
			return
		}
		artifacts, err := h.server.artifacts(project, service)
		if err != nil {
			standaloneFail(w, 500, "SERVER_ERROR")
			return
		}
		result, err := p.controlAuthorityImport(principal, parts[0], parts[1], input, artifacts)
		if err != nil {
			authorityImportError(w, err)
			return
		}
		standaloneReply(w, 200, result)
		return
	}
	standaloneFail(w, 404, "NOT_FOUND")
}
