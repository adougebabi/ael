package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/store"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func retainAgentUpload(a args, config, project string, binding remote.Binding, filename string, data []byte, lease *runnerUploadLease) (remoteOutput, error) {
	key := a.get("idempotency-key", "")
	runner := ""
	if lease != nil {
		runner = lease.RunnerID
	}
	path, record, err := agentOperationLocation(config, project, binding, "artifact-upload", runner, key)
	if err != nil {
		return nil, err
	}
	unlock, err := lockAgentOperation(path)
	if err != nil {
		return nil, err
	}
	defer unlock()
	flags := map[string]string{}
	for _, name := range []string{"context", "task", "attempt", "candidate", "mime", "source", "import-id", "operation-id", "idempotency-key", "expected-revision"} {
		if value := a.get(name, ""); value != "" {
			flags[name] = value
		}
	}
	signature, _ := json.Marshal(struct {
		Flags            map[string]string
		Filename, SHA256 string
	}{flags, filename, store.Hash(data)})
	hash := store.Hash(signature)
	if _, err = os.Lstat(path); err == nil {
		record, err = readAgentOperation(path)
		if err != nil {
			return nil, err
		}
		if record.RequestHash != hash {
			return nil, core.Error("IDEMPOTENCY_CONFLICT", "upload bytes or metadata changed; retry the retained operation to use its original bytes")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	} else {
		record.RequestHash = hash
		record.Payload = map[string]any{"task_id": a.get("task", ""), "attempt_id": a.get("attempt", ""), "operation_id": a.get("operation-id", ""), "idempotency_key": key}
		record.Upload = &agentRetainedUpload{Flags: flags, Filename: filename, Data: data}
		if lease != nil {
			record.Payload["token"], record.Payload["fence"] = lease.Token, lease.Fence
			record.Upload.Lease = &agentUploadLease{RunnerID: lease.RunnerID, Token: lease.Token, Fence: lease.Fence}
		} else {
			delete(record.Payload, "attempt_id")
		}
		if err = saveAgentOperation(path, record); err != nil {
			return nil, err
		}
	}
	result, err := executeAgentUpload(config, project, binding, path, record)
	return remoteOutput(result), err
}

func executeAgentUpload(config, project string, binding remote.Binding, path string, record agentOperationRecord) (map[string]any, error) {
	if record.Upload == nil || len(record.Upload.Data) == 0 || len(record.Upload.Data) > remoteArtifactLimit {
		return nil, core.Error("AGENT_OPERATION_INVALID", "missing retained upload bytes")
	}
	var result map[string]any
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)
		}
		key, _ := record.Payload["idempotency_key"].(string)
		status, lookupErr := agentCommunicationCall(config, project, binding, http.MethodGet, "/artifacts/uploads/by-key/"+url.PathEscape(key), nil)
		if lookupErr == nil && status["status"] == "finalized" {
			var lease *runnerUploadLease
			if in := record.Upload.Lease; in != nil {
				lease = &runnerUploadLease{RunnerID: in.RunnerID, Token: in.Token, Fence: in.Fence}
			}
			result, err = remoteUploadBytes(args{Flags: record.Upload.Flags}, config, project, binding, record.Upload.Filename, record.Upload.Data, lease)
			if err == nil {
				break
			}
		} else {
			if lookupErr != nil && agentTransient(lookupErr) {
				err = lookupErr
				continue
			}
			if lookupErr != nil && !strings.Contains(lookupErr.Error(), "UPLOAD_NOT_FOUND") && !strings.Contains(lookupErr.Error(), "ARTIFACT_NOT_FOUND") {
				return nil, lookupErr
			}
			if err = validateAgentResponsibility(config, project, binding, record); err != nil {
				if agentTransient(err) {
					continue
				}
				return nil, fmt.Errorf("%w; retain operation %s and stop the old responsibility before recovery", err, path)
			}
			var lease *runnerUploadLease
			if record.Upload.Lease != nil {
				in := record.Upload.Lease
				lease = &runnerUploadLease{RunnerID: in.RunnerID, Token: in.Token, Fence: in.Fence}
			}
			result, err = remoteUploadBytes(args{Flags: record.Upload.Flags}, config, project, binding, record.Upload.Filename, record.Upload.Data, lease)
			if err == nil {
				break
			}
		}
		if !agentTransient(err) {
			return nil, fmt.Errorf("%w; retained upload %s", err, path)
		}
	}
	if err != nil {
		return nil, core.Error("AGENT_OPERATION_PENDING", "upload communication budget exhausted; run runner retry --pending "+path)
	}
	record.Receipt = result
	if err = saveAgentOperation(path, record); err != nil {
		return nil, core.Error("AGENT_OPERATION_PENDING", "upload may be accepted; retry "+path)
	}
	result["operation_file"] = path
	return result, nil
}
