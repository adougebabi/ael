package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"net/http"
	"net/url"
)

func remoteFindingCommand(cmd string, a args, config, project string, binding remote.Binding) (any, error) {
	switch cmd {
	case "finding report":
		var body map[string]any
		if err := readAgentRequest(a.get("file", ""), &body); err != nil {
			return nil, err
		}
		if body == nil {
			return nil, core.Error("FINDING_INVALID", "provide a structured report request")
		}
		for flag, field := range map[string]string{"task": "task_id", "context": "context_id", "expected-revision": "expected_revision", "operation-id": "operation_id", "idempotency-key": "idempotency_key"} {
			if value := a.get(flag, ""); value != "" {
				body[field] = value
			}
		}
		if body["context_id"] == nil || body["task_id"] == nil || body["findings"] == nil {
			return nil, core.Error("FINDING_INVALID", "report requires context_id, task_id and findings")
		}
		result, err := runAgentOperation(a, config, project, binding, "finding-report", "", body)
		if err != nil {
			return nil, err
		}
		return remoteOutput(result), nil
	case "finding decide", "finding repair", "finding revalidate":
		var body map[string]any
		if err := readAgentRequest(a.get("file", ""), &body); err != nil {
			return nil, err
		}
		if body == nil {
			return nil, core.Error("FINDING_MUTATION_INVALID", "provide a version-bound finding request")
		}
		for flag, field := range map[string]string{"finding": "finding_id", "expected-revision": "expected_revision", "operation-id": "operation_id", "idempotency-key": "idempotency_key"} {
			if value := a.get(flag, ""); value != "" {
				body[field] = value
			}
		}
		id, _ := body["finding_id"].(string)
		if id == "" {
			return nil, core.Error("FINDING_MUTATION_INVALID", "provide --finding ID or finding_id")
		}
		action := map[string]string{"finding decide": "finding-decide", "finding repair": "finding-repair", "finding revalidate": "finding-revalidate"}[cmd]
		result, err := runAgentOperation(a, config, project, binding, action, id, body)
		if err != nil {
			return nil, err
		}
		return remoteOutput(result), nil
	case "finding list":
		query := url.Values{}
		for flag, field := range map[string]string{"task": "task", "candidate": "candidate", "severity": "severity", "status": "status", "completeness": "completeness", "query": "query", "limit": "limit", "offset": "offset"} {
			if value := a.get(flag, ""); value != "" {
				query.Set(field, value)
			}
		}
		result, err := remote.Call(config, project, binding, http.MethodGet, "/findings?"+query.Encode(), nil, nil)
		return remoteOutput(result), err
	case "finding show":
		if a.get("finding", "") == "" {
			return nil, core.Error("FINDING_INVALID", "provide --finding ID")
		}
		result, err := remote.Call(config, project, binding, http.MethodGet, "/findings/"+url.PathEscape(a.get("finding", "")), nil, nil)
		return remoteOutput(result), err
	}
	return nil, core.Error("REMOTE_COMMAND_UNSUPPORTED", cmd)
}
