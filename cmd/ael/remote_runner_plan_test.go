package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/store"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunnerArtifactUploadResumesSameKeyAfterDroppedResponses(t *testing.T) {
	data := bytes.Repeat([]byte("x"), remoteArtifactChunk+17)
	var mu sync.Mutex
	var stored []byte
	starts, chunks, finalizes := 0, 0, 0
	var protocolError string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		drop := func() {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
		}
		switch {
		case r.URL.Path == "/api/v1/meta":
			_, _ = fmt.Fprint(w, `{"mode":"server","protocol":1,"capabilities":["artifacts"]}`)
		case strings.HasSuffix(r.URL.Path, "/artifacts/uploads"):
			var body struct {
				OperationID, IdempotencyKey, SHA256 string
				Size                                int
			}
			var raw map[string]any
			_ = json.NewDecoder(r.Body).Decode(&raw)
			body.OperationID, _ = raw["operation_id"].(string)
			body.IdempotencyKey, _ = raw["idempotency_key"].(string)
			body.SHA256, _ = raw["sha256"].(string)
			body.Size = int(raw["size"].(float64))
			if body.OperationID != "artifact-fixture" || body.IdempotencyKey != "artifact-fixture" || body.SHA256 != store.Hash(data) || body.Size != len(data) {
				protocolError = "start request changed"
			}
			starts++
			if starts == 1 {
				drop()
				return
			}
			status := "open"
			if finalizes > 0 {
				status = "finalized"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"upload_id": "fixture-upload", "status": status, "received": len(stored)})
		case strings.HasSuffix(r.URL.Path, "/chunks"):
			var body struct {
				Offset int    `json:"offset"`
				Data   []byte `json:"data"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Offset != len(stored) {
				protocolError = "chunk offset changed"
			} else {
				stored = append(stored, body.Data...)
			}
			chunks++
			if chunks == 1 {
				drop()
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"received": len(stored)})
		case strings.HasSuffix(r.URL.Path, "/finalize"):
			finalizes++
			if finalizes == 1 {
				drop()
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "artifact-fixture", "sha256": store.Hash(stored)})
		default:
			http.Error(w, `{"code":"UNEXPECTED"}`, http.StatusNotFound)
		}
	})
	cfg, config, project, binding := runnerRecoveryFixture(t, handler)
	guard := newRunnerLeaseGuard(context.Background(), time.Now().Add(time.Minute))
	lease := &runnerUploadLease{RunnerID: cfg.RunnerID, Token: "fixture-lease-secret", Fence: 1, guard: guard}
	a := args{Flags: map[string]string{"task": "task", "attempt": "AT-fixture", "candidate": "candidate", "expected-revision": "revision", "source": "remote runner result", "mime": "text/plain", "operation-id": "artifact-fixture", "idempotency-key": "artifact-fixture"}}
	for attempt := 0; attempt < 3; attempt++ {
		if _, err := remoteUploadBytes(a, config, project, binding, "fixture.txt", data, lease); err == nil {
			t.Fatalf("fixture response %d was not dropped", attempt+1)
		}
	}
	receipt, err := remoteUploadBytes(a, config, project, binding, "fixture.txt", data, lease)
	if err != nil || receipt["id"] != "artifact-fixture" || receipt["sha256"] != store.Hash(data) {
		t.Fatalf("upload did not recover: %+v %v", receipt, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if protocolError != "" || starts != 4 || chunks != 2 || finalizes != 2 || !bytes.Equal(stored, data) {
		t.Fatalf("upload replay differed: error=%q starts=%d chunks=%d finalizes=%d bytes=%d", protocolError, starts, chunks, finalizes, len(stored))
	}
}

func TestRunnerSubmissionPlanDoesNotPersistControlCredentials(t *testing.T) {
	cfg := remoteRunnerConfig{RunnerID: "runner", RuntimeDir: t.TempDir(), Workspace: t.TempDir()}
	lease := remoteRunnerLease{TaskID: "task", AttemptID: "AT-plan-secret", Token: "fixture-lease-secret", Fence: 1, ClaimRevision: "revision", RunnerID: cfg.RunnerID}
	paths, err := runnerAttemptDirs(cfg, lease)
	if err != nil {
		t.Fatal(err)
	}
	profileToken := "abc1234"
	if err = os.WriteFile(filepath.Join(paths.IO, "report.txt"), []byte("token="+profileToken+" lease="+lease.Token+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result := remoteProcessResult{Result: core.StepResult{Status: "pass", Summary: "fixture", Artifacts: []string{"runtime:report.txt"}}}
	guard := newRunnerLeaseGuard(context.Background(), time.Now().Add(time.Minute))
	plan, err := runnerBuildSubmissionPlan(cfg, lease, core.Task{Definition: core.Definition{ID: "task"}, Step: "define"}, remote.PortableCandidate{ID: "candidate"}, result, paths, profileToken, guard, "submit-fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(paths.Control, "submission-plan.json"))
	if err != nil || bytes.Contains(raw, []byte(profileToken)) || bytes.Contains(raw, []byte(lease.Token)) {
		t.Fatalf("plan contains a control credential: %v", err)
	}
	if len(plan.Artifacts) != 1 {
		t.Fatalf("planned artifact missing: %+v", plan.Artifacts)
	}
	artifact, err := runnerPlannedArtifactBytes(filepath.Join(paths.Control, "submission-artifacts", plan.Artifacts[0].DataFile), plan.Artifacts[0].Size, plan.Artifacts[0].SHA256)
	if err != nil || bytes.Contains(artifact, []byte(profileToken)) || bytes.Contains(artifact, []byte(lease.Token)) {
		t.Fatalf("planned artifact contains a control credential: %v", err)
	}
}

func TestRunnerSubmissionPlanRejectsEscapedCredentialsInMetadata(t *testing.T) {
	for _, secret := range []string{"a&z", `q"r`} {
		t.Run(secret, func(t *testing.T) {
			cfg := remoteRunnerConfig{RunnerID: "runner", RuntimeDir: t.TempDir(), Workspace: t.TempDir()}
			lease := remoteRunnerLease{TaskID: "task", AttemptID: "AT-plan-escaped", Token: "fixture-lease-secret", Fence: 1, ClaimRevision: "revision", RunnerID: cfg.RunnerID}
			paths, err := runnerAttemptDirs(cfg, lease)
			if err != nil {
				t.Fatal(err)
			}
			name := "report-" + secret + ".txt"
			if err = os.WriteFile(filepath.Join(paths.IO, name), []byte("safe fixture report\n"), 0600); err != nil {
				t.Fatal(err)
			}
			result := remoteProcessResult{Result: core.StepResult{Status: "pass", Summary: "fixture", Artifacts: []string{"runtime:" + name}}}
			guard := newRunnerLeaseGuard(context.Background(), time.Now().Add(time.Minute))
			_, err = runnerBuildSubmissionPlan(cfg, lease, core.Task{Definition: core.Definition{ID: "task"}, Step: "define"}, remote.PortableCandidate{ID: "candidate"}, result, paths, secret, guard, "submit-fixture", nil)
			if err == nil || !strings.Contains(err.Error(), "ARTIFACT_SENSITIVE") {
				t.Fatalf("escaped credential in reference was accepted: %v", err)
			}
			if _, err = os.Stat(filepath.Join(paths.Control, "submission-plan.json")); !os.IsNotExist(err) {
				t.Fatalf("sensitive plan was persisted: %v", err)
			}
			if _, err = os.Stat(filepath.Join(paths.Control, "submission-artifacts")); !os.IsNotExist(err) {
				t.Fatalf("artifact copies were created before sensitive metadata was rejected: %v", err)
			}
		})
	}
}

