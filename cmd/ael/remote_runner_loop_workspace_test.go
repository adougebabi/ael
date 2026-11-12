package main

import (
	"ael/internal/core"
	"ael/internal/knowledge"
	"ael/internal/remote"
	"ael/internal/store"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// This exercises the internal Runner against a synthetic protocol server. It
// does not bypass or claim to pass the public remote-execution safety gate.
func TestRunnerLoopUsesReceiptConfirmedWorkspaceCopies(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	source, runtime := runnerWorkspaceFixture(t)
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err = filepath.EvalSymlinks(runtime)
	if err != nil {
		t.Fatal(err)
	}
	runnerWorkspaceFixtureGit(t, source, "rm", "--", "spec/rules.md")
	runnerWorkspaceFixtureGit(t, source, "commit", "-m", "remove fixture knowledge")
	if _, err = knowledge.Index(source); err != nil {
		t.Fatal(err)
	}
	type fixtureState struct {
		sync.Mutex
		step      string
		candidate remote.PortableCandidate
		attempt   core.Attempt
		attempts  []core.Attempt
		uploads   map[string][]byte
		artifacts int
	}
	state := &fixtureState{step: "define", uploads: map[string][]byte{}}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		state.Lock()
		defer state.Unlock()
		reply := func(value any) { _ = json.NewEncoder(w).Encode(value) }
		path := r.URL.Path
		switch {
		case path == "/api/v1/meta":
			reply(map[string]any{"mode": "server", "protocol": 1, "capabilities": []string{"remote_runners", "remote_tasks", "portable_candidates", "portable_contexts", "artifacts"}})
		case strings.HasSuffix(path, "/tasks/task") && r.Method == http.MethodGet:
			action := "continue"
			if state.step == "verify" {
				action = "wait_human"
			}
			task := core.Task{Definition: core.Definition{ID: "task"}, Step: state.step, Status: "open", Attempts: append([]core.Attempt{}, state.attempts...)}
			reasons := []string{}
			if action == "wait_human" {
				reasons = append(reasons, "fixture requires human review")
			}
			reply(map[string]any{"revision": "revision", "next_action": action, "current_step": state.step, "blocking_reasons": reasons, "task": task})
		case strings.HasSuffix(path, "/candidates") && r.Method == http.MethodPost:
			var body struct {
				Candidate remote.PortableCandidate `json:"candidate"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			state.candidate = body.Candidate
			reply(map[string]any{"id": "observation", "candidate_id": body.Candidate.ID})
		case strings.Contains(path, "/portable-operations/"):
			reply(map[string]any{"found": false})
		case strings.HasSuffix(path, "/observations/observation"):
			reply(map[string]any{"task_id": "task", "task_revision": "revision", "repository": "fixture-repo", "candidate_id": state.candidate.ID, "inputs_known": true})
		case strings.HasSuffix(path, "/contexts") && r.Method == http.MethodPost:
			reply(map[string]any{"id": "context", "inventory_unverified": false})
		case strings.HasSuffix(path, "/contexts/context") && r.Method == http.MethodGet:
			reply(map[string]any{"repository": "fixture-repo", "candidate_id": state.candidate.ID, "validity": "current", "inputs_known": true, "sources": []any{}})
		case strings.HasSuffix(path, "/runners/runner/claim"):
			state.attempt = core.Attempt{ID: fmt.Sprintf("AT-fixture-%d", len(state.attempts)+1), Unit: state.step, Executor: "runner", Input: state.candidate.ID, Token: "fixture-lease-token", Fence: len(state.attempts) + 1}
			reply(map[string]any{"task": core.Task{Definition: core.Definition{ID: "task"}, Step: state.step}, "attempt": state.attempt})
		case strings.HasSuffix(path, "/runners/runner/heartbeat"):
			issued := time.Now().UTC()
			reply(map[string]any{"attempt_id": state.attempt.ID, "fence": state.attempt.Fence, "lease_issued_at": issued, "lease_until": issued.Add(time.Minute)})
		case strings.HasSuffix(path, "/runners/runner/progress"):
			reply(map[string]any{})
		case strings.HasSuffix(path, "/runners/runner/observe"):
			var body struct {
				Candidate remote.PortableCandidate `json:"candidate"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			state.candidate = body.Candidate
			reply(map[string]any{"candidate_id": body.Candidate.ID})
		case strings.Contains(path, "/artifacts/uploads/by-key/"):
			w.WriteHeader(http.StatusNotFound)
			reply(map[string]any{"code": "ARTIFACT_NOT_FOUND"})
		case strings.HasSuffix(path, "/artifacts/uploads"):
			state.artifacts++
			id := fmt.Sprintf("upload-%d", state.artifacts)
			state.uploads[id] = nil
			reply(map[string]any{"upload_id": id, "status": "open", "received": 0})
		case strings.Contains(path, "/artifacts/uploads/") && strings.HasSuffix(path, "/chunks"):
			parts := strings.Split(path, "/")
			id := parts[len(parts)-2]
			var body struct {
				Data []byte `json:"data"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			state.uploads[id] = append(state.uploads[id], body.Data...)
			reply(map[string]any{"received": len(state.uploads[id])})
		case strings.Contains(path, "/artifacts/uploads/") && strings.HasSuffix(path, "/finalize"):
			parts := strings.Split(path, "/")
			id := parts[len(parts)-2]
			reply(map[string]any{"id": "artifact-" + id, "sha256": store.Hash(state.uploads[id])})
		case strings.HasSuffix(path, "/runners/runner/submit"):
			var body struct {
				OperationID string          `json:"operation_id"`
				CandidateID string          `json:"candidate_id"`
				Result      core.StepResult `json:"result"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			state.attempt.Candidate, state.attempt.Status = body.CandidateID, body.Result.Status
			state.attempts = append(state.attempts, state.attempt)
			if state.step == "define" {
				state.step = "execute"
			} else {
				state.step = "verify"
			}
			task := core.Task{Definition: core.Definition{ID: "task"}, Step: state.step, Status: "open", Attempts: append([]core.Attempt{}, state.attempts...)}
			reply(map[string]any{"operation_id": body.OperationID, "next": core.Next{Task: &task, Step: state.step}})
		default:
			http.Error(w, `{"code":"UNEXPECTED"}`, http.StatusNotFound)
		}
	})
	_, config, _, binding := runnerRecoveryFixture(t, handler)
	cfg := remoteRunnerConfig{RunnerID: "runner", Repository: "fixture-repo", CheckoutID: "checkout", WorkspaceID: "workspace", RuntimeDir: runtime, Workspace: source, Capabilities: []string{"development"}}
	cfg.Executor.MaxSteps, cfg.Executor.TimeoutSeconds = 4, 30
	cfg.Executor.Command = []string{python, "-c", `import json,os,pathlib
result={"status":"pass","summary":"fixture"}
if os.environ["AEL_STEP"] == "execute":
    pathlib.Path("app.txt").write_text("fixed\n")
    result["artifacts"]=["app.txt"]
with open(os.environ["AEL_RESULT"],"w") as f: json.dump(result,f)
`}
	value, err := runRemoteLoop(cfg, config, source, binding, "task", "")
	if err != nil {
		t.Fatal(err)
	}
	stopped := value.(map[string]any)
	if stopped["action"] != "wait_human" || stopped["workspace_delivery"] != "pending" || !strings.Contains(stopped["detail"].(string), "fixture requires human review") {
		t.Fatalf("waiting reason or undelivered workspace was lost: %+v", stopped)
	}
	chain, err := runnerOpenWorkspaceChain(cfg, source, binding, "task", "")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := chain.currentWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(workspace, "app.txt")); err != nil || string(raw) != "fixed\n" {
		t.Fatalf("accepted output missing: %q %v", raw, err)
	}
	if raw, err := os.ReadFile(filepath.Join(source, "app.txt")); err != nil || string(raw) != "working\n" {
		t.Fatalf("unconfirmed delivery modified the original checkout: %q %v", raw, err)
	}
	state.Lock()
	defer state.Unlock()
	if len(state.attempts) != 2 || state.artifacts != 1 {
		t.Fatalf("fixture did not receive both results and their artifact: attempts=%d artifacts=%d", len(state.attempts), state.artifacts)
	}
}
