package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type remoteOutput map[string]any

func remoteConfigDir(a args) (string, error) {
	if value := a.get("config-dir", ""); value != "" {
		if !filepath.IsAbs(value) {
			return "", core.Error("REMOTE_CONFIG_DIR_REQUIRED", "use an absolute --config-dir")
		}
		return value, nil
	}
	if value := os.Getenv("AEL_REMOTE_CONFIG_DIR"); value != "" {
		if !filepath.IsAbs(value) {
			return "", core.Error("REMOTE_CONFIG_DIR_REQUIRED", "AEL_REMOTE_CONFIG_DIR must be absolute")
		}
		return value, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" || !filepath.IsAbs(dir) {
		return "", core.Error("REMOTE_CONFIG_DIR_REQUIRED", "cannot find a private user configuration directory")
	}
	return filepath.Join(dir, "ael", "remote"), nil
}

func remoteControl(cmd string, a args) (any, error) {
	allowed := map[string]bool{"project": true, "config-dir": true, "json": true, "verbose": true, "no-color": true}
	if cmd == "server profile add" {
		allowed["name"], allowed["url"], allowed["token-stdin"], allowed["ca-file"] = true, true, true, true
	} else {
		allowed["profile"], allowed["remote-project"], allowed["install-entry"] = true, true, true
	}
	for flag, value := range a.Flags {
		if !allowed[flag] {
			return nil, core.Error("UNKNOWN_FLAG", flag)
		}
		if booleanFlag(flag) && value != "true" && value != "false" {
			return nil, core.Error("INVALID_FLAG_VALUE", flag)
		}
		if !booleanFlag(flag) && value == "true" {
			return nil, core.Error("FLAG_VALUE_REQUIRED", flag)
		}
	}
	config, err := remoteConfigDir(a)
	if err != nil {
		return nil, err
	}
	project := a.get("project", ".")
	if cmd == "server profile add" {
		if !a.has("token-stdin") {
			return nil, core.Error("REMOTE_TOKEN_REQUIRED", "provide --token-stdin")
		}
		raw, err := io.ReadAll(io.LimitReader(os.Stdin, 514))
		if err != nil || len(raw) > 513 {
			return nil, core.Error("REMOTE_TOKEN_REQUIRED", "invalid token input")
		}
		token := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
		authority := ""
		if a.get("ca-file", "") != "" {
			authority, err = remote.ReadAuthority(a.get("ca-file", ""))
			if err != nil {
				return nil, err
			}
		}
		if err = remote.SaveProfileWithAuthority(config, project, a.get("name", ""), a.get("url", ""), token, authority); err != nil {
			return nil, err
		}
		return map[string]string{"mode": "remote", "profile": a.get("name", ""), "status": "saved"}, nil
	}
	binding, err := remote.Connect(config, project, a.get("profile", ""), a.get("remote-project", ""), nil)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"mode": "remote", "online": true, "profile": binding.Profile, "project_id": binding.ProjectID}
	if a.has("install-entry") {
		entry, err := installRemoteEntry(project, false, false)
		if err != nil {
			return nil, core.Error("REMOTE_ENTRY_SETUP_PENDING", "project binding saved; complete the entry update after resolving "+err.Error())
		}
		result["entry"] = entry
	}
	return result, nil
}