func TestRunnerPendingPlanRejectsChangedSnapshotBeforeSubmit(t *testing.T) {
	source, runtime := runnerWorkspaceFixture(t)
	var err error
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err = filepath.EvalSymlinks(runtime)
	if err != nil {
		t.Fatal(err)
	}
	cfg := remoteRunnerConfig{RunnerID: "runner", Repository: "fixture-repo", CheckoutID: "checkout", WorkspaceID: "workspace", RuntimeDir: runtime, Workspace: source}
	binding := remote.Binding{Schema: 1, Profile: "central", ProjectID: "shared"}
	chain, err := runnerOpenWorkspaceChain(cfg, source, binding, "task", "")
	if err != nil {
		t.Fatal(err)
	}
	lease := remoteRunnerLease{TaskID: "task", AttemptID: "AT-tamper", Token: "fixture-lease-secret", Fence: 4, ClaimRevision: "revision", RunnerID: cfg.RunnerID}
	paths, err := runnerAttemptDirs(cfg, lease)
	if err != nil {
		t.Fatal(err)
	}
	paths.Snapshot = filepath.Join(paths.IO, "snapshot")
	candidate, err := runnerCreateWorkspace(source, paths.Snapshot, "fixture-repo")
	if err != nil {
		t.Fatal(err)
	}
	if err = chain.prepare("task", lease.AttemptID, "submit-fixture", paths.Snapshot, candidate.ID); err != nil {
		t.Fatal(err)
	}
	guard := newRunnerLeaseGuard(context.Background(), time.Now().Add(time.Minute))
	task := core.Task{Definition: core.Definition{ID: "task"}, Step: "define"}
	result := remoteProcessResult{Result: core.StepResult{Status: "pass", Summary: "fixture"}}
	if _, err = runnerBuildSubmissionPlan(cfg, lease, task, candidate, result, paths, "fixture-profile-token", guard, "submit-fixture", chain); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"task_id": lease.TaskID, "attempt_id": lease.AttemptID, "fence": lease.Fence, "expected_revision": lease.ClaimRevision, "operation_id": "submit-fixture", "idempotency_key": "submit-fixture"}
	raw, _ := json.Marshal(payload)
	pending := filepath.Join(paths.Control, "pending-submission.json")
	if err = os.WriteFile(pending, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(paths.Snapshot, "app.txt"), []byte("tampered\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	posts := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/meta":
			_, _ = fmt.Fprint(w, `{"mode":"server","protocol":1,"capabilities":["remote_runners"]}`)
		case strings.Contains(r.URL.Path, "/portable-operations/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"found": true, "result": map[string]any{"operation_id": runnerPlanKey("output", "submit-fixture", ""), "attempt_id": lease.AttemptID, "candidate_id": candidate.ID}})
		case strings.Contains(r.URL.Path, "/operations/"):
			_, _ = fmt.Fprint(w, `{"found":false}`)
		case strings.HasSuffix(r.URL.Path, "/heartbeat"):
			issued := time.Now().UTC()
			_ = json.NewEncoder(w).Encode(map[string]any{"attempt_id": lease.AttemptID, "fence": lease.Fence, "lease_issued_at": issued, "lease_until": issued.Add(time.Minute)})
		case strings.HasSuffix(r.URL.Path, "/submit"):
			mu.Lock()
			posts++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"operation_id": "submit-fixture"})
		default:
			http.Error(w, `{"code":"UNEXPECTED"}`, http.StatusNotFound)
		}
	})
	_, config, _, _ := runnerRecoveryFixture(t, handler)
	if _, err = resumeRunnerResult(cfg, config, source, binding, pending); err == nil {
		t.Fatal("changed snapshot was accepted for replay")
	}
	mu.Lock()
	count := posts
	mu.Unlock()
	if count != 0 {
		t.Fatalf("result was submitted before snapshot validation: %d", count)
	}
	if _, err = os.Stat(pending); err != nil {
		t.Fatalf("pending result was not retained: %v", err)
	}
	if err = os.WriteFile(filepath.Join(paths.Snapshot, "app.txt"), []byte("working\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = resumeRunnerResult(cfg, config, source, binding, pending); err == nil {
		t.Fatal("altered pending payload was accepted")
	}
	mu.Lock()
	count = posts
	mu.Unlock()
	if count != 0 {
		t.Fatalf("altered pending payload was submitted: %d", count)
	}
}

