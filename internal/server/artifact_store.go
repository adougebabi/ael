package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"bytes"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const artifactFileLimit int64 = 10 << 20
const artifactChunkLimit = 256 << 10
const artifactDefaultQuota int64 = 100 << 20

var artifactDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

type artifactStore struct {
	service *core.Service
	project string
	uploads string
	objects string
}

type artifactStartInput struct {
	OperationID      string `json:"operation_id"`
	IdempotencyKey   string `json:"idempotency_key"`
	ExpectedRevision string `json:"expected_revision"`
	TaskID           string `json:"task_id"`
	AttemptID        string `json:"attempt_id"`
	Candidate        string `json:"candidate"`
	Filename         string `json:"filename"`
	MIME             string `json:"mime"`
	Size             int64  `json:"size"`
	SHA256           string `json:"sha256"`
	Source           string `json:"source"`
	RunnerID         string `json:"runner_id,omitempty"`
	LeaseToken       string `json:"lease_token,omitempty"`
	Fence            int    `json:"fence,omitempty"`
}

type artifactLeaseInput struct {
	RunnerID   string `json:"runner_id,omitempty"`
	LeaseToken string `json:"lease_token,omitempty"`
	Fence      int    `json:"fence,omitempty"`
}

type artifactUpload struct {
	ID, ArtifactID, ProjectID, TaskID, AttemptID, Candidate string
	Filename, MIME, SHA256, Source, OwnerID, Uploader       string
	OperationID, IdempotencyKey, RequestHash, Status        string
	Size, Received                                          int64
	Created                                                 string
}

type artifactObject struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	TaskID    string `json:"task_id"`
	AttemptID string `json:"attempt_id"`
	Candidate string `json:"candidate"`
	Filename  string `json:"filename"`
	MIME      string `json:"mime"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Source    string `json:"source"`
	Uploader  string `json:"uploader"`
	Created   string `json:"created_at"`
	Status    string `json:"status"`
}

type artifactStartResult struct {
	UploadID   string `json:"upload_id"`
	ArtifactID string `json:"artifact_id"`
	Received   int64  `json:"received"`
	Status     string `json:"status"`
}

func (s *Standalone) artifacts(project string, service *core.Service) (*artifactStore, error) {
	base, err := store.SafePath(s.store.Dir, filepath.Join("attachments", store.Hash([]byte(project))[:32]))
	if err != nil {
		return nil, err
	}
	a := &artifactStore{service: service, project: project, uploads: filepath.Join(base, "uploads"), objects: filepath.Join(base, "objects")}
	for _, dir := range []string{base, a.uploads, a.objects} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return nil, errors.New("ARTIFACT_STORAGE_INVALID")
		}
		if err = standaloneNoACL(dir); err != nil {
			return nil, err
		}
		if err = standaloneLocalFilesystem(dir); err != nil {
			return nil, err
		}
	}
	_, err = service.Store.DB.Exec(`
