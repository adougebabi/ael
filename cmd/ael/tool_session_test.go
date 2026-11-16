package main

import (
	"ael/internal/bootstrap"
	"ael/internal/remote"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestToolSessionFreezesRulesAndChecksLiveCompatibility(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(t.TempDir(), "config")
	t.Setenv("AEL_REMOTE_CONFIG_DIR", config)
	t.Setenv("AEL_CLIENT_CONFIG_DIR", t.TempDir())
	t.Setenv("AEL_INSTALL_PREFIX", filepath.Join(t.TempDir(), "unused-install"))
	var minimum atomic.Value
	minimum.Store("0.1.22")
	var available, active, missingRunner atomic.Bool
	available.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !available.Load() {
			w.WriteHeader(503)
			json.NewEncoder(w).Encode(map[string]any{"code": "REMOTE_UNAVAILABLE"})
			return
		}
		switch r.URL.Path {
		case "/api/v1/meta":
			json.NewEncoder(w).Encode(map[string]any{"mode": "server", "protocol": 1, "skill_protocol": 1, "minimum_client_version": minimum.Load(), "capabilities": []string{"remote_tasks"}})
		case "/api/v1/projects/shared":
			json.NewEncoder(w).Encode(map[string]any{"id": "shared", "role": "executor"})
		case "/api/v1/projects/shared/runners/not-registered", "/api/v1/projects/shared/runners/agent-b":
			w.WriteHeader(404)
			json.NewEncoder(w).Encode(map[string]any{"code": "RUNNER_NOT_FOUND"})
		case "/api/v1/projects/shared/runners/agent":
			if missingRunner.Load() {
				w.WriteHeader(404)
				json.NewEncoder(w).Encode(map[string]any{"code": "RUNNER_NOT_FOUND"})
				return
			}
			assignments := []any{}
			if active.Load() {
				assignments = append(assignments, map[string]any{"responsibility_state": "active"})
			}
			json.NewEncoder(w).Encode(map[string]any{"assignments": assignments})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	if err := remote.SaveProfile(config, root, "central", server.URL, "fixture-session-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Connect(config, root, "central", "shared", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := installRemoteEntry(root, false, false); err != nil {
		t.Fatal(err)
	}
	binding, _, _ := remote.ReadBinding(root)
	cli := filepath.Join(t.TempDir(), "ael")
	if output, err := exec.Command("go", "build", "-o", cli, ".").CombinedOutput(); err != nil {
		t.Fatalf("build actual CLI %v %s", err, output)
	}
	invoke := func(command string, options args) (any, error) {
		arguments := append(strings.Fields(command), "--project", root, "--config-dir", config, "--session", options.get("session", ""), "--json")
		if command == "session start" && options.get("runner", "") != "" {
			arguments = append(arguments, "--runner", options.get("runner", ""))
		}
		process := exec.Command(cli, arguments...)
		var stderr bytes.Buffer
		process.Stderr = &stderr
		raw, err := process.Output()
		if err != nil {
			return nil, fmt.Errorf("%s", stderr.String())
		}
		var result map[string]any
		if err = json.Unmarshal(raw, &result); err != nil {
			return nil, err
		}
		if body, ok := result["session"]; ok {
			data, _ := json.Marshal(body)
			var session toolSession
			if err = json.Unmarshal(data, &session); err != nil {
				return nil, err
			}
			result["session"] = session
		}
		return result, nil
	}
	options := args{Flags: map[string]string{"session": "session-a", "runner": "agent"}}
	value, err := invoke("session start", options)
	if err != nil {
		t.Fatal(err)
	}
	first := value.(map[string]any)["session"].(toolSession)
	if first.SkillVersion != bootstrap.Version || first.Tool.Version.Version != bootstrap.Version {
		t.Fatalf("incorrect versions %+v", first)
	}
	if first.Tool.Executable == cli {
		t.Fatal("standalone CLI original path was not cached")
	}
	backupCLI := cli + ".saved"
	if err = os.Rename(cli, backupCLI); err != nil {
		t.Fatal(err)
	}
	cachedCommand := exec.Command(filepath.Join(root, "ael"), "version", "--session", "session-a", "--json")
	cachedCommand.Env = append(os.Environ(), "AEL_CLI="+cli)
	cachedOutput, cachedErr := cachedCommand.CombinedOutput()
	if err = os.Rename(backupCLI, cli); err != nil {
		t.Fatal(err)
	}
	if cachedErr != nil {
		t.Fatalf("old session depended on replaced original %v %s", cachedErr, cachedOutput)
	}
	unbound := args{Flags: map[string]string{"session": "unbound"}}
	if _, err = invoke("session start", unbound); err != nil {
		t.Fatal(err)
	}
	active.Store(true)
	unbound.Flags["runner"] = "agent"
	if _, err = invoke("session start", unbound); err == nil || !strings.Contains(err.Error(), "SESSION_ACTIVE_RUNNER") {
		t.Fatalf("active runner rebound %v", err)
	}
	wrong := exec.Command(cli, "runner", "heartbeat", "--runner", "agent", "--session", "unbound", "--project", root, "--config-dir", config, "--json")
	wrongOutput, wrongErr := wrong.CombinedOutput()
	if wrongErr == nil || !strings.Contains(string(wrongOutput), "SESSION_RUNNER_BINDING_REQUIRED") {
		t.Fatalf("unbound session borrowed responsibility %v %s", wrongErr, wrongOutput)
	}
	active.Store(false)
	missingRunner.Store(true)
	unregistered := args{Flags: map[string]string{"session": "unregistered", "runner": "not-registered"}}
	if _, err = invoke("session start", unregistered); err != nil {
		t.Fatal(err)
	}
	if _, err = invoke("session end", unregistered); err != nil {
		t.Fatal("unregistered runner session cannot end", err)
	}
	missingRunner.Store(false)
	skill, err := os.ReadFile(first.SkillPath)
	if err != nil {
		t.Fatal(err)
	}
	projectSkill := filepath.Join(root, ".agents/skills/ael-work/SKILL.md")
	os.WriteFile(projectSkill, []byte("user-edited project rules"), 0644)
	value, err = invoke("session start", options)
	if err != nil {
		t.Fatal("existing session changed with project assets", err)
	}
	if current, _ := os.ReadFile(first.SkillPath); string(current) != string(skill) {
		t.Fatal("frozen Skill was overwritten")
	}
	options.Flags["session"] = "session-b"
	options.Flags["runner"] = "agent-b"
	if _, err := invoke("session start", options); err == nil || !strings.Contains(err.Error(), "ASSET_CONFLICT") {
		t.Fatalf("new session accepted modified assets %v", err)
	}
	os.WriteFile(projectSkill, skill, 0644)
	minimum.Store("99.0.0")
	if _, err := invoke("session start", options); err == nil || !strings.Contains(err.Error(), "CLIENT_VERSION_INCOMPATIBLE") {
		t.Fatalf("incompatible new work accepted %v", err)
	}
	options.Flags["session"] = "session-a"
	options.Flags["runner"] = "agent"
	value, err = invoke("session start", options)
	if err != nil || value.(map[string]any)["compatible_for_new_work"] != false {
		t.Fatalf("active session was upgraded or minimum ignored %+v %v", value, err)
	}
	available.Store(false)
	options.Flags["session"] = "session-offline"
	if _, err := invoke("session start", options); err == nil {
		t.Fatal("offline new session accepted")
	}
	path, _, _, err := toolSessionLocation(options, config, root, binding)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("offline tool session index created")
	}
	available.Store(true)
	minimum.Store("0.1.22")
	options.Flags["session"] = "session-a"
	options.Flags["runner"] = "agent"
	if _, err := invoke("session end", options); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke("session end", options); err != nil {
		t.Fatal("repeat end was not idempotent", err)
	}
	if _, err := invoke("session start", options); err == nil || !strings.Contains(err.Error(), "SESSION_ENDED") {
		t.Fatalf("ended session restarted %v", err)
	}
}
