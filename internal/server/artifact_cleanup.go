package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

type artifactStats struct {
	Quota     int64 `json:"quota_bytes"`
	Used      int64 `json:"used_bytes"`
	Temporary int64 `json:"temporary_bytes"`
	Artifacts int64 `json:"artifact_bytes"`
	Reserved  int64 `json:"reserved_bytes"`
}

func artifactDirectorySizes(dir string) (int64, map[string]int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, nil, err
	}
	sizes := map[string]int64{}
	var total int64
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return 0, nil, errors.New("ARTIFACT_STORAGE_INVALID")
		}
		sizes[entry.Name()] = info.Size()
		total += info.Size()
	}
	return total, sizes, nil
}

func (a *artifactStore) usage(tx *store.Tx) (artifactStats, error) {
	result := artifactStats{Quota: artifactDefaultQuota}
	if err := tx.Row("SELECT value FROM artifact_settings WHERE key='quota'").Scan(&result.Quota); err != nil && err != sql.ErrNoRows {
		return result, err
	}
	var err error
	var uploadSizes map[string]int64
	result.Temporary, uploadSizes, err = artifactDirectorySizes(a.uploads)
	if err != nil {
		return result, err
	}
	result.Artifacts, _, err = artifactDirectorySizes(a.objects)
	if err != nil {
		return result, err
	}
	result.Used = result.Temporary + result.Artifacts
	rows, err := tx.Conn.QueryContext(context.Background(), "SELECT id,size FROM artifact_uploads WHERE status IN ('open','purging')")
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var size int64
		if err = rows.Scan(&id, &size); err != nil {
			return result, err
		}
		if remaining := size - uploadSizes[id]; remaining > 0 {
			result.Reserved += remaining
		}
	}
	return result, rows.Err()
}

func (a *artifactStore) stats() (artifactStats, error) {
	var result artifactStats
	err := a.service.Store.Write(func(tx *store.Tx) error {
		var err error
		result, err = a.usage(tx)
		return err
	})
	return result, err
}

type artifactQuotaInput struct {
	LimitBytes         int64  `json:"limit_bytes"`
	ExpectedQuotaBytes int64  `json:"expected_quota_bytes"`
	OperationID        string `json:"operation_id"`
	IdempotencyKey     string `json:"idempotency_key"`
}

func (a *artifactStore) setQuota(principal standalonePrincipal, input artifactQuotaInput) (artifactStats, error) {
	var result artifactStats
	if input.LimitBytes <= 0 || input.LimitBytes > 1<<40 || input.ExpectedQuotaBytes <= 0 || !standaloneID.MatchString(input.OperationID) || !standaloneID.MatchString(input.IdempotencyKey) {
		return result, errors.New("ARTIFACT_INVALID")
	}
	raw, _ := json.Marshal(struct {
		Actor, Project string
		Input          artifactQuotaInput
	}{principal.UserID, a.project, input})
	hash := store.Hash(raw)
	key := "artifact-quota:" + store.Hash([]byte(principal.UserID+"\n"+input.IdempotencyKey))
	err := a.service.Store.Write(func(tx *store.Tx) error {
		var saved string
		err := tx.Row("SELECT body FROM metadata WHERE key=?", key).Scan(&saved)
		if err == nil {
			var receipt struct {
				Hash   string        `json:"hash"`
				Result artifactStats `json:"result"`
			}
			if err = json.Unmarshal([]byte(saved), &receipt); err != nil {
				return err
			}
			if receipt.Hash != hash {
				return errors.New("IDEMPOTENCY_CONFLICT")
			}
			result = receipt.Result
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		result, err = a.usage(tx)
		if err != nil {
			return err
		}
		if result.Quota != input.ExpectedQuotaBytes {
			return errors.New("REVISION_CONFLICT")
		}
		if input.LimitBytes < result.Used+result.Reserved {
			return errors.New("QUOTA_TOO_SMALL")
		}
		if _, err = tx.Exec("INSERT INTO artifact_settings(key,value) VALUES('quota',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", input.LimitBytes); err != nil {
			return err
		}
		result.Quota = input.LimitBytes
		savedBytes, err := json.Marshal(struct {
			Hash   string        `json:"hash"`
			Result artifactStats `json:"result"`
		}{hash, result})
		if err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?)", key, string(savedBytes))
		return err
	})
	return result, err
}