func TestRunnerWorkspaceChainRecoversPlanSavedBeforeJournal(t *testing.T) {
	source, runtime := runnerWorkspaceFixture(t)
	var err error
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err = filepath.EvalSymlinks(runtime)
	if err != nil {
		t.Fatal(err)
	}
	cfg := remoteRunnerConfig{RunnerID: "runner", Repository: "fixture-repo", CheckoutID: "checkout", WorkspaceID: "workspace", RuntimeDir: runtime, Workspace: source}
	binding := remote.Binding{Schema: 1, Profile: "central", ProjectID: "shared"}
	chain, err := runnerOpenWorkspaceChain(cfg, source, binding, "task", "")
	if err != nil {
		t.Fatal(err)
	}
	lease := remoteRunnerLease{TaskID: "task", AttemptID: "AT-orphan", Token: "fixture-lease-secret", Fence: 4, ClaimRevision: "revision", RunnerID: cfg.RunnerID}
	paths, err := runnerAttemptDirs(cfg, lease)
	if err != nil {
		t.Fatal(err)
	}
	paths.Snapshot = filepath.Join(paths.IO, "snapshot")
	candidate, err := runnerCreateWorkspace(source, paths.Snapshot, "fixture-repo")
	if err != nil {
		t.Fatal(err)
	}
	guard := newRunnerLeaseGuard(context.Background(), time.Now().Add(time.Minute))
	task := core.Task{Definition: core.Definition{ID: "task"}, Step: "define"}
	result := remoteProcessResult{Result: core.StepResult{Status: "pass", Summary: "fixture"}}
	if _, err = runnerBuildSubmissionPlan(cfg, lease, task, candidate, result, paths, "fixture-profile-token", guard, "submit-fixture", chain); err != nil {
		t.Fatal(err)
	}
	if chain.Pending != nil {
		t.Fatal("fixture did not stop before journal preparation")
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/meta":
			_, _ = fmt.Fprint(w, `{"mode":"server","protocol":1,"capabilities":["remote_runners"]}`)
		case strings.Contains(r.URL.Path, "/portable-operations/"), strings.Contains(r.URL.Path, "/operations/"):
			_, _ = fmt.Fprint(w, `{"found":false}`)
		case strings.HasSuffix(r.URL.Path, "/heartbeat"):
			issued := time.Now().UTC()
			_ = json.NewEncoder(w).Encode(map[string]any{"attempt_id": lease.AttemptID, "fence": lease.Fence, "lease_issued_at": issued, "lease_until": issued.Add(time.Minute)})
		case strings.HasSuffix(r.URL.Path, "/observe"):
			_ = json.NewEncoder(w).Encode(map[string]any{"operation_id": runnerPlanKey("output", "submit-fixture", ""), "attempt_id": lease.AttemptID, "candidate_id": candidate.ID})
		case strings.HasSuffix(r.URL.Path, "/submit"):
			_ = json.NewEncoder(w).Encode(map[string]any{"operation_id": "submit-fixture", "next": core.Next{Task: &core.Task{Definition: core.Definition{ID: "task"}, Attempts: []core.Attempt{{ID: lease.AttemptID, Executor: cfg.RunnerID, Input: chain.CurrentCandidate, Candidate: candidate.ID, Status: "pass"}}}}})
		default:
			http.Error(w, `{"code":"UNEXPECTED"}`, http.StatusNotFound)
		}
	})
	_, config, _, _ := runnerRecoveryFixture(t, handler)
	recovered, err := runnerRecoverWorkspaceChain(cfg, config, source, binding, "task", "", chain)
	if err != nil || recovered.Pending != nil || recovered.CurrentRelative == "" || recovered.CurrentCandidate != candidate.ID {
		t.Fatalf("plan saved before journal was not recovered: %+v %v", recovered, err)
	}
}

