package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"encoding/json"
	"net/http"
	"net/url"
)

func remoteCandidateCommand(cmd string, a args, config, project string, binding remote.Binding) (any, error) {
	call := func(method, path string, body any) (map[string]any, error) {
		return remote.Call(config, project, binding, method, path, body, nil)
	}
	switch cmd {
	case "repository list":
		result, err := call(http.MethodGet, "/repositories", nil)
		return remoteOutput(result), err
	case "repository register":
		id, op, key := a.get("repository", ""), a.get("operation-id", ""), a.get("idempotency-key", "")
		if id == "" || op == "" || key == "" {
			return nil, core.Error("REMOTE_OPERATION_REQUIRED", "provide --repository, --operation-id and --idempotency-key")
		}
		result, err := call(http.MethodPost, "/repositories", map[string]string{"id": id, "expected_revision": "absent", "operation_id": op, "idempotency_key": key})
		return remoteOutput(result), err
	case "candidate capture":
		task, repository := a.get("task", ""), a.get("repository", "")
		checkout, workspace, runner := a.get("checkout", ""), a.get("workspace", ""), a.get("runner", "")
		revision, op, key := a.get("expected-revision", ""), a.get("operation-id", ""), a.get("idempotency-key", "")
		if task == "" || repository == "" || checkout == "" || workspace == "" || runner == "" || revision == "" || op == "" || key == "" {
			return nil, core.Error("REMOTE_OPERATION_REQUIRED", "provide task, repository, checkout, workspace, runner, expected revision and operation keys")
		}
		candidate, err := remote.CapturePortableCandidate(project, repository)
		if err != nil {
			return nil, err
		}
		payload := map[string]any{"task_id": task, "candidate": candidate, "checkout_id": checkout, "workspace_id": workspace, "runner_id": runner, "inputs_known": candidate.InputsKnown, "expected_revision": revision, "operation_id": op, "idempotency_key": key}
		result, err := call(http.MethodPost, "/candidates", payload)
		return remoteOutput(result), err
	case "candidate show":
		id := a.get("candidate", "")
		if id == "" {
			return nil, core.Error("REMOTE_REQUEST_INVALID", "provide --candidate")
		}
		result, err := call(http.MethodGet, "/candidates/"+url.PathEscape(id), nil)
		return remoteOutput(result), err
	case "candidate compare":
		ids := []string{a.get("before", ""), a.get("after", "")}
		var candidates [2]remote.PortableCandidate
		for i, id := range ids {
			if id == "" {
				return nil, core.Error("REMOTE_REQUEST_INVALID", "provide --before and --after candidate IDs")
			}
			result, err := call(http.MethodGet, "/candidates/"+url.PathEscape(id), nil)
			if err != nil {
				return nil, err
			}
			raw, err := json.Marshal(result)
			if err != nil || json.Unmarshal(raw, &candidates[i]) != nil || remote.ValidatePortableCandidate(candidates[i]) != nil {
				return nil, core.Error("REMOTE_RESPONSE_INVALID", "invalid candidate manifest")
			}
		}
		diff := remote.ComparePortableCandidates(candidates[0], candidates[1])
		return map[string]any{"mode": "remote", "online": true, "authoritative": true, "before": ids[0], "after": ids[1], "repository_changed": diff.RepositoryChanged, "commit_changed": diff.CommitChanged, "changed_paths": diff.Paths, "manifest_equal": candidates[0].ID == candidates[1].ID, "exactly_equal": diff.ExactlyEqual, "reason": "candidate manifests alone do not prove ignored inputs or check configuration"}, nil
	}
	return nil, core.Error("REMOTE_COMMAND_UNSUPPORTED", cmd)
}