type artifactReferenceInput struct {
	TaskID           string `json:"task_id"`
	Purpose          string `json:"purpose"`
	Candidate        string `json:"candidate"`
	ExpectedRevision string `json:"expected_revision"`
	OperationID      string `json:"operation_id"`
	IdempotencyKey   string `json:"idempotency_key"`
}

type artifactReference struct {
	ArtifactID  string `json:"artifact_id"`
	TaskID      string `json:"task_id"`
	Purpose     string `json:"purpose"`
	Candidate   string `json:"candidate"`
	Actor       string `json:"actor"`
	OperationID string `json:"operation_id"`
	Created     string `json:"created_at"`
}

func (a *artifactStore) reference(principal standalonePrincipal, id string, in artifactReferenceInput) (artifactReference, error) {
	var result artifactReference
	if !standaloneID.MatchString(id) || !standaloneID.MatchString(in.OperationID) || !standaloneID.MatchString(in.IdempotencyKey) || in.TaskID == "" || in.ExpectedRevision == "" || (in.Purpose != "reference" && in.Purpose != "evidence") {
		return result, errors.New("ARTIFACT_INVALID")
	}
	raw, _ := json.Marshal(struct {
		Actor, Project, Artifact string
		Input                    artifactReferenceInput
	}{principal.UserID, a.project, id, in})
	hash := store.Hash(raw)
	err := a.service.Store.Write(func(tx *store.Tx) error {
		var existingHash string
		err := tx.Row("SELECT request_hash,artifact_id,task_id,purpose,candidate,actor_name,operation_id,created FROM artifact_references WHERE actor=? AND idempotency_key=?", principal.UserID, in.IdempotencyKey).Scan(&existingHash, &result.ArtifactID, &result.TaskID, &result.Purpose, &result.Candidate, &result.Actor, &result.OperationID, &result.Created)
		if err == nil {
			if existingHash != hash {
				return errors.New("IDEMPOTENCY_CONFLICT")
			}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		item, err := scanArtifact(tx.Row("SELECT "+artifactColumns+" FROM artifact_objects WHERE id=?", id))
		if err != nil || item.Status != "finalized" {
			return errors.New("ARTIFACT_NOT_FOUND")
		}
		if _, err = a.read(item); err != nil {
			return err
		}
		task, err := artifactTask(tx, in.TaskID)
		if err != nil {
			return errors.New("TASK_NOT_FOUND")
		}
		if core.RemoteRevision(task) != in.ExpectedRevision {
			return errors.New("REVISION_CONFLICT")
		}
		if in.Purpose == "evidence" && (task.Candidate == "" || task.Candidate != in.Candidate || item.Candidate != in.Candidate) {
			return errors.New("ARTIFACT_CANDIDATE_MISMATCH")
		}
		result = artifactReference{ArtifactID: id, TaskID: in.TaskID, Purpose: in.Purpose, Candidate: in.Candidate, Actor: principal.Username, OperationID: in.OperationID, Created: time.Now().UTC().Format(time.RFC3339Nano)}
		_, err = tx.Exec("INSERT INTO artifact_references(artifact_id,task_id,purpose,candidate,actor,actor_name,operation_id,idempotency_key,request_hash,created) VALUES(?,?,?,?,?,?,?,?,?,?)", id, in.TaskID, in.Purpose, in.Candidate, principal.UserID, principal.Username, in.OperationID, in.IdempotencyKey, hash, result.Created)
		return err
	})
	return result, err
}

type artifactPruneInput struct {
	Before         string `json:"before"`
	Apply          bool   `json:"apply"`
	Digest         string `json:"digest"`
	OperationID    string `json:"operation_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type artifactPruneItem struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	ArtifactID string   `json:"artifact_id,omitempty"`
	Size       int64    `json:"size"`
	SHA256     string   `json:"sha256"`
	Revisions  []string `json:"revisions"`
}

type artifactPruneResult struct {
	Apply  bool                `json:"applied"`
	Count  int                 `json:"count"`
	Bytes  int64               `json:"bytes"`
	Digest string              `json:"digest"`
	Items  []artifactPruneItem `json:"items"`
}

func artifactTaskProtected(task core.Task) bool {
	if task.Status == "open" || task.Confirmation != nil {
		return true
	}
	for _, note := range task.HumanReviewNotes {
		if note.Status == "pending" {
			return true
		}
	}
	return false
}

func (a *artifactStore) pruneCandidates(tx *store.Tx, cutoff time.Time) ([]artifactPruneItem, error) {
	rows, err := tx.Conn.QueryContext(context.Background(), "SELECT body FROM tasks")
	if err != nil {
		return nil, err
	}
	tasks := map[string]core.Task{}
	bodies := map[string]string{}
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			rows.Close()
			return nil, err
		}
		var task core.Task
		if err = json.Unmarshal([]byte(body), &task); err != nil {
			rows.Close()
			return nil, err
		}
		tasks[task.Definition.ID], bodies[task.Definition.ID] = task, body
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	// A completed child or prerequisite can still be needed by an active
	// parent or dependent. Include these relations in the dry-run revision.
	dependents := map[string][]string{}
	for id, task := range tasks {
		for _, dep := range task.Definition.Dependencies {
			dependents[dep] = append(dependents[dep], id)
		}
	}
	protected := func(associated map[string]bool) (bool, []string) {
		queue := make([]string, 0, len(associated))
		for id := range associated {
			queue = append(queue, id)
		}
		seen := map[string]bool{}
		revisions := []string{}
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			if seen[id] {
				continue
			}
			seen[id] = true
			task, ok := tasks[id]
			if !ok || artifactTaskProtected(task) {
				return true, nil
			}
			revisions = append(revisions, id+":"+core.RemoteRevision(task))
			if task.Definition.Parent != "" {
				queue = append(queue, task.Definition.Parent)
			}
			if task.EpicMembership != nil && task.EpicMembership.Parent != "" {
				queue = append(queue, task.EpicMembership.Parent)
			}
			queue = append(queue, dependents[id]...)
		}
		sort.Strings(revisions)
		return false, revisions
	}
	refs := map[string][]string{}
	rows, err = tx.Conn.QueryContext(context.Background(), "SELECT artifact_id,task_id FROM artifact_references")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, task string
		if err = rows.Scan(&id, &task); err != nil {
			rows.Close()
			return nil, err
		}
		refs[id] = append(refs[id], task)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	retained := map[string]bool{}
	rows, err = tx.Conn.QueryContext(context.Background(), "SELECT DISTINCT artifact_id FROM portable_context_sources")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		retained[id] = true
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	items := []artifactPruneItem{}
	rows, err = tx.Conn.QueryContext(context.Background(), "SELECT "+artifactColumns+" FROM artifact_objects WHERE status='finalized'")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		item, err := scanArtifact(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if retained[item.ID] {
			continue
		}
		created, err := time.Parse(time.RFC3339Nano, item.Created)
		if err != nil || !created.Before(cutoff) {
			continue
		}
		associated := map[string]bool{item.TaskID: true}
		for _, id := range refs[item.ID] {
			associated[id] = true
		}
		for id, body := range bodies {
			if strings.Contains(body, item.ID) {
				associated[id] = true
			}
		}
		blocked, revisions := protected(associated)
		if !blocked {
			items = append(items, artifactPruneItem{ID: item.ID, Kind: "artifact", Size: item.Size, SHA256: item.SHA256, Revisions: revisions})
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rows, err = tx.Conn.QueryContext(context.Background(), "SELECT id,artifact_id,task_id,size,sha256,created FROM artifact_uploads WHERE status='open'")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, artifactID, taskID, digest, created string
		var size int64
		if err = rows.Scan(&id, &artifactID, &taskID, &size, &digest, &created); err != nil {
			rows.Close()
			return nil, err
		}
		at, err := time.Parse(time.RFC3339Nano, created)
		blocked, revisions := protected(map[string]bool{taskID: true})
		if err != nil || !at.Before(cutoff) || blocked {
			continue
		}
		path, err := a.uploadPath(id)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if info, err := os.Lstat(path); err == nil {
			size = info.Size()
		} else if os.IsNotExist(err) {
			size = 0
		} else {
			rows.Close()
			return nil, err
		}
		dest, err := a.objectPath(artifactID)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if info, statErr := os.Lstat(dest); statErr == nil {
			size += info.Size()
		} else if !os.IsNotExist(statErr) {
			rows.Close()
			return nil, statErr
		}
		items = append(items, artifactPruneItem{ID: id, Kind: "upload", ArtifactID: artifactID, Size: size, SHA256: digest, Revisions: revisions})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	sort.Slice(items, func(i, j int) bool { return items[i].Kind+items[i].ID < items[j].Kind+items[j].ID })
	return items, nil
}

type artifactPruneReceipt struct {
	Hash   string              `json:"hash"`
	Result artifactPruneResult `json:"result"`
	State  string              `json:"state"`
}

// Validate every selected file before committing deletion intent. An upload
// may have been interrupted after linking the object but before its DB commit.
func (a *artifactStore) prunePaths(tx *store.Tx, item artifactPruneItem) ([]string, error) {
	if item.Kind == "artifact" {
		object, err := scanArtifact(tx.Row("SELECT "+artifactColumns+" FROM artifact_objects WHERE id=?", item.ID))
		if err != nil {
			return nil, err
		}
		path, err := a.objectPath(item.ID)
		if err != nil {
			return nil, err
		}
		if _, err = os.Lstat(path); err == nil {
			if _, err = artifactFileBytes(path, object.Size, object.SHA256, object.MIME); err != nil {
				return nil, err
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		return []string{path}, nil
	}
	upload, err := scanUpload(tx.Row("SELECT "+uploadColumns+" FROM artifact_uploads WHERE id=?", item.ID))
	if err != nil || upload.ArtifactID != item.ArtifactID {
		return nil, errors.New("ARTIFACT_STORAGE_INVALID")
	}
	temp, err := a.uploadPath(item.ID)
	if err != nil {
		return nil, err
	}
	dest, err := a.objectPath(item.ArtifactID)
	if err != nil {
		return nil, err
	}
	tempInfo, tempErr := os.Lstat(temp)
	destInfo, destErr := os.Lstat(dest)
	if tempErr != nil && !os.IsNotExist(tempErr) {
		return nil, tempErr
	}
	if destErr != nil && !os.IsNotExist(destErr) {
		return nil, destErr
	}
	paired := tempErr == nil && destErr == nil && os.SameFile(tempInfo, destInfo)
	if paired {
		// Both names are in private storage. Verify the full content before
		// unlinking either; a symlink cannot pass SafePath or IsRegular.
		if !tempInfo.Mode().IsRegular() || tempInfo.Mode().Perm()&0077 != 0 || standaloneNoACL(temp) != nil || standaloneLocalFilesystem(temp) != nil {
			return nil, errors.New("ARTIFACT_STORAGE_INVALID")
		}
		file, readErr := os.Open(temp)
		if readErr != nil {
			return nil, errors.New("ARTIFACT_CONTENT_MISMATCH")
		}
		data, readErr := io.ReadAll(io.LimitReader(file, artifactFileLimit+1))
		if closeErr := file.Close(); readErr == nil {
			readErr = closeErr
		}
		if readErr != nil || int64(len(data)) != upload.Size || store.Hash(data) != upload.SHA256 || validateArtifactBytes(data, upload.MIME) != nil {
			return nil, errors.New("ARTIFACT_CONTENT_MISMATCH")
		}
	} else {
		if tempErr == nil && standaloneSafeDatabaseFile(temp) != nil {
			return nil, errors.New("ARTIFACT_STORAGE_INVALID")
		}
		if destErr == nil {
			if _, err = artifactFileBytes(dest, upload.Size, upload.SHA256, upload.MIME); err != nil {
				return nil, err
			}
		}
	}
	return []string{temp, dest}, nil
}

// The owner can abandon an unfinished upload even while its task is active.
// The original operation and idempotency key fence cancellation and retry.
func (a *artifactStore) cancelUpload(principal standalonePrincipal, id, operationID, key string) (artifactStartResult, error) {
	var result artifactStartResult
	var item artifactPruneItem
	err := a.service.Store.Write(func(tx *store.Tx) error {
		upload, err := scanUpload(tx.Row("SELECT "+uploadColumns+" FROM artifact_uploads WHERE id=?", id))
		if err != nil || (upload.OwnerID != principal.UserID && !principal.Admin) {
			return errors.New("UPLOAD_NOT_FOUND")
		}
		if upload.OperationID != operationID || upload.IdempotencyKey != key {
			return errors.New("IDEMPOTENCY_CONFLICT")
		}
		if upload.Status != "open" && upload.Status != "purging" && upload.Status != "purged" {
			return errors.New("UPLOAD_CONFLICT")
		}
		result = artifactStartResult{UploadID: upload.ID, ArtifactID: upload.ArtifactID, Received: upload.Received, Status: upload.Status}
		if upload.Status == "purged" {
			return nil
		}
		item = artifactPruneItem{ID: upload.ID, ArtifactID: upload.ArtifactID, Kind: "upload"}
		if _, err = a.prunePaths(tx, item); err != nil {
			return err
		}
		_, err = tx.Exec("UPDATE artifact_uploads SET status='purging' WHERE id=?", id)
		return err
	})
	if err != nil || result.Status == "purged" {
		return result, err
	}
	err = a.service.Store.Write(func(tx *store.Tx) error {
		paths, err := a.prunePaths(tx, item)
		if err != nil {
			return err
		}
		for _, path := range paths {
			if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if err = artifactSyncDirs(a.objects, a.uploads); err != nil {
			return err
		}
		_, err = tx.Exec("UPDATE artifact_uploads SET status='purged' WHERE id=? AND status='purging'", id)
		return err
	})
	if err == nil {
		result.Status = "purged"
	}
	return result, err
}

func (a *artifactStore) prune(principal standalonePrincipal, input artifactPruneInput) (artifactPruneResult, error) {
	var result artifactPruneResult
	cutoff, err := time.Parse(time.RFC3339, input.Before)
	if err != nil || (input.Apply && (!standaloneID.MatchString(input.OperationID) || !standaloneID.MatchString(input.IdempotencyKey) || input.Digest == "")) {
		return result, errors.New("ARTIFACT_INVALID")
	}
	raw, _ := json.Marshal(struct {
		Actor, Project string
		Input          artifactPruneInput
	}{principal.UserID, a.project, input})
	hash := store.Hash(raw)
	key := "artifact-prune:" + store.Hash([]byte(principal.UserID+"\n"+input.IdempotencyKey))
	receipt := artifactPruneReceipt{}
	err = a.service.Store.Write(func(tx *store.Tx) error {
		if input.Apply {
			var saved string
			err := tx.Row("SELECT body FROM metadata WHERE key=?", key).Scan(&saved)
			if err == nil {
				if err = json.Unmarshal([]byte(saved), &receipt); err != nil {
					return err
				}
				if receipt.Hash != hash {
					return errors.New("IDEMPOTENCY_CONFLICT")
				}
				result = receipt.Result
				return nil
			}
			if err != sql.ErrNoRows {
				return err
			}
		}
		items, err := a.pruneCandidates(tx, cutoff)
		if err != nil {
			return err
		}
		basis, _ := json.Marshal(struct {
			Before string
			Items  []artifactPruneItem
		}{input.Before, items})
		result = artifactPruneResult{Apply: input.Apply, Count: len(items), Digest: store.Hash(basis), Items: items}
		for _, item := range items {
			result.Bytes += item.Size
		}
		if !input.Apply {
			return nil
		}
		if input.Digest != result.Digest {
			return errors.New("PRUNE_STALE")
		}
		for _, item := range items {
			if _, err = a.prunePaths(tx, item); err != nil {
				return err
			}
		}
		// Persist intent before deleting files. Interrupted cleanup is resumed
		// by replaying the same request/key, without pretending files exist.
		for _, item := range items {
			table := "artifact_objects"
			if item.Kind == "upload" {
				table = "artifact_uploads"
			}
			if _, err = tx.Exec("UPDATE "+table+" SET status='purging' WHERE id=?", item.ID); err != nil {
				return err
			}
		}
		receipt = artifactPruneReceipt{Hash: hash, Result: result, State: "pending"}
		saved, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?)", key, string(saved))
		return err
	})
	if err != nil || !input.Apply || receipt.State == "done" || receipt.State == "" {
		return result, err
	}
	if receipt.State == "stale" {
		return result, errors.New("PRUNE_STALE")
	}
	stale := false
	err = a.service.Store.Write(func(tx *store.Tx) error {
		// Another replay may already have completed this intent.
		var saved string
		if err := tx.Row("SELECT body FROM metadata WHERE key=?", key).Scan(&saved); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(saved), &receipt); err != nil {
			return err
		}
		if receipt.Hash != hash {
			return errors.New("IDEMPOTENCY_CONFLICT")
		}
		if receipt.State == "done" {
			return nil
		}
		if receipt.State == "stale" {
			stale = true
			return nil
		}
		// Recheck under the write lock: a parent or dependent may have become
		// active after an interrupted intent was committed.
		for _, item := range receipt.Result.Items {
			table, status := "artifact_objects", "finalized"
			if item.Kind == "upload" {
				table, status = "artifact_uploads", "open"
			}
			if _, err := tx.Exec("UPDATE "+table+" SET status=? WHERE id=? AND status='purging'", status, item.ID); err != nil {
				return err
			}
		}
		eligible, err := a.pruneCandidates(tx, cutoff)
		if err != nil {
			return err
		}
		byID := map[string]artifactPruneItem{}
		for _, item := range eligible {
			byID[item.Kind+":"+item.ID] = item
		}
		for _, item := range receipt.Result.Items {
			current, ok := byID[item.Kind+":"+item.ID]
			if !ok || current.ArtifactID != item.ArtifactID || current.SHA256 != item.SHA256 || !slices.Equal(current.Revisions, item.Revisions) {
				stale = true
				break
			}
		}
		if stale {
			// If a prior interrupted attempt already removed a file, keep that
			// fact visible instead of restoring a nonexistent original.
			for _, item := range receipt.Result.Items {
				paths, pathErr := a.prunePaths(tx, item)
				if pathErr != nil {
					return pathErr
				}
				found := false
				for _, path := range paths {
					if _, statErr := os.Lstat(path); statErr == nil {
						found = true
					} else if !os.IsNotExist(statErr) {
						return statErr
					}
				}
				if !found {
					table := "artifact_objects"
					if item.Kind == "upload" {
						table = "artifact_uploads"
					}
					if _, err = tx.Exec("UPDATE "+table+" SET status='purged' WHERE id=?", item.ID); err != nil {
						return err
					}
				}
			}
			receipt.State = "stale"
			updated, marshalErr := json.Marshal(receipt)
			if marshalErr != nil {
				return marshalErr
			}
			_, err = tx.Exec("UPDATE metadata SET body=? WHERE key=?", string(updated), key)
			return err
		}
		for _, item := range receipt.Result.Items {
			table, status := "artifact_objects", "finalized"
			if item.Kind == "upload" {
				table, status = "artifact_uploads", "open"
			}
			if _, err = tx.Exec("UPDATE "+table+" SET status='purging' WHERE id=? AND status=?", item.ID, status); err != nil {
				return err
			}
		}
		for _, item := range receipt.Result.Items {
			paths, err := a.prunePaths(tx, item)
			if err != nil {
				return err
			}
			for _, path := range paths {
				if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
			table := "artifact_objects"
			if item.Kind == "upload" {
				table = "artifact_uploads"
			}
			if _, err = tx.Exec("UPDATE "+table+" SET status='purged' WHERE id=? AND status='purging'", item.ID); err != nil {
				return err
			}
		}
		if err = artifactSyncDirs(a.objects, a.uploads); err != nil {
			return err
		}
		receipt.State = "done"
		finalBytes, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		_, err = tx.Exec("UPDATE metadata SET body=? WHERE key=?", string(finalBytes), key)
		return err
	})
	if err == nil && stale {
		return result, errors.New("PRUNE_STALE")
	}
	return result, err
}