func TestRunnerWorkspaceChainRejectsTamperedPlanBeforeJournal(t *testing.T) {
	source, runtime := runnerWorkspaceFixture(t)
	var err error
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err = filepath.EvalSymlinks(runtime)
	if err != nil {
		t.Fatal(err)
	}
	cfg := remoteRunnerConfig{RunnerID: "runner", Repository: "fixture-repo", CheckoutID: "checkout", WorkspaceID: "workspace", RuntimeDir: runtime, Workspace: source}
	binding := remote.Binding{Schema: 1, Profile: "central", ProjectID: "shared"}
	chain, err := runnerOpenWorkspaceChain(cfg, source, binding, "task", "")
	if err != nil {
		t.Fatal(err)
	}
	lease := remoteRunnerLease{TaskID: "task", AttemptID: "AT-plan-tamper", Token: "fixture-lease-secret", Fence: 4, ClaimRevision: "revision", RunnerID: cfg.RunnerID}
	paths, err := runnerAttemptDirs(cfg, lease)
	if err != nil {
		t.Fatal(err)
	}
	paths.Snapshot = filepath.Join(paths.IO, "snapshot")
	candidate, err := runnerCreateWorkspace(source, paths.Snapshot, "fixture-repo")
	if err != nil {
		t.Fatal(err)
	}
	guard := newRunnerLeaseGuard(context.Background(), time.Now().Add(time.Minute))
	plan, err := runnerBuildSubmissionPlan(cfg, lease, core.Task{Definition: core.Definition{ID: "task"}, Step: "define"}, candidate, remoteProcessResult{Result: core.StepResult{Status: "pass", Summary: "fixture"}}, paths, "fixture-profile-token", guard, "submit-fixture", chain)
	if err != nil {
		t.Fatal(err)
	}
	plan.SnapshotRelative, plan.Journal, plan.ScopeKind, plan.ScopeID = "", "", "", ""
	plan.InputCandidate, plan.InputFingerprint, plan.OutputFingerprint = "", "", ""
	plan.Result.Summary = "modified after plan was saved"
	raw, _ := json.Marshal(plan)
	planPath := filepath.Join(paths.Control, "submission-plan.json")
	if err = os.WriteFile(planPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = runnerRecoverWorkspaceChain(cfg, "", source, binding, "task", "", chain); err == nil {
		t.Fatal("tampered plan was silently treated as a legacy result")
	}
	if _, err = os.Stat(planPath); err != nil {
		t.Fatalf("tampered plan was not retained: %v", err)
	}
}

func TestRunnerSubmissionRecoversLostObserveWithOriginalKey(t *testing.T) {
	source, runtime := runnerWorkspaceFixture(t)
	var err error
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err = filepath.EvalSymlinks(runtime)
	if err != nil {
		t.Fatal(err)
	}
	cfg := remoteRunnerConfig{RunnerID: "runner", Repository: "fixture-repo", CheckoutID: "checkout", WorkspaceID: "workspace", RuntimeDir: runtime, Workspace: source}
	binding := remote.Binding{Schema: 1, Profile: "central", ProjectID: "shared"}
	chain, err := runnerOpenWorkspaceChain(cfg, source, binding, "task", "")
	if err != nil {
		t.Fatal(err)
	}
	lease := remoteRunnerLease{TaskID: "task", AttemptID: "AT-plan", Token: "fixture-lease-secret", Fence: 4, ClaimRevision: "revision", RunnerID: cfg.RunnerID}
	paths, err := runnerAttemptDirs(cfg, lease)
	if err != nil {
		t.Fatal(err)
	}
	paths.Snapshot = filepath.Join(paths.IO, "snapshot")
	candidate, err := runnerCreateWorkspace(source, paths.Snapshot, "fixture-repo")
	if err != nil {
		t.Fatal(err)
	}
	if err = chain.prepare("task", lease.AttemptID, "submit-fixture", paths.Snapshot, candidate.ID); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var observeKey string
	observePosts, submitPosts := 0, 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/api/v1/meta":
			_, _ = fmt.Fprint(w, `{"mode":"server","protocol":1,"capabilities":["remote_runners"]}`)
		case strings.Contains(r.URL.Path, "/portable-operations/"):
			if observeKey == "" {
				_, _ = fmt.Fprint(w, `{"found":false}`)
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{"found": true, "result": map[string]any{"operation_id": observeKey, "attempt_id": lease.AttemptID, "candidate_id": candidate.ID}})
			}
		case strings.Contains(r.URL.Path, "/operations/"):
			_, _ = fmt.Fprint(w, `{"found":false}`)
		case strings.HasSuffix(r.URL.Path, "/heartbeat"):
			issued := time.Now().UTC()
			_ = json.NewEncoder(w).Encode(map[string]any{"attempt_id": lease.AttemptID, "fence": lease.Fence, "lease_issued_at": issued, "lease_until": issued.Add(time.Minute)})
		case strings.HasSuffix(r.URL.Path, "/observe"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			observeKey, _ = body["operation_id"].(string)
			observePosts++
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
		case strings.HasSuffix(r.URL.Path, "/submit"):
			submitPosts++
			receipt := map[string]any{"operation_id": "submit-fixture", "next": core.Next{Task: &core.Task{Definition: core.Definition{ID: "task"}, Attempts: []core.Attempt{{ID: lease.AttemptID, Executor: cfg.RunnerID, Input: chain.CurrentCandidate, Candidate: candidate.ID, Status: "pass"}}}}}
			_ = json.NewEncoder(w).Encode(receipt)
		default:
			http.Error(w, `{"code":"UNEXPECTED"}`, http.StatusNotFound)
		}
	})
	_, config, _, _ := runnerRecoveryFixture(t, handler)
	ctx := context.Background()
	guard := newRunnerLeaseGuard(ctx, time.Now().Add(time.Minute))
	task := core.Task{Definition: core.Definition{ID: "task"}, Step: "define"}
	result := remoteProcessResult{Result: core.StepResult{Status: "pass", Summary: "fixture"}}
	err = runnerSubmitResult(ctx, cfg, config, source, binding, lease, task, candidate, result, paths, "fixture-profile-token", guard, "submit-fixture", chain)
	if err == nil || !strings.Contains(err.Error(), "RUNNER_RESULT_PENDING") {
		t.Fatalf("lost observe response was not retained: %v", err)
	}
	planPath := filepath.Join(paths.Control, "submission-plan.json")
	if _, err = resumeRunnerResult(cfg, config, source, binding, planPath); err != nil {
		t.Fatal(err)
	}
	reopened, err := runnerOpenWorkspaceChain(cfg, source, binding, "task", "")
	if err != nil || reopened.Pending != nil || reopened.CurrentCandidate != candidate.ID {
		t.Fatalf("recovered receipt did not advance workspace: %+v %v", reopened, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if observePosts != 1 || submitPosts != 1 {
		t.Fatalf("recovery repeated an accepted operation: observe=%d submit=%d", observePosts, submitPosts)
	}
}

func TestRunnerSubmissionRecoversDroppedUploadAndSubmitResponses(t *testing.T) {
	source, runtime := runnerWorkspaceFixture(t)
	var err error
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err = filepath.EvalSymlinks(runtime)
	if err != nil {
		t.Fatal(err)
	}
	cfg := remoteRunnerConfig{RunnerID: "runner", Repository: "fixture-repo", CheckoutID: "checkout", WorkspaceID: "workspace", RuntimeDir: runtime, Workspace: source}
	binding := remote.Binding{Schema: 1, Profile: "central", ProjectID: "shared"}
	chain, err := runnerOpenWorkspaceChain(cfg, source, binding, "task", "")
	if err != nil {
		t.Fatal(err)
	}
	lease := remoteRunnerLease{TaskID: "task", AttemptID: "AT-upload-plan", Token: "fixture-lease-secret", Fence: 4, ClaimRevision: "revision", RunnerID: cfg.RunnerID}
	paths, err := runnerAttemptDirs(cfg, lease)
	if err != nil {
		t.Fatal(err)
	}
	paths.Snapshot = filepath.Join(paths.IO, "snapshot")
	if _, err = runnerCreateWorkspace(source, paths.Snapshot, "fixture-repo"); err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("x"), remoteArtifactChunk+17)
	if err = os.WriteFile(filepath.Join(paths.Snapshot, "new.txt"), data, 0755); err != nil {
		t.Fatal(err)
	}
	candidate, _, err := runnerWorkspaceFingerprint(paths.Snapshot, "fixture-repo")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var observedKey, artifactKey, protocolError string
	var stored []byte
	starts, chunks, finalizes, submits := 0, 0, 0, 0
	accepted, dropLookup := false, false
	receipt := func() map[string]any {
		return map[string]any{"operation_id": "submit-fixture", "next": core.Next{Task: &core.Task{Definition: core.Definition{ID: "task"}, Attempts: []core.Attempt{{ID: lease.AttemptID, Executor: cfg.RunnerID, Input: chain.CurrentCandidate, Candidate: candidate.ID, Status: "pass"}}}}}
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		drop := func() {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
		}
		switch {
		case r.URL.Path == "/api/v1/meta":
			_, _ = fmt.Fprint(w, `{"mode":"server","protocol":1,"capabilities":["remote_runners","artifacts"]}`)
		case strings.Contains(r.URL.Path, "/portable-operations/"):
			if observedKey == "" {
				_, _ = fmt.Fprint(w, `{"found":false}`)
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{"found": true, "result": map[string]any{"operation_id": observedKey, "attempt_id": lease.AttemptID, "candidate_id": candidate.ID}})
			}
		case strings.Contains(r.URL.Path, "/operations/"):
			if dropLookup {
				dropLookup = false
				drop()
				return
			}
			if accepted {
				value := receipt()
				value["found"] = true
				_ = json.NewEncoder(w).Encode(value)
			} else {
				_, _ = fmt.Fprint(w, `{"found":false}`)
			}
		case strings.HasSuffix(r.URL.Path, "/heartbeat"):
			issued := time.Now().UTC()
			_ = json.NewEncoder(w).Encode(map[string]any{"attempt_id": lease.AttemptID, "fence": lease.Fence, "lease_issued_at": issued, "lease_until": issued.Add(time.Minute)})
		case strings.HasSuffix(r.URL.Path, "/observe"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			observedKey, _ = body["operation_id"].(string)
			_ = json.NewEncoder(w).Encode(map[string]any{"operation_id": observedKey, "attempt_id": lease.AttemptID, "candidate_id": candidate.ID})
		case strings.Contains(r.URL.Path, "/artifacts/uploads/by-key/"):
			if artifactKey == "" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = fmt.Fprint(w, `{"code":"ARTIFACT_NOT_FOUND"}`)
			} else {
				status := "open"
				if finalizes > 0 {
					status = "finalized"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"upload_id": "fixture-upload", "status": status, "received": len(stored)})
			}
		case strings.HasSuffix(r.URL.Path, "/artifacts/uploads"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			key, _ := body["operation_id"].(string)
			if artifactKey == "" {
				artifactKey = key
			} else if artifactKey != key {
				protocolError = "artifact operation key changed"
			}
			starts++
			if starts == 1 {
				drop()
				return
			}
			status := "open"
			if finalizes > 0 {
				status = "finalized"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"upload_id": "fixture-upload", "status": status, "received": len(stored)})
		case strings.HasSuffix(r.URL.Path, "/chunks"):
			var body struct {
				Offset int    `json:"offset"`
				Data   []byte `json:"data"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Offset != len(stored) {
				protocolError = "chunk offset changed"
			} else {
				stored = append(stored, body.Data...)
			}
			chunks++
			if chunks == 1 {
				drop()
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"received": len(stored)})
		case strings.HasSuffix(r.URL.Path, "/finalize"):
			finalizes++
			if finalizes == 1 {
				drop()
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "artifact-fixture", "sha256": store.Hash(stored)})
		case strings.HasSuffix(r.URL.Path, "/submit"):
			submits++
			accepted, dropLookup = true, true
			drop()
		default:
			http.Error(w, `{"code":"UNEXPECTED"}`, http.StatusNotFound)
		}
	})
	_, config, _, _ := runnerRecoveryFixture(t, handler)
	ctx := context.Background()
	guard := newRunnerLeaseGuard(ctx, time.Now().Add(time.Minute))
	task := core.Task{Definition: core.Definition{ID: "task"}, Step: "execute"}
	result := remoteProcessResult{Result: core.StepResult{Status: "pass", Summary: "fixture", Artifacts: []string{"new.txt"}}}
	err = runnerSubmitResult(ctx, cfg, config, source, binding, lease, task, candidate, result, paths, "fixture-profile-token", guard, "submit-fixture", chain)
	if err == nil || !strings.Contains(err.Error(), "RUNNER_RESULT_PENDING") {
		t.Fatalf("lost start response was not retained: %v", err)
	}
	planPath := filepath.Join(paths.Control, "submission-plan.json")
	for attempt := 0; attempt < 2; attempt++ {
		if _, err = resumeRunnerResult(cfg, config, source, binding, planPath); err == nil || !strings.Contains(err.Error(), "RUNNER_RESULT_PENDING") {
			t.Fatalf("lost upload response %d was not retained: %v", attempt+1, err)
		}
	}
	if _, err = resumeRunnerResult(cfg, config, source, binding, planPath); err == nil || !strings.Contains(err.Error(), "RUNNER_RESULT_PENDING") {
		t.Fatalf("lost submit response was not retained: %v", err)
	}
	if _, err = resumeRunnerResult(cfg, config, source, binding, planPath); err != nil {
		t.Fatal(err)
	}
	reopened, err := runnerOpenWorkspaceChain(cfg, source, binding, "task", "")
	if err != nil || reopened.Pending != nil || reopened.CurrentCandidate != candidate.ID {
		t.Fatalf("recovered result did not advance workspace: %+v %v", reopened, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if protocolError != "" || starts != 4 || chunks != 2 || finalizes != 2 || submits != 1 || !bytes.Equal(stored, data) {
		t.Fatalf("recovery differed: error=%q starts=%d chunks=%d finalizes=%d submits=%d bytes=%d", protocolError, starts, chunks, finalizes, submits, len(stored))
	}
}
