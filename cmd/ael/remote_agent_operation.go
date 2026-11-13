package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/securityfs"
	"ael/internal/store"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type agentOperationRecord struct {
	Schema       int                  `json:"schema"`
	Origin       string               `json:"origin"`
	Profile      string               `json:"profile"`
	ProjectID    string               `json:"project_id"`
	Checkout     string               `json:"checkout"`
	IdentityHash string               `json:"identity_hash"`
	Action       string               `json:"action"`
	Runner       string               `json:"runner"`
	PayloadHash  string               `json:"payload_hash"`
	RequestHash  string               `json:"request_hash"`
	Payload      map[string]any       `json:"payload"`
	Receipt      map[string]any       `json:"receipt,omitempty"`
	Upload       *agentRetainedUpload `json:"upload,omitempty"`
}

type agentRetainedUpload struct {
	Flags    map[string]string `json:"flags"`
	Filename string            `json:"filename"`
	Data     []byte            `json:"data"`
	Lease    *agentUploadLease `json:"lease,omitempty"`
}

func readAgentOperation(path string) (agentOperationRecord, error) {
	var record agentOperationRecord
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 20<<20 || securityfs.NoACL(path) != nil || securityfs.SingleLinkRegular(path) != nil {
		return record, core.Error("AGENT_OPERATION_INVALID", "operation record must be private, regular and unlinked")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return record, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&record) != nil || record.Schema != 1 || record.Payload == nil {
		return record, core.Error("AGENT_OPERATION_INVALID", "invalid operation record")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return record, core.Error("AGENT_OPERATION_INVALID", "trailing operation data")
	}
	digest, err := remote.RequestDigest(record.Payload)
	if err != nil {
		return record, err
	}
	if record.PayloadHash != digest {
		return record, core.Error("AGENT_OPERATION_INVALID", "retained payload checksum changed")
	}
	if record.Upload != nil {
		signature, _ := json.Marshal(struct {
			Flags            map[string]string
			Filename, SHA256 string
		}{record.Upload.Flags, record.Upload.Filename, store.Hash(record.Upload.Data)})
		if record.RequestHash != store.Hash(signature) {
			return record, core.Error("AGENT_OPERATION_INVALID", "retained upload bytes or metadata changed")
		}
	}
	return record, nil
}

func saveAgentOperation(path string, record agentOperationRecord) error {
	digest, err := remote.RequestDigest(record.Payload)
	if err != nil {
		return err
	}
	if record.PayloadHash != "" && record.PayloadHash != digest {
		return core.Error("AGENT_OPERATION_INVALID", "retained payload changed")
	}
	record.PayloadHash = digest
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return store.Atomic(path, raw, 0600)
}

func agentOperationLocation(config, project string, binding remote.Binding, action, runner, key string) (string, agentOperationRecord, error) {
	var record agentOperationRecord
	profile, err := remote.LoadProfile(config, project, binding.Profile)
	if err != nil {
		return "", record, err
	}
	checkout, err := filepath.Abs(project)
	if err == nil {
		checkout, err = filepath.EvalSymlinks(checkout)
	}
	if err != nil {
		return "", record, err
	}
	record = agentOperationRecord{Schema: 1, Origin: profile.URL, Profile: binding.Profile, ProjectID: binding.ProjectID, Checkout: checkout, IdentityHash: store.Hash([]byte(profile.Token)), Action: action, Runner: runner}
	dir, err := store.SafePath(config, "agent-operations")
	if err != nil {
		return "", record, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", record, err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || securityfs.NoACL(dir) != nil {
		return "", record, core.Error("AGENT_OPERATION_INVALID", "operation directory must be private")
	}
	scope, _ := json.Marshal([]string{record.Origin, record.Profile, record.ProjectID, record.Checkout, record.IdentityHash, action, runner, key})
	path, err := store.SafePath(dir, store.Hash(scope)+".json")
	return path, record, err
}

func lockAgentOperation(path string) (func(), error) {
	lockPath := path + ".lock"
	if _, err := os.Lstat(lockPath); err == nil {
		if securityfs.SingleLinkRegular(lockPath) != nil || securityfs.NoACL(lockPath) != nil {
			return nil, core.Error("AGENT_OPERATION_INVALID", "unsafe operation lock")
		}
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, core.Error("AGENT_OPERATION_BUSY", "another CLI is using this operation")
	}
	return func() { f.Close() }, nil
}

