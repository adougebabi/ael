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
	runner := a.get("runner", "")
	if action != "register" && (runner == "" || strings.ContainsAny(runner, "/\\?#\r\n")) {
		return nil, core.Error("AGENT_REQUEST_INVALID", "provide --runner ID")
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
