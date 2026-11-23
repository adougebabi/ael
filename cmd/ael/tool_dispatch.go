package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/store"
	"fmt"
	"maps"
	"net/http"
	"os"
	"strings"
)

func prepareRemoteToolCommand(cmd string, a args, raw []string, binding remote.Binding) (any, bool, error) {
	if os.Getenv("AEL_BINARY_PROBE") == "1" || strings.HasPrefix(cmd, "session ") || cmd == "update" || strings.HasPrefix(cmd, "release ") {
		return nil, false, nil
	}
	config, err := remoteConfigDir(a)
	if err != nil {
		return nil, true, err
	}
	runner := a.get("runner", "")
	if cmd == "runner register" {
		var request map[string]any
		if err = readAgentRequest(a.get("file", ""), &request); err != nil {
			return nil, true, err
		}
		runner, _ = request["id"].(string)
	}
	if cmd == "artifact upload" && a.get("lease-file", "") != "" {
		var lease agentUploadLease
		if err = readAgentRequest(a.get("lease-file", ""), &lease); err != nil {
			return nil, true, err
		}
		runner = lease.RunnerID
	}
	if cmd == "runner retry" {
		record, err := readAgentOperation(a.get("pending", ""))
		if err != nil {
			return nil, true, err
		}
		runner = record.Runner
		if record.Action == "register" {
			runner, _ = record.Payload["id"].(string)
		}
		if record.Upload != nil && record.Upload.Lease != nil {
			runner = record.Upload.Lease.RunnerID
		}
	}
	sessionID := a.get("session", os.Getenv("AEL_SESSION_ID"))
	if sessionID == "" && runner != "" {
		fallback := args{Flags: maps.Clone(a.Flags)}
		fallback.Flags["session"] = "runner-" + runner
		path, profile, root, err := toolSessionLocation(fallback, config, a.get("project", "."), binding)
		if err != nil {
			return nil, true, err
		}
		if existing, ok, err := openRunnerToolSession(path, profile, root, binding, runner); err != nil {
			return nil, true, err
		} else if ok {
			sessionID = existing.Session
		} else {
			sessionID = "runner-" + runner
		}
	}
	newResponsibility := cmd == "runner register" || cmd == "runner claim"
	if sessionID == "" || (cmd == "runner run" || cmd == "runner resume-result" || cmd == "runner recover-claim") {
		return nil, false, nil
	}
	options := args{Words: a.Words, Flags: maps.Clone(a.Flags)}
	options.Flags["session"] = sessionID
	if runner != "" {
		options.Flags["runner"] = runner
	}
	project := a.get("project", ".")
	path, profile, root, err := toolSessionLocation(options, config, project, binding)
	if err != nil {
		return nil, true, err
	}
	if selected := os.Getenv("AEL_PINNED_SESSION_FILE"); selected != "" && selected != path {
		return nil, true, core.Error("SESSION_BINDING_CONFLICT", "launcher session reference differs from current project or credential binding")
	}
	if newResponsibility {
		identity, err := remote.Call(config, root, binding, http.MethodGet, "", nil, nil)
		if err != nil {
			return nil, true, err
		}
		if identity["online"] != true || identity["authoritative"] != true {
			return nil, true, core.Error("REMOTE_UNAVAILABLE", "new responsibility requires live project authority")
		}
		role, _ := identity["role"].(string)
		if role != "executor" && role != "reviewer" && role != "admin" {
			return nil, true, core.Error("FORBIDDEN", "project role cannot register or claim responsibility")
		}
		value, err := remoteSessionCommand("session start", options, config, root, binding)
		if err != nil {
			return nil, true, err
		}
		result := value.(map[string]any)
		if compatible, reported := result["compatible_for_new_work"].(bool); reported && !compatible {
			return nil, true, core.Error("CLIENT_VERSION_INCOMPATIBLE", "end or hand off this tool session and prepare a compatible new session before another claim")
		}
		if preflight, ok := result["preflight"].(toolPreflight); ok {
			for _, warning := range preflight.Warnings {
				fmt.Fprintln(os.Stderr, "AEL update · "+warning)
			}
		}
	}
	session, err := readToolSession(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if err = validateToolSession(session, profile, root, binding); err != nil {
		return nil, true, err
	}
	runnerWrite := strings.HasPrefix(cmd, "runner ") && cmd != "runner show" && cmd != "runner next" && cmd != "runner receipt" && cmd != "runner queue-list"
	if runner != "" && session.Runner == "" && (runnerWrite || cmd == "artifact upload") {
		return nil, true, core.Error("SESSION_RUNNER_BINDING_REQUIRED", "bind this session to its own idle runner using session start before runner communication")
	}
	if runner != "" && session.Runner != "" && runner != session.Runner {
		return nil, true, core.Error("SESSION_BINDING_CONFLICT", "command runner differs from the pinned tool session")
	}
	current, err := os.Executable()
	if err != nil {
		return nil, true, err
	}
	currentHash, err := store.HashFile(current)
	if err != nil {
		return nil, true, err
	}
	if bootstrap.Version == session.Tool.Version.Version && bootstrap.Build == session.Tool.Version.Build && currentHash == session.Tool.SHA256 {
		return nil, false, nil
	}
	if os.Getenv("AEL_TOOL_DELEGATED") == session.Tool.SHA256 {
		return nil, true, core.Error("SESSION_CLI_MISMATCH", "selected executable did not identify as the pinned tool")
	}
	return toolDelegation{executable: session.Tool.Executable, arguments: append([]string{session.Tool.Executable}, raw...), session: session.Session, hash: session.Tool.SHA256}, true, nil
}

// main replaces the foreground CLI process. Tests can inspect this descriptor
// without executing a second program or replacing the test process.
type toolDelegation struct {
	executable string
	arguments  []string
	session    string
	hash       string
}