func agentCommunicationCall(config, project string, binding remote.Binding, method, path string, body any) (map[string]any, error) {
	profile, err := remote.LoadProfile(config, project, binding.Profile)
	if err != nil {
		return nil, err
	}
	client, err := remote.NewHTTPClient(profile)
	if err != nil {
		return nil, err
	}
	client.Timeout = 5 * time.Second
	if profile.CertificateAuthority != "" {
		defer client.CloseIdleConnections()
	}
	return remote.Call(config, project, binding, method, path, body, client)
}

func agentTransient(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "REMOTE_UNAVAILABLE") || strings.Contains(err.Error(), "REMOTE_RESPONSE_INVALID"))
}

func agentOperationEndpoint(record agentOperationRecord) string {
	if record.Action == "finding-report" {
		return "/findings"
	}
	if action, ok := map[string]string{"finding-decide": "decisions", "finding-repair": "repair", "finding-revalidate": "revalidations"}[record.Action]; ok {
		return "/findings/" + url.PathEscape(record.Runner) + "/" + action
	}
	if record.Action == "register" {
		return "/runners"
	}
	return "/runners/" + url.PathEscape(record.Runner) + "/" + record.Action
}

func lookupAgentOperation(config, project string, binding remote.Binding, record agentOperationRecord) (map[string]any, bool, error) {
	key, _ := record.Payload["idempotency_key"].(string)
	path := "/portable-operations/" + url.PathEscape(key)
	if record.Action == "submit" {
		path = "/runners/" + url.PathEscape(record.Runner) + "/operations/" + url.PathEscape(key)
	}
	result, err := agentCommunicationCall(config, project, binding, http.MethodGet, path, nil)
	if err != nil {
		return nil, false, err
	}
	found, ok := result["found"].(bool)
	if !ok {
		return nil, false, core.Error("REMOTE_RESPONSE_INVALID", "missing operation lookup result")
	}
	if !found {
		return nil, false, nil
	}
	receipt := result
	if record.Action != "submit" {
		receipt, ok = result["result"].(map[string]any)
		if !ok {
			return nil, false, core.Error("REMOTE_RESPONSE_INVALID", "missing operation receipt")
		}
		for _, field := range []string{"mode", "online", "authoritative", "observed_at"} {
			receipt[field] = result[field]
		}
	} else {
		delete(receipt, "found")
	}
	expectedDigest, err := remote.RequestDigest(record.Payload)
	if err != nil {
		return nil, false, err
	}
	digest, hasDigest := receipt["request_digest"].(string)
	if !hasDigest {
		// Older receipts remain recoverable through the existing server-side
		// idempotent replay, which validates the entire original request.
		replay, err := agentCommunicationCall(config, project, binding, http.MethodPost, agentOperationEndpoint(record), record.Payload)
		return replay, err == nil, err
	}
	if digest != expectedDigest {
		return nil, false, core.Error("IDEMPOTENCY_CONFLICT", "the accepted request differs from this retained operation")
	}
	if receipt["operation_id"] != record.Payload["operation_id"] {
		return nil, false, core.Error("IDEMPOTENCY_CONFLICT", "the original operation differs")
	}
	return receipt, true, nil
}

func validateAgentResponsibility(config, project string, binding remote.Binding, record agentOperationRecord) error {
	if record.Payload["attempt_id"] == nil || record.Action == "recover" {
		return nil
	}
	op, err := runnerOperation("reconnect")
	if err != nil {
		return err
	}
	body := map[string]any{"operation_id": op, "idempotency_key": op}
	for _, field := range []string{"task_id", "attempt_id", "token", "fence"} {
		body[field] = record.Payload[field]
	}
	_, err = agentCommunicationCall(config, project, binding, http.MethodPost, "/runners/"+url.PathEscape(record.Runner)+"/heartbeat", body)
	return err
}

func executeAgentOperation(config, project string, binding remote.Binding, path string, record agentOperationRecord, recovering bool) (map[string]any, error) {
	var response map[string]any
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)
		}
		if recovering {
			var found bool
			response, found, err = lookupAgentOperation(config, project, binding, record)
			if err == nil && found {
				break
			}
			if err != nil {
				if agentTransient(err) {
					continue
				}
				return nil, fmt.Errorf("%w; retained operation %s", err, path)
			}
			if err = validateAgentResponsibility(config, project, binding, record); err != nil {
				if agentTransient(err) {
					continue
				}
				return nil, fmt.Errorf("%w; stop the old responsibility, retain operation %s and read the current task before explicit recovery", err, path)
			}
		}
		response, err = agentCommunicationCall(config, project, binding, http.MethodPost, agentOperationEndpoint(record), record.Payload)
		if err == nil {
			break
		}
		if !agentTransient(err) {
			return nil, fmt.Errorf("%w; retained operation %s", err, path)
		}
		recovering = true
	}
	if err != nil {
		return nil, core.Error("AGENT_OPERATION_PENDING", "communication budget exhausted; retain drafts and run runner retry --pending "+path)
	}
	record.Receipt = response
	if err = saveAgentOperation(path, record); err != nil {
		return nil, core.Error("AGENT_OPERATION_PENDING", "server may have accepted; retry the retained operation "+path)
	}
	response["operation_file"] = path
	return response, nil
}

