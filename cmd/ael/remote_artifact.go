package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/store"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const remoteArtifactLimit = 10 << 20
const remoteArtifactChunk = 256 << 10

func remoteArtifactCommand(cmd string, a args, config, project string, binding remote.Binding) (any, error) {
	call := func(method, path string, body any) (map[string]any, error) {
		return remote.Call(config, project, binding, method, path, body, nil)
	}
	id := a.get("artifact", "")
	switch cmd {
	case "artifact upload-status":
		key := a.get("idempotency-key", "")
		if key == "" {
			return nil, core.Error("REMOTE_REQUEST_INVALID", "provide the upload --idempotency-key")
		}
		result, err := call(http.MethodGet, "/artifacts/uploads/by-key/"+url.PathEscape(key), nil)
		return remoteOutput(result), err
	case "artifact list":
		if a.get("task", "") == "" {
			return nil, core.Error("REMOTE_REQUEST_INVALID", "provide --task")
		}
		result, err := call(http.MethodGet, "/tasks/"+url.PathEscape(a.get("task", ""))+"/artifacts", nil)
		return remoteOutput(result), err
	case "artifact show", "artifact download":
		if id == "" {
			return nil, core.Error("REMOTE_REQUEST_INVALID", "provide --artifact")
		}
		metadata, err := call(http.MethodGet, "/artifacts/"+url.PathEscape(id), nil)
		if err != nil {
			return nil, err
		}
		if cmd == "artifact show" {
			return remoteOutput(metadata), nil
		}
		if metadata["status"] != "finalized" {
			return nil, core.Error("ARTIFACT_UNAVAILABLE", "original is not available")
		}
		out := a.get("out", "")
		if out == "" {
			return nil, core.Error("REMOTE_REQUEST_INVALID", "provide --out for a new file")
		}
		data, err := remote.ArtifactContent(config, project, binding, id, nil)
		if err != nil {
			return nil, err
		}
		size, ok := metadata["size"].(json.Number)
		if !ok {
			return nil, core.Error("REMOTE_RESPONSE_INVALID", "missing artifact size")
		}
		n, err := size.Int64()
		if err != nil || n != int64(len(data)) || metadata["sha256"] != store.Hash(data) {
			return nil, core.Error("ARTIFACT_CONTENT_MISMATCH", "download did not match server metadata")
		}
		tmp, err := os.CreateTemp(filepath.Dir(out), ".ael-artifact-*")
		if err != nil {
			return nil, err
		}
		defer os.Remove(tmp.Name())
		_, writeErr := tmp.Write(data)
		if writeErr == nil {
			writeErr = tmp.Sync()
		}
		if closeErr := tmp.Close(); writeErr == nil {
			writeErr = closeErr
		}
		if writeErr != nil {
			return nil, writeErr
		}
		if err = os.Link(tmp.Name(), out); err != nil {
			return nil, core.Error("ARTIFACT_OUTPUT_EXISTS_OR_UNAVAILABLE", "choose a new output path")
		}
		return remoteOutput(map[string]any{"artifact_id": id, "sha256": store.Hash(data), "size": len(data), "output": out, "online": true, "authoritative": true}), nil
	case "artifact cancel":
		upload, op, key := a.get("upload", ""), a.get("operation-id", ""), a.get("idempotency-key", "")
		if upload == "" || op == "" || key == "" {
			return nil, core.Error("REMOTE_OPERATION_REQUIRED", "provide --upload, --operation-id and --idempotency-key from the upload")
		}
		result, err := call(http.MethodPost, "/artifacts/uploads/"+url.PathEscape(upload)+"/cancel", map[string]string{"operation_id": op, "idempotency_key": key})
		return remoteOutput(result), err
	case "artifact reference":
		task, op, key, revision := a.get("task", ""), a.get("operation-id", ""), a.get("idempotency-key", ""), a.get("expected-revision", "")
		if id == "" || task == "" || op == "" || key == "" || revision == "" {
			return nil, core.Error("REMOTE_OPERATION_REQUIRED", "provide --artifact, --task, --operation-id, --idempotency-key and --expected-revision")
		}
		body := map[string]string{"task_id": task, "purpose": a.get("purpose", "reference"), "candidate": a.get("candidate", ""), "expected_revision": revision, "operation_id": op, "idempotency_key": key}
		result, err := call(http.MethodPost, "/artifacts/"+url.PathEscape(id)+"/references", body)
		return remoteOutput(result), err
	case "artifact upload":
		task, path := a.get("task", ""), a.get("file", "")
		op, key, revision := a.get("operation-id", ""), a.get("idempotency-key", ""), a.get("expected-revision", "")
		if task == "" || path == "" || op == "" || key == "" || revision == "" {
			return nil, core.Error("REMOTE_OPERATION_REQUIRED", "provide --task, --file, --operation-id, --idempotency-key and --expected-revision")
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(f, remoteArtifactLimit+1))
		closeErr := f.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		var lease *runnerUploadLease
		if a.get("lease-file", "") != "" {
			var in agentUploadLease
			if err := readAgentRequest(a.get("lease-file", ""), &in); err != nil {
				return nil, err
			}
			if in.RunnerID == "" || in.Token == "" || in.Fence < 1 || in.TaskID != task || in.AttemptID != a.get("attempt", "") || in.ExpectedRevision != revision || !in.LeaseUntil.After(time.Now()) {
				return nil, core.Error("LEASE_LOST", "upload lease is missing, expired or belongs to another responsibility")
			}
			lease = &runnerUploadLease{RunnerID: in.RunnerID, Token: in.Token, Fence: in.Fence}
		}
		return retainAgentUpload(a, config, project, binding, filepath.Base(path), data, lease)
	}
	return nil, core.Error("REMOTE_COMMAND_UNSUPPORTED", cmd)
}