func remoteCommand(cmd string, a args, binding remote.Binding) (any, error) {
	if err := remote.CheckClientProtocols(binding); err != nil {
		return nil, err
	}
	flags := map[string][]string{
		"session start":      {"session", "runner", "prefix"},
		"session show":       {"session"},
		"session end":        {"session"},
		"recovery save":      {"session", "runner", "parent", "task", "file"},
		"recovery check":     {"session"},
		"finding decide":     {"file", "finding", "expected-revision", "operation-id", "idempotency-key"},
		"finding repair":     {"file", "finding", "expected-revision", "operation-id", "idempotency-key"},
		"finding revalidate": {"file", "finding", "expected-revision", "operation-id", "idempotency-key"},
		"finding report":     {"file", "task", "context", "expected-revision", "operation-id", "idempotency-key"},
		"finding list":       {"task", "candidate", "severity", "status", "completeness", "query", "limit", "offset"},
		"finding show":       {"finding"},
		"doctor":             {}, "task list": {"parent", "limit", "offset", "days", "query", "board"},
		"task show": {"task"}, "task plan": {"task"}, "next": {"task"}, "task tree": {"task"}, "task progress": {"task"},
		"task integrate":        {"task", "context", "impact-artifact", "expected-revision", "operation-id", "idempotency-key"},
		"task reconcile-import": {"task", "context", "reason", "start", "expected-revision", "operation-id", "idempotency-key"},
		"task review-failure":   {"task", "file", "expected-revision", "operation-id", "idempotency-key"},
		"operation show":        {"idempotency-key"},
		"artifact upload":       {"context", "lease-file", "task", "file", "mime", "source", "attempt", "candidate", "operation-id", "idempotency-key", "expected-revision"},
		"artifact list":         {"task"}, "artifact show": {"artifact"}, "artifact download": {"artifact", "out"},
		"artifact cancel":        {"upload", "operation-id", "idempotency-key"},
		"artifact upload-status": {"idempotency-key"},
		"artifact reference":     {"artifact", "task", "purpose", "candidate", "expected-revision", "operation-id", "idempotency-key"},
		"repository register":    {"repository", "operation-id", "idempotency-key"}, "repository list": {},
		"candidate capture": {"task", "repository", "checkout", "workspace", "runner", "expected-revision", "operation-id", "idempotency-key"},
		"candidate show":    {"candidate"}, "candidate compare": {"before", "after"},
		"knowledge index": {}, "context publish": {"task", "observation", "share-source", "expected-revision", "operation-id", "idempotency-key"},
		"context show":         {"context", "task"},
		"context record-check": {"context", "task", "check", "artifact", "expected-revision", "operation-id", "idempotency-key"},
		"runner retry":         {"pending", "out"},
		"runner recover":       {"runner", "file"},
		"runner register":      {"file", "operation-id", "idempotency-key"},
		"runner claim":         {"runner", "file", "out"},
		"runner heartbeat":     {"runner", "file", "lease-file"}, "runner progress": {"runner", "file", "lease-file"},
		"runner observe": {"runner", "file", "repository", "lease-file"}, "runner submit": {"runner", "file", "lease-file", "repository", "artifacts-dir", "dry-run", "status", "summary", "method", "exit-code", "inputs", "candidate", "operation-id", "idempotency-key"},
		"runner interrupt": {"runner", "file", "lease-file"}, "runner show": {"runner"},
		"runner next": {"runner", "parent"}, "runner receipt": {"runner", "idempotency-key"},
		"runner run":           {"file", "task", "parent"},
		"runner resume-result": {"file", "pending"},
		"runner recover-claim": {"file", "claim"},
		"task create":          {"file", "operation-id", "idempotency-key", "expected-revision"},
		"task edit":            {"task", "file", "reason", "operation-id", "idempotency-key", "expected-revision"},
		"task pause":           {"task", "reason", "operation-id", "idempotency-key", "expected-revision"},
		"task cancel":          {"task", "reason", "operation-id", "idempotency-key", "expected-revision"},
		"resume":               {"task", "return-to", "ack-stopped", "operation-id", "idempotency-key", "expected-revision"},
		"task link":            {"task", "parent", "required", "milestone", "operation-id", "idempotency-key", "expected-revision"},
		"plan prepare":         {"task", "file", "operation-id", "idempotency-key", "expected-revision"},
		"plan preview":         {"file"}, "plan apply": {"file", "digest", "operation-id", "idempotency-key", "expected-revision"},
		"plan abandon": {"task", "plan-id", "reason", "operation-id", "idempotency-key", "expected-revision"},
	}
	commandFlags, supported := flags[cmd]
	if !supported {
		return nil, core.Error("REMOTE_COMMAND_UNSUPPORTED", "this command is not available for the remote authority; local fallback is disabled")
	}
	allowed := map[string]bool{"session": true, "project": true, "config-dir": true, "json": true, "verbose": true, "no-color": true}
	for _, flag := range commandFlags {
		allowed[flag] = true
	}
	for flag, value := range a.Flags {
		if !allowed[flag] {
			return nil, core.Error("REMOTE_UNSUPPORTED_FLAG", flag)
		}
		if booleanFlag(flag) && value != "true" && value != "false" {
			return nil, core.Error("INVALID_FLAG_VALUE", flag)
		}
		if !booleanFlag(flag) && value == "true" {
			return nil, core.Error("FLAG_VALUE_REQUIRED", flag)
		}
	}
	config, err := remoteConfigDir(a)
	if err != nil {
		return nil, err
	}
	project, id := a.get("project", "."), a.get("task", "")
	if strings.HasPrefix(cmd, "session ") {
		return remoteSessionCommand(cmd, a, config, project, binding)
	}
	if strings.HasPrefix(cmd, "recovery ") {
		return recoveryCommand(cmd, a, config, project, binding)
	}
	if strings.HasPrefix(cmd, "finding ") {
		return remoteFindingCommand(cmd, a, config, project, binding)
	}
	if strings.HasPrefix(cmd, "artifact ") {
		return remoteArtifactCommand(cmd, a, config, project, binding)
	}
	if strings.HasPrefix(cmd, "repository ") || strings.HasPrefix(cmd, "candidate ") {
		return remoteCandidateCommand(cmd, a, config, project, binding)
	}
	if cmd == "knowledge index" || strings.HasPrefix(cmd, "context ") {
		return remoteContextCommand(cmd, a, config, project, binding)
	}
	if strings.HasPrefix(cmd, "runner ") {
		return remoteRunnerCommand(cmd, a, config, project, binding)
	}
	method, path := http.MethodGet, ""
	var payload any
	switch cmd {
	case "doctor":
		path = ""
	case "task list":
		path = "/tasks"
		query := url.Values{}
		for _, key := range []string{"parent", "limit", "offset", "days", "query", "board"} {
			if value := a.get(key, ""); value != "" {
				query.Set(key, value)
			}
		}
		if len(query) > 0 {
			path += "?" + query.Encode()
		}
	case "task show", "task plan":
		path = "/tasks/" + url.PathEscape(id)
	case "next":
		path = "/tasks/" + url.PathEscape(id) + "/next"
	case "task tree", "task progress":
		path = "/tasks/" + url.PathEscape(id) + "/tree"
	case "operation show":
		path = "/operations/" + url.PathEscape(a.get("idempotency-key", ""))
	case "plan prepare", "plan preview", "plan apply", "plan abandon":
		method = http.MethodPost
		inputValue := map[string]any{}
		parent := id
		if cmd == "plan prepare" {
			var planInput core.PlanInput
			if err = input(a.get("file", ""), &planInput); err != nil {
				return nil, err
			}
			inputValue["input"] = planInput
		} else if cmd == "plan abandon" {
			inputValue["plan_id"], inputValue["reason"] = a.get("plan-id", ""), a.get("reason", "")
		} else {
			var proposal core.PlanProposal
			if err = input(a.get("file", ""), &proposal); err != nil {
				return nil, err
			}
			parent = proposal.Parent
			inputValue["proposal"] = proposal
			if cmd == "plan apply" {
				inputValue["digest"] = a.get("digest", "")
			}
		}
		if parent == "" {
			return nil, core.Error("REMOTE_REQUEST_INVALID", "supply the parent task")
		}
		if cmd != "plan preview" {
			op, key, expected := a.get("operation-id", ""), a.get("idempotency-key", ""), a.get("expected-revision", "")
			if op == "" || key == "" || expected == "" {
				return nil, core.Error("REMOTE_OPERATION_REQUIRED", "provide --operation-id, --idempotency-key and --expected-revision")
			}
			inputValue["operation_id"], inputValue["idempotency_key"], inputValue["expected_revision"] = op, key, expected
		}
		path = "/tasks/" + url.PathEscape(parent) + "/plan/" + strings.TrimPrefix(cmd, "plan ")
		payload = inputValue
	case "task integrate":
		method = http.MethodPost
		path = "/tasks/" + url.PathEscape(id) + "/integrate"
		payload = map[string]any{"context_id": a.get("context", ""), "impact_artifact": a.get("impact-artifact", ""), "expected_revision": a.get("expected-revision", ""), "operation_id": a.get("operation-id", ""), "idempotency_key": a.get("idempotency-key", "")}
	case "task review-failure":
		var review core.FailureReview
		if err = input(a.get("file", ""), &review); err != nil {
			return nil, err
		}
		method = http.MethodPost
		path = "/tasks/" + url.PathEscape(id) + "/review-failure"
		payload = map[string]any{"review": review, "expected_revision": a.get("expected-revision", ""), "operation_id": a.get("operation-id", ""), "idempotency_key": a.get("idempotency-key", "")}
	case "task create", "task edit", "task pause", "task cancel", "resume", "task link", "task reconcile-import":
		method = http.MethodPost
		expected := a.get("expected-revision", "")
		if cmd == "task create" && expected == "" {
			expected = "absent"
		}
		op, key := a.get("operation-id", ""), a.get("idempotency-key", "")
		if op == "" || key == "" || expected == "" {
			return nil, core.Error("REMOTE_OPERATION_REQUIRED", "provide --operation-id, --idempotency-key and expected revision")
		}
		mutation := map[string]any{"operation_id": op, "idempotency_key": key, "expected_revision": expected}
		switch cmd {
		case "task create", "task edit":
			var definition core.Definition
			if err = input(a.get("file", ""), &definition); err != nil {
				return nil, err
			}
			mutation["definition"] = definition
			if cmd == "task create" {
				path = "/tasks"
			} else {
				path = "/tasks/" + url.PathEscape(id) + "/edit"
				mutation["reason"] = a.get("reason", "")
			}
		case "task pause", "task cancel":
			path = "/tasks/" + url.PathEscape(id) + "/" + strings.TrimPrefix(cmd, "task ")
			mutation["reason"] = a.get("reason", "")
		case "task reconcile-import":
			path = "/tasks/" + url.PathEscape(id) + "/reconcile-import"
			mutation["context_id"], mutation["reason"], mutation["start"] = a.get("context", ""), a.get("reason", ""), a.get("start", "")
		case "resume":
			path = "/tasks/" + url.PathEscape(id) + "/resume"
			mutation["return_to"], mutation["ack_stopped"] = a.get("return-to", ""), a.has("ack-stopped")
		case "task link":
			path = "/tasks/" + url.PathEscape(id) + "/link"
			required := a.get("required", "true")
			if required != "true" && required != "false" {
				return nil, core.Error("INVALID_FLAG_VALUE", "required")
			}
			mutation["parent"], mutation["required"], mutation["milestone"] = a.get("parent", ""), required == "true", a.get("milestone", "")
		}
		payload = mutation
	}
	if (path == "" && cmd != "doctor") || strings.Contains(path, "/tasks//") || strings.HasSuffix(path, "/tasks/") || strings.HasSuffix(path, "/operations/") {
		return nil, core.Error("REMOTE_REQUEST_INVALID", "supply the task or operation key")
	}
	result, err := remote.Call(config, project, binding, method, path, payload, nil)
	if err != nil {
		if cmd == "task reconcile-import" {
			for code, detail := range map[string]string{
				"IMPORT_SOURCE_UNKNOWN":   "原归档缺少可信的暂停/未决决定来源。请 reviewer 在任务页核对当前恢复范围；可审阅一批明确范围后使用准确版本确认，勿逐项盲点恢复。",
				"IMPORT_PAUSE_PROTECTED":  "存在人工暂停、未决决定、失败或其他执行责任。读取 task show 的 blocker/confirmation/attempt，按该具体事项处理；其他责任使用 runner recover。",
				"IMPORT_CONTEXT_REQUIRED": "先核对当前工程候选和现行依据，publish/show 当前身份、准确任务版本的 context，并填写核对结果与继续起点。",
			} {
				if strings.Contains(err.Error(), code) {
					return nil, core.Error(code, detail)
				}
			}
		}
		if method == http.MethodPost && strings.Contains(err.Error(), "REMOTE_UNAVAILABLE") {
			return nil, core.Error("REMOTE_UNAVAILABLE", "outcome may be unknown; query operation show with the same --idempotency-key before retrying")
		}
		return nil, err
	}
	if cmd == "plan prepare" || cmd == "plan apply" || cmd == "plan abandon" {
		if details, ok := result["result"].(map[string]any); ok {
			for key, value := range details {
				if _, exists := result[key]; !exists {
					result[key] = value
				}
			}
		}
	}
	if cmd == "task reconcile-import" {
		next, nextErr := remote.Call(config, project, binding, http.MethodGet, "/tasks/"+url.PathEscape(id)+"/next", nil, nil)
		if nextErr != nil {
			return nil, nextErr
		}
		result["next"] = next
		result["conclusion"] = "历史核对已记录；导入专属暂停已解除，按 next_action 继续领取。"
	}
	return remoteOutput(result), nil
}
