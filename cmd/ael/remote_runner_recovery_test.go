package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func runnerRecoveryFixture(t *testing.T, handler http.Handler) (remoteRunnerConfig, string, string, remote.Binding) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, config, runtime := filepath.Join(root, "project"), filepath.Join(root, "profile"), filepath.Join(root, "runtime")
	for _, dir := range []string{project, runtime} {
		if err = os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err = remote.SaveProfile(config, project, "central", "http://"+listener.Addr().String(), "fixture-profile-token"); err != nil {
		t.Fatal(err)
	}
	return remoteRunnerConfig{RunnerID: "runner", RuntimeDir: runtime, Workspace: project}, config, project, remote.Binding{Schema: 1, Profile: "central", ProjectID: "shared"}
}

func TestRunnerPendingSubmissionRestoresTokenOnlyForReplay(t *testing.T) {
	var mu sync.Mutex
	submissions := 0
	var replay map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/meta":
			_, _ = fmt.Fprint(w, `{"mode":"server","protocol":1,"capabilities":["remote_runners"]}`)
		case strings.HasSuffix(r.URL.Path, "/observe"):
			_, _ = fmt.Fprint(w, `{}`)
		case strings.Contains(r.URL.Path, "/operations/"):
			_, _ = fmt.Fprint(w, `{"found":false}`)
		case strings.HasSuffix(r.URL.Path, "/heartbeat"):
			issued := time.Now().UTC()
			_ = json.NewEncoder(w).Encode(map[string]any{"attempt_id": "AT-recovery", "fence": 4, "lease_issued_at": issued, "lease_until": issued.Add(time.Minute)})
		case strings.HasSuffix(r.URL.Path, "/submit"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			submissions++
			count := submissions
			if count == 2 {
				replay = body
			}
			mu.Unlock()
			if count == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = fmt.Fprint(w, `{"code":"FIXTURE_UNAVAILABLE"}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"operation_id": body["operation_id"]})
		default:
			http.Error(w, `{"code":"UNEXPECTED"}`, http.StatusNotFound)
		}
	})
	cfg, config, project, binding := runnerRecoveryFixture(t, handler)
	lease := remoteRunnerLease{TaskID: "task", AttemptID: "AT-recovery", Token: "fixture-lease-secret", Fence: 4, ClaimRevision: "revision", RunnerID: cfg.RunnerID}
	paths, err := runnerAttemptDirs(cfg, lease)
	if err != nil {
		t.Fatal(err)
	}
	task := core.Task{Definition: core.Definition{ID: "task"}, Step: "define"}
	result := remoteProcessResult{Result: core.StepResult{Status: "pass", Summary: "fixture"}}
	ctx := context.Background()
	guard := newRunnerLeaseGuard(ctx, time.Now().Add(time.Minute))
	err = runnerSubmitResult(ctx, cfg, config, project, binding, lease, task, remote.PortableCandidate{ID: "candidate"}, result, paths, "fixture-profile-token", guard)
	if err == nil || !strings.Contains(err.Error(), "RUNNER_RESULT_PENDING") {
		t.Fatalf("submission was not retained: %v", err)
	}
	pending := filepath.Join(paths.Control, "pending-submission.json")
	raw, err := os.ReadFile(pending)
	if err != nil || strings.Contains(string(raw), lease.Token) || strings.Contains(string(raw), `"token"`) {
		t.Fatalf("pending payload exposed a token: %s %v", raw, err)
	}
	if _, err = resumeRunnerResult(cfg, config, project, binding, pending); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	gotToken, gotAttempt, count := replay["token"], replay["attempt_id"], submissions
	mu.Unlock()
	if gotToken != lease.Token || gotAttempt != lease.AttemptID || count != 2 {
		t.Fatalf("replay did not use the retained lease: token=%v attempt=%v submissions=%d", gotToken, gotAttempt, count)
	}
	for _, path := range []string{pending, filepath.Join(paths.Control, "lease.json")} {
		if _, err = os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("recovered secret or pending file retained: %s %v", path, err)
		}
	}
	if _, err = os.Stat(filepath.Join(paths.Control, "receipt.json")); err != nil {
		t.Fatal(err)
	}
}

func TestRunnerPendingResultDoesNotReplayAnExpiredLease(t *testing.T) {
	var submits atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/meta":
			_, _ = fmt.Fprint(w, `{"mode":"server","protocol":1,"capabilities":["remote_runners"]}`)
		case strings.Contains(r.URL.Path, "/operations/"):
			_, _ = fmt.Fprint(w, `{"found":false}`)
		case strings.HasSuffix(r.URL.Path, "/heartbeat"):
			w.WriteHeader(http.StatusConflict)
			_, _ = fmt.Fprint(w, `{"code":"STALE_TOKEN"}`)
		case strings.HasSuffix(r.URL.Path, "/submit"):
			submits.Add(1)
			_, _ = fmt.Fprint(w, `{}`)
		default:
			http.Error(w, `{"code":"UNEXPECTED"}`, http.StatusNotFound)
		}
	})
	cfg, config, project, binding := runnerRecoveryFixture(t, handler)
	lease := remoteRunnerLease{TaskID: "task", AttemptID: "AT-expired", Token: "fixture-lease-secret", Fence: 4, ClaimRevision: "revision", RunnerID: cfg.RunnerID}
	paths, err := runnerAttemptDirs(cfg, lease)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"task_id": lease.TaskID, "attempt_id": lease.AttemptID, "fence": lease.Fence, "expected_revision": lease.ClaimRevision, "operation_id": "submit-fixture", "idempotency_key": "submit-fixture"}
	raw, _ := json.Marshal(payload)
	pending := filepath.Join(paths.Control, "pending-submission.json")
	if err = os.WriteFile(pending, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = resumeRunnerResult(cfg, config, project, binding, pending); err == nil || !strings.Contains(err.Error(), "RUNNER_RESULT_PENDING") {
		t.Fatalf("expired lease was not retained for inspection: %v", err)
	}
	if submits.Load() != 0 {
		t.Fatalf("expired pending result was submitted %d times", submits.Load())
	}
	if _, err = os.Stat(pending); err != nil {
		t.Fatalf("pending result was lost: %v", err)
	}
}

func TestRunnerPendingResultReportsOldServerAndKeepsReceipt(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/meta":
			_, _ = fmt.Fprint(w, `{"mode":"server","protocol":1,"capabilities":["remote_runners"]}`)
		case strings.Contains(r.URL.Path, "/operations/"):
			_, _ = fmt.Fprint(w, `{"found":false}`)
		case strings.HasSuffix(r.URL.Path, "/heartbeat"):
			_ = json.NewEncoder(w).Encode(map[string]any{"attempt_id": "AT-old-server", "fence": 4, "lease_until": time.Now().Add(time.Minute)})
		default:
			http.Error(w, `{"code":"UNEXPECTED"}`, http.StatusNotFound)
		}
	})
	cfg, config, project, binding := runnerRecoveryFixture(t, handler)
	lease := remoteRunnerLease{TaskID: "task", AttemptID: "AT-old-server", Token: "fixture-lease-secret", Fence: 4, ClaimRevision: "revision", RunnerID: cfg.RunnerID}
	paths, err := runnerAttemptDirs(cfg, lease)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"task_id": lease.TaskID, "attempt_id": lease.AttemptID, "fence": lease.Fence, "expected_revision": lease.ClaimRevision, "operation_id": "submit-fixture", "idempotency_key": "submit-fixture"}
	raw, _ := json.Marshal(payload)
	pending := filepath.Join(paths.Control, "pending-submission.json")
	if err = os.WriteFile(pending, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = resumeRunnerResult(cfg, config, project, binding, pending); err == nil || !strings.Contains(err.Error(), "REMOTE_PROTOCOL_UNSUPPORTED") {
		t.Fatalf("old Server did not produce an upgrade hint: %v", err)
	}
	if _, err = os.Stat(pending); err != nil {
		t.Fatalf("pending result was lost: %v", err)
	}
}

func TestRunnerClaimRecoveryUsesControlLayoutAndAcceptsLegacyClaims(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%t", legacy), func(t *testing.T) {
			var mu sync.Mutex
			var claim map[string]any
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.URL.Path == "/api/v1/meta":
					_, _ = fmt.Fprint(w, `{"mode":"server","protocol":1,"capabilities":["remote_runners"]}`)
				case strings.HasSuffix(r.URL.Path, "/claim"):
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					mu.Lock()
					claim = map[string]any{"operation_id": body["operation_id"], "context_id": "context", "task": core.Task{Definition: core.Definition{ID: "task"}}, "attempt": core.Attempt{ID: "AT-claim", Executor: "runner", Token: "fixture-lease-secret", Fence: 2}}
					_ = json.NewEncoder(w).Encode(claim)
					mu.Unlock()
				case strings.Contains(r.URL.Path, "/portable-operations/"):
					mu.Lock()
					_ = json.NewEncoder(w).Encode(map[string]any{"found": true, "result": claim})
					mu.Unlock()
				default:
					http.Error(w, `{"code":"UNEXPECTED"}`, http.StatusNotFound)
				}
			})
			cfg, config, project, binding := runnerRecoveryFixture(t, handler)
			_, _, claimPath, err := runnerClaim(cfg, config, project, binding, "task", "", "context", "revision")
			if err != nil {
				t.Fatal(err)
			}
			if legacy {
				old := filepath.Join(cfg.RuntimeDir, filepath.Base(claimPath))
				if err = os.Rename(claimPath, old); err != nil {
					t.Fatal(err)
				}
				claimPath = old
			}
			value, err := recoverRunnerClaim(cfg, config, project, binding, claimPath)
			if err != nil {
				t.Fatal(err)
			}
			response := value.(map[string]any)
			if response["action"] != "claim_recovered" || response["attempt_dir"] != filepath.Join(cfg.RuntimeDir, "control", "attempts", "AT-claim") || response["io_dir"] != filepath.Join(cfg.RuntimeDir, "io", "attempts", "AT-claim") {
				t.Fatalf("unexpected recovery paths: %+v", response)
			}
			if _, err = os.Stat(filepath.Join(response["attempt_dir"].(string), "lease.json")); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(response["attempt_dir"].(string), "process.json")); !os.IsNotExist(err) {
				t.Fatalf("claim recovery must not start a process: %v", err)
			}
		})
	}
}

func TestRunnerLegacyPendingRecoveryAndMismatch(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/operations/submit-fixture") {
			_, _ = fmt.Fprint(w, `{"found":true,"operation_id":"submit-fixture"}`)
			return
		}
		http.Error(w, `{"code":"UNEXPECTED"}`, http.StatusNotFound)
	})
	cfg, config, project, binding := runnerRecoveryFixture(t, handler)
	lease := remoteRunnerLease{TaskID: "task", AttemptID: "AT-legacy", Token: "fixture-lease-secret", Fence: 4, ClaimRevision: "revision", RunnerID: cfg.RunnerID}
	dir := filepath.Join(cfg.RuntimeDir, "attempts", lease.AttemptID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	leaseBytes, _ := json.Marshal(lease)
	if err := os.WriteFile(filepath.Join(dir, "lease.json"), leaseBytes, 0600); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"task_id": lease.TaskID, "attempt_id": lease.AttemptID, "token": "wrong-fixture-token", "fence": lease.Fence, "expected_revision": lease.ClaimRevision, "operation_id": "submit-fixture", "idempotency_key": "submit-fixture"}
	pending := filepath.Join(dir, "pending-submission.json")
	writePending := func() {
		t.Helper()
		raw, _ := json.Marshal(payload)
		if err := os.WriteFile(pending, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writePending()
	if _, err := resumeRunnerResult(cfg, config, project, binding, pending); err == nil || !strings.Contains(err.Error(), "RUNNER_RECOVERY_INVALID") {
		t.Fatalf("mismatched lease token was accepted: %v", err)
	}
	payload["token"] = lease.Token
	writePending()
	if _, err := resumeRunnerResult(cfg, config, project, binding, pending); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pending); !os.IsNotExist(err) {
		t.Fatalf("legacy pending submission was not consumed: %v", err)
	}
}