// Upload the exact caller-supplied bytes, so an already verified knowledge
// snapshot cannot be swapped by changing its local file between steps.
type runnerUploadLease struct {
	RunnerID, Token string
	Fence           int
	guard           *runnerLeaseGuard
}

func remoteUploadBytes(a args, config, project string, binding remote.Binding, filename string, data []byte, lease *runnerUploadLease) (remoteOutput, error) {
	if len(data) == 0 || len(data) > remoteArtifactLimit {
		return nil, core.Error("ARTIFACT_INVALID", "file must be between 1 byte and 10 MiB")
	}
	task, op, key, revision := a.get("task", ""), a.get("operation-id", ""), a.get("idempotency-key", ""), a.get("expected-revision", "")
	if task == "" || filename == "" || op == "" || key == "" || revision == "" {
		return nil, core.Error("REMOTE_OPERATION_REQUIRED", "provide task, filename, expected revision and operation keys")
	}
	mimeType := a.get("mime", "")
	if mimeType == "" {
		switch strings.ToLower(filepath.Ext(filename)) {
		case ".png":
			mimeType = "image/png"
		case ".jpg", ".jpeg":
			mimeType = "image/jpeg"
		case ".txt", ".md", ".log", ".json", ".csv":
			mimeType = "text/plain"
		case ".mp4":
			mimeType = "video/mp4"
		default:
			return nil, core.Error("ARTIFACT_INVALID", "unsupported filename extension")
		}
	}
	call := func(method, path string, body any) (map[string]any, error) {
		if lease != nil && lease.guard != nil {
			return runnerGuardedCall(config, project, binding, lease.guard, method, path, body)
		}
		return agentCommunicationCall(config, project, binding, method, path, body)
	}
	start := map[string]any{"operation_id": op, "idempotency_key": key, "expected_revision": revision, "task_id": task, "attempt_id": a.get("attempt", ""), "candidate": a.get("candidate", ""), "filename": filename, "mime": mimeType, "size": len(data), "sha256": store.Hash(data), "source": a.get("source", "CLI")}
	if a.get("context", "") != "" {
		start["context_id"] = a.get("context", "")
	}
	if lease != nil {
		start["runner_id"], start["lease_token"], start["fence"] = lease.RunnerID, lease.Token, lease.Fence
	}
	result, err := call(http.MethodPost, "/artifacts/uploads", start)
	if err != nil {
		return nil, err
	}
	upload, ok := result["upload_id"].(string)
	if !ok || upload == "" {
		return nil, core.Error("REMOTE_RESPONSE_INVALID", "missing upload ID")
	}
	if result["status"] != "open" && result["status"] != "finalized" {
		return nil, core.Error("UPLOAD_CONFLICT", "upload was cancelled or is being cleaned")
	}
	received, ok := result["received"].(json.Number)
	if !ok {
		return nil, core.Error("REMOTE_RESPONSE_INVALID", "missing upload offset")
	}
	offset, err := received.Int64()
	if err != nil || offset < 0 || offset > int64(len(data)) {
		return nil, core.Error("REMOTE_RESPONSE_INVALID", "invalid upload offset")
	}
	if result["status"] == "open" {
		for offset < int64(len(data)) {
			end := min(offset+remoteArtifactChunk, int64(len(data)))
			chunk := map[string]any{"offset": offset, "data": data[offset:end]}
			if lease != nil {
				chunk["runner_id"], chunk["lease_token"], chunk["fence"] = lease.RunnerID, lease.Token, lease.Fence
			}
			result, err = call(http.MethodPost, "/artifacts/uploads/"+url.PathEscape(upload)+"/chunks", chunk)
			if err != nil {
				return nil, fmt.Errorf("%w; upload %s: retry the same command and keys, or cancel the upload", err, upload)
			}
			next, ok := result["received"].(json.Number)
			if !ok {
				return nil, core.Error("REMOTE_RESPONSE_INVALID", "missing upload offset")
			}
			offset, err = next.Int64()
			if err != nil || offset != end {
				return nil, core.Error("REMOTE_RESPONSE_INVALID", "invalid upload offset")
			}
		}
	}
	finish := map[string]any{"operation_id": op, "idempotency_key": key}
	if lease != nil {
		finish["runner_id"], finish["lease_token"], finish["fence"] = lease.RunnerID, lease.Token, lease.Fence
	}
	result, err = call(http.MethodPost, "/artifacts/uploads/"+url.PathEscape(upload)+"/finalize", finish)
	if err != nil {
		return nil, fmt.Errorf("%w; upload %s: retry the same command and keys, or cancel the upload", err, upload)
	}
	return remoteOutput(result), nil
}