func runAgentOperation(a args, config, project string, binding remote.Binding, action, runner string, body map[string]any) (map[string]any, error) {
	key, _ := body["idempotency_key"].(string)
	if key == "" {
		return nil, core.Error("REMOTE_OPERATION_REQUIRED", "provide operation_id and idempotency_key in the request")
	}
	path, record, err := agentOperationLocation(config, project, binding, action, runner, key)
	if err != nil {
		return nil, err
	}
	unlock, err := lockAgentOperation(path)
	if err != nil {
		return nil, err
	}
	defer unlock()
	request, _ := json.Marshal(struct {
		Body       map[string]any
		Repository string
	}{body, a.get("repository", "")})
	hash := store.Hash(request)
	recovering := false
	if _, err = os.Lstat(path); err == nil {
		record, err = readAgentOperation(path)
		if err != nil {
			return nil, err
		}
		if record.RequestHash != hash {
			return nil, core.Error("IDEMPOTENCY_CONFLICT", "the operation request changed; the original record is retained")
		}
		recovering = true
	} else if !os.IsNotExist(err) {
		return nil, err
	} else {
		if action == "observe" && a.get("repository", "") != "" {
			if _, exists := body["candidate"]; exists {
				return nil, core.Error("AGENT_REQUEST_INVALID", "choose supplied candidate or --repository capture")
			}
			candidate, err := remote.CapturePortableCandidate(project, a.get("repository", ""))
			if err != nil {
				return nil, err
			}
			body["candidate"] = candidate
		}
		record.RequestHash, record.Payload = hash, body
		if err = saveAgentOperation(path, record); err != nil {
			return nil, err
		}
	}
	return executeAgentOperation(config, project, binding, path, record, recovering)
}

func retryAgentOperation(a args, config, project string, binding remote.Binding) (any, error) {
	path := a.get("pending", "")
	record, err := readAgentOperation(path)
	if err != nil {
		return nil, err
	}
	key, _ := record.Payload["idempotency_key"].(string)
	expected, scope, err := agentOperationLocation(config, project, binding, record.Action, record.Runner, key)
	if err != nil {
		return nil, err
	}
	if filepath.Clean(path) != expected || record.Origin != scope.Origin || record.Profile != scope.Profile || record.ProjectID != scope.ProjectID || record.Checkout != scope.Checkout || record.IdentityHash != scope.IdentityHash {
		return nil, core.Error("AGENT_OPERATION_SCOPE", "use the original checkout, profile, identity and server for this retained operation")
	}
	unlock, err := lockAgentOperation(path)
	if err != nil {
		return nil, err
	}
	defer unlock()
	output, err := reserveAgentClaimOutput(record.Action, a.get("out", ""))
	if err != nil {
		return nil, err
	}
	if output != nil {
		defer output.Close()
	}
	var result map[string]any
	if record.Upload != nil {
		result, err = executeAgentUpload(config, project, binding, path, record)
	} else {
		result, err = executeAgentOperation(config, project, binding, path, record, true)
	}
	return finishAgentOutput(result, err, output, a.get("out", ""))
}

func reserveAgentClaimOutput(action, path string) (*os.File, error) {
	if action != "claim" {
		return nil, nil
	}
	if path == "" {
		return nil, core.Error("AGENT_REQUEST_INVALID", "claim requires --out NEW_PRIVATE_FILE")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, core.Error("AGENT_OUTPUT_UNAVAILABLE", "choose a new private claim output file")
	}
	return f, nil
}

func finishAgentOutput(response map[string]any, err error, output *os.File, path string) (any, error) {
	if err != nil {
		if output != nil {
			os.Remove(path)
		}
		return nil, err
	}
	if output != nil {
		if err = json.NewEncoder(output).Encode(response); err == nil {
			err = output.Sync()
		}
		if err != nil {
			return nil, core.Error("AGENT_OUTPUT_FAILED", "claim may be accepted; retry the retained operation to a new private file")
		}
		response["lease_file"] = path
	}
	redactAgentSecrets(response)
	return remoteOutput(response), nil
}