CREATE TABLE IF NOT EXISTS artifact_uploads(id TEXT PRIMARY KEY,artifact_id TEXT NOT NULL UNIQUE,project_id TEXT NOT NULL,task_id TEXT NOT NULL,attempt_id TEXT NOT NULL,candidate TEXT NOT NULL,filename TEXT NOT NULL,mime TEXT NOT NULL,size INTEGER NOT NULL,sha256 TEXT NOT NULL,source TEXT NOT NULL,owner_id TEXT NOT NULL,uploader TEXT NOT NULL,operation_id TEXT NOT NULL,idempotency_key TEXT NOT NULL,request_hash TEXT NOT NULL,status TEXT NOT NULL,received INTEGER NOT NULL,created TEXT NOT NULL,UNIQUE(owner_id,idempotency_key));
CREATE TABLE IF NOT EXISTS artifact_objects(id TEXT PRIMARY KEY,project_id TEXT NOT NULL,task_id TEXT NOT NULL,attempt_id TEXT NOT NULL,candidate TEXT NOT NULL,filename TEXT NOT NULL,mime TEXT NOT NULL,size INTEGER NOT NULL,sha256 TEXT NOT NULL,source TEXT NOT NULL,uploader TEXT NOT NULL,created TEXT NOT NULL,status TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS artifact_references(artifact_id TEXT NOT NULL,task_id TEXT NOT NULL,purpose TEXT NOT NULL,candidate TEXT NOT NULL,actor TEXT NOT NULL,actor_name TEXT NOT NULL,operation_id TEXT NOT NULL,idempotency_key TEXT NOT NULL,request_hash TEXT NOT NULL,created TEXT NOT NULL,PRIMARY KEY(actor,idempotency_key));
CREATE TABLE IF NOT EXISTS portable_context_sources(context_id TEXT NOT NULL,id TEXT NOT NULL,path TEXT NOT NULL,artifact_id TEXT NOT NULL,sha256 TEXT NOT NULL,status TEXT NOT NULL,kind TEXT NOT NULL,reason TEXT NOT NULL,PRIMARY KEY(context_id,id));
CREATE TABLE IF NOT EXISTS remote_artifact_leases(upload_id TEXT PRIMARY KEY,runner_id TEXT NOT NULL,attempt_id TEXT NOT NULL,fence INTEGER NOT NULL,token_hash TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS artifact_settings(key TEXT PRIMARY KEY,value INTEGER NOT NULL);`)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func artifactNameValid(name, mimeType string) bool {
	if name == "" || len(name) > 180 || !utf8.ValidString(name) || strings.TrimSpace(name) != name || strings.HasPrefix(name, ".") || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	ext := strings.ToLower(filepath.Ext(name))
	switch mimeType {
	case "image/png":
		return ext == ".png"
	case "image/jpeg":
		return ext == ".jpg" || ext == ".jpeg"
	case "text/plain":
		return ext == ".txt" || ext == ".md" || ext == ".log" || ext == ".json" || ext == ".csv"
	case "video/mp4":
		return ext == ".mp4"
	case "application/octet-stream":
		return ext == ".bin"
	default:
		return false
	}
}

func artifactTask(tx *store.Tx, id string) (core.Task, error) {
	var task core.Task
	var body string
	if err := tx.Row("SELECT body FROM tasks WHERE id=?", id).Scan(&body); err != nil {
		return task, err
	}
	err := json.Unmarshal([]byte(body), &task)
	return task, err
}

type artifactScanner interface{ Scan(...any) error }

func scanUpload(row artifactScanner) (artifactUpload, error) {
	var u artifactUpload
	err := row.Scan(&u.ID, &u.ArtifactID, &u.ProjectID, &u.TaskID, &u.AttemptID, &u.Candidate, &u.Filename, &u.MIME, &u.Size, &u.SHA256, &u.Source, &u.OwnerID, &u.Uploader, &u.OperationID, &u.IdempotencyKey, &u.RequestHash, &u.Status, &u.Received, &u.Created)
	return u, err
}

const uploadColumns = "id,artifact_id,project_id,task_id,attempt_id,candidate,filename,mime,size,sha256,source,owner_id,uploader,operation_id,idempotency_key,request_hash,status,received,created"

func scanArtifact(row artifactScanner) (artifactObject, error) {
	var item artifactObject
	err := row.Scan(&item.ID, &item.ProjectID, &item.TaskID, &item.AttemptID, &item.Candidate, &item.Filename, &item.MIME, &item.Size, &item.SHA256, &item.Source, &item.Uploader, &item.Created, &item.Status)
	return item, err
}

const artifactColumns = "id,project_id,task_id,attempt_id,candidate,filename,mime,size,sha256,source,uploader,created,status"

func (a *artifactStore) uploadPath(id string) (string, error) {
	if !standaloneID.MatchString(id) {
		return "", errors.New("UPLOAD_NOT_FOUND")
	}
	return store.SafePath(a.uploads, id)
}

func (a *artifactStore) objectPath(id string) (string, error) {
	if !standaloneID.MatchString(id) {
		return "", errors.New("ARTIFACT_NOT_FOUND")
	}
	return store.SafePath(a.objects, id)
}

func (a *artifactStore) verifyUploadLease(tx *store.Tx, principal standalonePrincipal, upload artifactUpload, auth artifactLeaseInput) error {
	var runner, attemptID, hash string
	var fence int
	err := tx.Row("SELECT runner_id,attempt_id,fence,token_hash FROM remote_artifact_leases WHERE upload_id=?", upload.ID).Scan(&runner, &attemptID, &fence, &hash)
	if err == sql.ErrNoRows {
		if auth.RunnerID != "" || auth.LeaseToken != "" || auth.Fence != 0 {
			return errors.New("ARTIFACT_INVALID")
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !remoteRunnerIsolationVerified() {
		return errors.New("RUNNER_ISOLATION_REQUIRED")
	}
	if auth.RunnerID != runner || auth.Fence != fence || auth.LeaseToken == "" || subtle.ConstantTimeCompare([]byte(store.Hash([]byte(auth.LeaseToken))), []byte(hash)) != 1 || upload.AttemptID != attemptID {
		return errors.New("STALE_TOKEN")
	}
	p := &portableStore{service: a.service, project: a.project}
	if err = p.verifyRunnerLease(tx, principal, runner, upload.TaskID, attemptID, auth.LeaseToken, fence); err != nil {
		return err
	}
	_, _, err = a.service.RemoteAttemptTx(tx, upload.TaskID, runner, attemptID, auth.LeaseToken, fence)
	return err
}

func (a *artifactStore) begin(principal standalonePrincipal, in artifactStartInput) (artifactStartResult, error) {
	var result artifactStartResult
	if !standaloneID.MatchString(in.OperationID) || !standaloneID.MatchString(in.IdempotencyKey) || in.ExpectedRevision == "" || in.TaskID == "" || !artifactNameValid(in.Filename, in.MIME) || in.Size <= 0 || in.Size > artifactFileLimit || !artifactDigest.MatchString(in.SHA256) || strings.TrimSpace(in.Source) == "" || len(in.Source) > 200 {
		return result, errors.New("ARTIFACT_INVALID")
	}
	if in.MIME == "application/octet-stream" && in.RunnerID == "" {
		return result, errors.New("ARTIFACT_INVALID")
	}
	request, err := json.Marshal(struct {
		Actor, Project string
		Input          artifactStartInput
	}{principal.UserID, a.project, in})
	if err != nil {
		return result, err
	}
	hash := store.Hash(request)
	uploadID, err := standaloneRandom("upload_")
	if err != nil {
		return result, err
	}
	artifactID, err := standaloneRandom("artifact_")
	if err != nil {
		return result, err
	}
	err = a.service.Store.Write(func(tx *store.Tx) error {
		existing, err := scanUpload(tx.Row("SELECT "+uploadColumns+" FROM artifact_uploads WHERE owner_id=? AND idempotency_key=?", principal.UserID, in.IdempotencyKey))
		if err == nil {
			if existing.RequestHash != hash {
				return errors.New("IDEMPOTENCY_CONFLICT")
			}
			result = artifactStartResult{UploadID: existing.ID, ArtifactID: existing.ArtifactID, Received: existing.Received, Status: existing.Status}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		task, err := artifactTask(tx, in.TaskID)
		if err != nil {
			return errors.New("TASK_NOT_FOUND")
		}
		runnerUpload := in.RunnerID != "" || in.LeaseToken != "" || in.Fence != 0
		if runnerUpload {
			if !remoteRunnerIsolationVerified() {
				return errors.New("RUNNER_ISOLATION_REQUIRED")
			}
			if !standaloneID.MatchString(in.RunnerID) || !standaloneID.MatchString(in.AttemptID) || in.LeaseToken == "" || in.Fence < 1 {
				return errors.New("STALE_TOKEN")
			}
			p := &portableStore{service: a.service, project: a.project}
			if err = p.verifyRunnerLease(tx, principal, in.RunnerID, in.TaskID, in.AttemptID, in.LeaseToken, in.Fence); err != nil {
				return err
			}
			if _, _, err = a.service.RemoteAttemptTx(tx, in.TaskID, in.RunnerID, in.AttemptID, in.LeaseToken, in.Fence); err != nil {
				return err
			}
			var revision, output string
			if err = tx.Row("SELECT claim_revision,output_candidate FROM remote_runner_attempts WHERE attempt_id=?", in.AttemptID).Scan(&revision, &output); err != nil {
				return err
			}
			if revision != in.ExpectedRevision || output == "" || output != in.Candidate {
				return errors.New("REVISION_CONFLICT")
			}
		} else if core.RemoteRevision(task) != in.ExpectedRevision || task.Candidate != in.Candidate {
			return errors.New("REVISION_CONFLICT")
		}
		if in.AttemptID != "" && !runnerUpload {
			found := false
			for _, attempt := range task.Attempts {
				if attempt.ID == in.AttemptID && (in.Candidate == "" || attempt.Input == in.Candidate || attempt.Candidate == in.Candidate) {
					if strings.HasPrefix(attempt.Workspace, "remote:") {
						return errors.New("STALE_TOKEN")
					}
					found = true
				}
			}
			if !found {
				return errors.New("ATTEMPT_NOT_FOUND")
			}
		}
		stats, err := a.usage(tx)
		if err != nil {
			return err
		}
		if stats.Used+stats.Reserved+in.Size > stats.Quota {
			return errors.New("QUOTA_EXCEEDED")
		}
		created := time.Now().UTC().Format(time.RFC3339Nano)
		_, err = tx.Exec("INSERT INTO artifact_uploads("+uploadColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", uploadID, artifactID, a.project, in.TaskID, in.AttemptID, in.Candidate, in.Filename, in.MIME, in.Size, in.SHA256, in.Source, principal.UserID, principal.Username, in.OperationID, in.IdempotencyKey, hash, "open", 0, created)
		if err == nil && runnerUpload {
			_, err = tx.Exec("INSERT INTO remote_artifact_leases(upload_id,runner_id,attempt_id,fence,token_hash) VALUES(?,?,?,?,?)", uploadID, in.RunnerID, in.AttemptID, in.Fence, store.Hash([]byte(in.LeaseToken)))
		}
		if err == nil {
			result = artifactStartResult{UploadID: uploadID, ArtifactID: artifactID, Status: "open"}
		}
		return err
	})
	if err != nil || result.Status != "open" {
		return result, err
	}
	// Commit the reservation before creating a file. A crash can then leave
	// only a tracked upload, and replay can safely create its missing stage.
	err = a.service.Store.Write(func(tx *store.Tx) error {
		upload, err := scanUpload(tx.Row("SELECT "+uploadColumns+" FROM artifact_uploads WHERE id=?", result.UploadID))
		if err != nil {
			return err
		}
		result = artifactStartResult{UploadID: upload.ID, ArtifactID: upload.ArtifactID, Received: upload.Received, Status: upload.Status}
		if upload.Status != "open" {
			return nil
		}
		if err = a.verifyUploadLease(tx, principal, upload, artifactLeaseInput{RunnerID: in.RunnerID, LeaseToken: in.LeaseToken, Fence: in.Fence}); err != nil {
			return err
		}
		dest, err := a.objectPath(upload.ArtifactID)
		if err != nil {
			return err
		}
		if _, err = os.Lstat(dest); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		path, err := a.uploadPath(upload.ID)
		if err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if os.IsExist(err) {
			return standaloneSafeDatabaseFile(path)
		}
		if err != nil {
			return err
		}
		return file.Close()
	})
	return result, err
}

func (a *artifactStore) chunk(principal standalonePrincipal, id string, offset int64, data []byte, auth artifactLeaseInput) (artifactStartResult, error) {
	var result artifactStartResult
	if offset < 0 || len(data) == 0 || len(data) > artifactChunkLimit {
		return result, errors.New("ARTIFACT_INVALID")
	}
	err := a.service.Store.Write(func(tx *store.Tx) error {
		upload, err := scanUpload(tx.Row("SELECT "+uploadColumns+" FROM artifact_uploads WHERE id=?", id))
		if err != nil || (upload.OwnerID != principal.UserID && !principal.Admin) {
			return errors.New("UPLOAD_NOT_FOUND")
		}
		if upload.Status != "open" || offset+int64(len(data)) > upload.Size {
			return errors.New("UPLOAD_CONFLICT")
		}
		if err = a.verifyUploadLease(tx, principal, upload, auth); err != nil {
			return err
		}
		path, err := a.uploadPath(id)
		if err != nil || standaloneSafeDatabaseFile(path) != nil {
			return errors.New("UPLOAD_UNAVAILABLE")
		}
		file, err := os.OpenFile(path, os.O_RDWR, 0600)
		if err != nil {
			return errors.New("UPLOAD_UNAVAILABLE")
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || offset > info.Size() || info.Size() > upload.Size {
			return errors.New("UPLOAD_CONFLICT")
		}
		existing := min(int64(len(data)), info.Size()-offset)
		if existing > 0 {
			prefix := make([]byte, existing)
			if _, err = file.ReadAt(prefix, offset); err != nil || !bytes.Equal(prefix, data[:existing]) {
				return errors.New("UPLOAD_CONFLICT")
			}
		}
		if existing < int64(len(data)) {
			if _, err = file.WriteAt(data[existing:], offset+existing); err != nil {
				return err
			}
			if err = file.Sync(); err != nil {
				return err
			}
		}
		if err = a.verifyUploadLease(tx, principal, upload, auth); err != nil {
			return err
		}
		received := max(info.Size(), offset+int64(len(data)))
		if _, err = tx.Exec("UPDATE artifact_uploads SET received=? WHERE id=?", received, id); err != nil {
			return err
		}
		result = artifactStartResult{UploadID: upload.ID, ArtifactID: upload.ArtifactID, Received: received, Status: upload.Status}
		return nil
	})
	return result, err
}

func validateArtifactBytes(data []byte, mimeType string) error {
	if len(data) == 0 || int64(len(data)) > artifactFileLimit {
		return errors.New("ARTIFACT_INVALID")
	}
	switch mimeType {
	case "image/png", "image/jpeg":
		config, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 25_000_000 || (mimeType == "image/png" && format != "png") || (mimeType == "image/jpeg" && format != "jpeg") {
			return errors.New("ARTIFACT_MIME_INVALID")
		}
		if _, decoded, err := image.Decode(bytes.NewReader(data)); err != nil || decoded != format {
			return errors.New("ARTIFACT_MIME_INVALID")
		}
	case "text/plain":
		detected := http.DetectContentType(data)
		trimmed := strings.ToLower(strings.TrimSpace(string(data)))
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 || !strings.HasPrefix(detected, "text/plain") || strings.HasPrefix(trimmed, "<svg") || strings.HasPrefix(trimmed, "<?xml") {
			return errors.New("ARTIFACT_MIME_INVALID")
		}
	case "video/mp4":
		if http.DetectContentType(data) != "video/mp4" {
			return errors.New("ARTIFACT_MIME_INVALID")
		}
	case "application/octet-stream":
		// Runner outputs are download-only; they are never rendered as active
		// same-origin content. Size and hash are still verified.
	default:
		return errors.New("ARTIFACT_MIME_INVALID")
	}
	return nil
}

func artifactFileBytes(path string, size int64, digest, mimeType string) ([]byte, error) {
	if err := standaloneSafeDatabaseFile(path); err != nil {
		return nil, errors.New("ARTIFACT_UNAVAILABLE")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("ARTIFACT_UNAVAILABLE")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, artifactFileLimit+1))
	if err != nil || int64(len(data)) != size || store.Hash(data) != digest {
		return nil, errors.New("ARTIFACT_CONTENT_MISMATCH")
	}
	if err = validateArtifactBytes(data, mimeType); err != nil {
		return nil, err
	}
	return data, nil
}

func artifactSyncDirs(dirs ...string) error {
	for _, dir := range dirs {
		f, err := os.Open(dir)
		if err != nil {
			return err
		}
		err = f.Sync()
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *artifactStore) finalize(principal standalonePrincipal, id, operationID, key string, auth artifactLeaseInput) (artifactObject, error) {
	var result artifactObject
	err := a.service.Store.Write(func(tx *store.Tx) error {
		upload, err := scanUpload(tx.Row("SELECT "+uploadColumns+" FROM artifact_uploads WHERE id=?", id))
		if err != nil || (upload.OwnerID != principal.UserID && !principal.Admin) {
			return errors.New("UPLOAD_NOT_FOUND")
		}
		if upload.OperationID != operationID || upload.IdempotencyKey != key {
			return errors.New("IDEMPOTENCY_CONFLICT")
		}
		if upload.Status == "finalized" {
			result, err = scanArtifact(tx.Row("SELECT "+artifactColumns+" FROM artifact_objects WHERE id=?", upload.ArtifactID))
			return err
		}
		if upload.Status != "open" {
			return errors.New("UPLOAD_CONFLICT")
		}
		if err = a.verifyUploadLease(tx, principal, upload, auth); err != nil {
			return err
		}
		temp, err := a.uploadPath(id)
		if err != nil {
			return err
		}
		dest, err := a.objectPath(upload.ArtifactID)
		if err != nil {
			return err
		}
		tempInfo, tempErr := os.Lstat(temp)
		destInfo, destErr := os.Lstat(dest)
		if tempErr == nil && destErr == nil && os.SameFile(tempInfo, destInfo) {
			if err = os.Remove(temp); err != nil {
				return err
			}
			tempErr = os.ErrNotExist
		}
		if tempErr == nil {
			if tempInfo.Size() != upload.Size {
				return errors.New("UPLOAD_INCOMPLETE")
			}
			if _, err = artifactFileBytes(temp, upload.Size, upload.SHA256, upload.MIME); err != nil {
				return err
			}
			if destErr == nil {
				return errors.New("ARTIFACT_CONFLICT")
			}
			if !os.IsNotExist(destErr) {
				return destErr
			}
			if err = os.Link(temp, dest); err != nil {
				return err
			}
			if err = os.Remove(temp); err != nil {
				return err
			}
		} else if !os.IsNotExist(tempErr) {
			return tempErr
		}
		if _, err = artifactFileBytes(dest, upload.Size, upload.SHA256, upload.MIME); err != nil {
			return err
		}
		// Make the new object name and removal of the staging name durable
		// before recording a finalized object in SQLite.
		if err = artifactSyncDirs(a.objects, a.uploads); err != nil {
			return err
		}
		if err = a.verifyUploadLease(tx, principal, upload, auth); err != nil {
			return err
		}
		result = artifactObject{ID: upload.ArtifactID, ProjectID: upload.ProjectID, TaskID: upload.TaskID, AttemptID: upload.AttemptID, Candidate: upload.Candidate, Filename: upload.Filename, MIME: upload.MIME, Size: upload.Size, SHA256: upload.SHA256, Source: upload.Source, Uploader: upload.Uploader, Created: upload.Created, Status: "finalized"}
		if _, err = tx.Exec("INSERT INTO artifact_objects("+artifactColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)", result.ID, result.ProjectID, result.TaskID, result.AttemptID, result.Candidate, result.Filename, result.MIME, result.Size, result.SHA256, result.Source, result.Uploader, result.Created, result.Status); err != nil {
			return err
		}
		_, err = tx.Exec("UPDATE artifact_uploads SET status='finalized',received=size WHERE id=?", id)
		return err
	})
	return result, err
}

func (a *artifactStore) get(id string) (artifactObject, error) {
	return scanArtifact(a.service.Store.DB.QueryRow("SELECT "+artifactColumns+" FROM artifact_objects WHERE id=?", id))
}

func (a *artifactStore) read(item artifactObject) ([]byte, error) {
	if item.Status != "finalized" {
		return nil, errors.New("ARTIFACT_PURGED")
	}
	path, err := a.objectPath(item.ID)
	if err != nil {
		return nil, err
	}
	return artifactFileBytes(path, item.Size, item.SHA256, item.MIME)
}
