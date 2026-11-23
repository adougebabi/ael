package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Secret-bearing requests stay in explicitly selected private files, never flags.
func readAgentRequest(path string, target any) error {
	f, err := os.Open(path)
	if err != nil {
		return core.Error("AGENT_REQUEST_REQUIRED", "provide a private JSON request file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
		return core.Error("AGENT_REQUEST_INVALID", "request must be a private regular file of at most 1 MiB")
	}
	decoder := json.NewDecoder(f)
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return core.Error("AGENT_REQUEST_INVALID", "invalid request JSON")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return core.Error("AGENT_REQUEST_INVALID", "trailing request data")
	}
	return nil
}

type agentUploadLease struct {
	RunnerID         string    `json:"runner_id"`
	TaskID           string    `json:"task_id"`
	AttemptID        string    `json:"attempt_id"`
	Token            string    `json:"token"`
	Fence            int       `json:"fence"`
	ExpectedRevision string    `json:"expected_revision"`
	LeaseUntil       time.Time `json:"lease_until"`
}

func remoteAgentCommand(cmd string, a args, config, project string, binding remote.Binding) (any, error) {
	action := strings.TrimPrefix(cmd, "runner ")
	switch action {
	case "run", "resume-result", "recover-claim":
		return nil, core.Error("REMOTE_MANAGED_UNSUPPORTED", "remote process hosting is retired; use Agent-owned tools and runner communication commands")
	}
	if action == "retry" {
		return retryAgentOperation(a, config, project, binding)
	}
	if action == "queue-list" {
		query := url.Values{}
		for _, key := range []string{"runner", "state", "task"} {
			if value := a.get(key, ""); value != "" {
				query.Set(key, value)
			}
		}
		path := "/queue"
		if len(query) > 0 {
			path += "?" + query.Encode()
		}
		response, err := agentCommunicationCall(config, project, binding, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		return remoteOutput(response), nil
	}
	runner := a.get("runner", "")
	if action == "queue-release" || action == "queue-transfer" {
		// The path names the current holder; the caller's own session runner
		// is not involved, so an admin or the receiving agent can act on it.
		runner = a.get("holder", "")
	}
	var lease agentUploadLease
	var leaseReceipt map[string]any
	if a.get("lease-file", "") != "" {
		if action != "heartbeat" && action != "progress" && action != "observe" && action != "submit" && action != "interrupt" {
			return nil, core.Error("AGENT_LEASE_INVALID", "--lease-file is for current attempt operations")
		}
		var err error
		lease, leaseReceipt, err = readAgentLease(a.get("lease-file", ""))
		if err != nil {
			return nil, err
		}
		if runner == "" {
			runner = lease.RunnerID
		}
	}
	if action != "register" && (runner == "" || strings.ContainsAny(runner, "/\\?#\r\n")) {
		return nil, core.Error("AGENT_REQUEST_INVALID", "provide --runner ID (or --holder for queue release/transfer)")
	}
	path := "/runners/" + url.PathEscape(runner)
	method := http.MethodPost
	var payload any
	switch action {
	case "show":
		method = http.MethodGet
	case "next":
		if a.get("parent", "") == "" {
			return nil, core.Error("AGENT_REQUEST_INVALID", "provide --parent")
		}
		method, path = http.MethodGet, path+"/next?parent="+url.QueryEscape(a.get("parent", ""))
	case "receipt":
		if a.get("idempotency-key", "") == "" {
			return nil, core.Error("AGENT_REQUEST_INVALID", "provide --idempotency-key")
		}
		method, path = http.MethodGet, path+"/operations/"+url.PathEscape(a.get("idempotency-key", ""))
	case "queue", "queue-release", "queue-transfer":
		body, err := queueRequest(a, action)
		if err != nil {
			return nil, err
		}
		path += "/" + action
		payload = body
	case "register", "claim", "heartbeat", "progress", "observe", "submit", "interrupt", "recover":
		if action == "claim" && a.get("out", "") == "" {
			return nil, core.Error("AGENT_REQUEST_INVALID", "claim requires --out NEW_PRIVATE_FILE for the lease; response output omits credentials")
		}
		var body map[string]any
		if err := readAgentRequest(a.get("file", ""), &body); err != nil {
			return nil, err
		}
		if body == nil {
			return nil, core.Error("AGENT_REQUEST_INVALID", "request must be an object")
		}
		if leaseReceipt != nil {
			if err := applyAgentLease(body, lease, action, runner); err != nil {
				return nil, err
			}
		}
		if action == "register" {
			path = "/runners"
			for _, key := range []string{"operation-id", "idempotency-key"} {
				if v := a.get(key, ""); v != "" {
					body[strings.ReplaceAll(key, "-", "_")] = v
				}
			}
		} else {
			path += "/" + action
		}
		payload = body
	default:
		return nil, core.Error("REMOTE_COMMAND_UNSUPPORTED", cmd)
	}
	if a.has("dry-run") {
		if action != "submit" || a.get("repository", "") == "" {
			return nil, core.Error("AGENT_REQUEST_INVALID", "dry-run requires runner submit --repository REPO")
		}
		profile, err := remote.LoadProfile(config, project, binding.Profile)
		if err != nil {
			return nil, err
		}
		plan, err := buildAgentDelivery(a, project, payload.(map[string]any), profile.Token)
		if err != nil {
			return nil, err
		}
		files := []map[string]any{}
		for _, artifact := range plan.Artifacts {
			files = append(files, map[string]any{"path": artifact.Path, "size": len(artifact.Data), "mime": artifact.MIME})
		}
		return map[string]any{"status": "ready", "validation": "local_candidate_and_artifact_bytes", "candidate_id": plan.Candidate.ID, "artifacts": files}, nil
	}
	output, err := reserveAgentClaimOutput(action, a.get("out", ""))
	if err != nil {
		return nil, err
	}
	if output != nil {
		defer output.Close()
	}
	var response map[string]any
	if method == http.MethodPost {
		response, err = runAgentOperation(a, config, project, binding, action, runner, payload.(map[string]any))
	} else {
		response, err = agentCommunicationCall(config, project, binding, method, path, nil)
	}
	if err == nil && action == "heartbeat" && leaseReceipt != nil {
		err = updateAgentLease(a.get("lease-file", ""), leaseReceipt, response)
	}
	return finishAgentOutput(response, err, output, a.get("out", ""))
}

func redactAgentSecrets(value any) {
	switch object := value.(type) {
	case map[string]any:
		for key, child := range object {
			if key == "token" || key == "lease_token" {
				delete(object, key)
			} else {
				redactAgentSecrets(child)
			}
		}
	case []any:
		for _, child := range object {
			redactAgentSecrets(child)
		}
	}
}

// queueRequest builds a queue marker request from flags (no secrets) or the
// same fields in a private --file. The CLI keeps a pending operation record so
// an unknown result is resolved by runner retry, never assumed accepted.
func queueRequest(a args, action string) (map[string]any, error) {
	body := map[string]any{}
	if a.get("file", "") != "" {
		if err := readAgentRequest(a.get("file", ""), &body); err != nil {
			return nil, err
		}
	}
	for flag, field := range map[string]string{"task": "task_id", "expected-version": "expected_version", "scope": "scope", "to": "to_runner", "to-scope": "to_scope", "reason": "reason", "operation-id": "operation_id", "idempotency-key": "idempotency_key"} {
		if value := a.get(flag, ""); value != "" {
			body[field] = value
		}
	}
	if action == "queue" && body["expected_version"] == nil {
		body["expected_version"] = "absent"
	}
	if body["operation_id"] == nil && body["idempotency_key"] != nil {
		body["operation_id"] = body["idempotency_key"]
	}
	for _, field := range []string{"task_id", "expected_version", "idempotency_key"} {
		if value, _ := body[field].(string); value == "" {
			return nil, core.Error("AGENT_REQUEST_INVALID", "provide --task, --expected-version and --idempotency-key")
		}
	}
	if action == "queue-transfer" && (body["to_runner"] == nil || body["reason"] == nil) {
		return nil, core.Error("AGENT_REQUEST_INVALID", "queue-transfer requires --to RUNNER and --reason")
	}
	return body, nil
}
