package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/securityfs"
	"ael/internal/store"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type runnerPlannedArtifact struct {
	Ref, OperationID, Filename, MIME, SHA256, DataFile string
	Size                                               int
}

type runnerSubmissionPlan struct {
	Schema                                int
	RunnerID, TaskID, AttemptID           string
	Fence                                 int
	ClaimRevision, OperationID, ObserveID string
	SnapshotRelative                      string
	Journal, ScopeKind, ScopeID           string
	InputCandidate, InputFingerprint      string
	OutputFingerprint                     string
	Candidate                             remote.PortableCandidate
	Result                                core.StepResult
	Method                                string
	Inputs                                []string
	ExitCode                              *int
	Artifacts                             []runnerPlannedArtifact
}

type runnerSubmissionMarker struct {
	Schema             int
	AttemptID, Journal string
	PlanHash           string
}

func runnerPlanKey(kind, operation, ref string) string {
	return kind + "-" + store.Hash([]byte(operation + "\x00" + ref))[:32]
}

func runnerMetadataContainsSecrets(value any, secrets ...string) (bool, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	var decoded any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		return false, err
	}
	return runnerValueContainsSecrets(decoded, secrets...), nil
}

func runnerPlannedArtifactBytes(path string, size int, digest string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || securityfs.NoACL(path) != nil || securityfs.SingleLinkRegular(path) != nil || info.Size() != int64(size) || size <= 0 || size > remoteArtifactLimit {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "planned artifact is missing or unsafe")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, remoteArtifactLimit+1))
	if err != nil || len(data) != size || store.Hash(data) != digest {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "planned artifact bytes changed")
	}
	return data, nil
}

func runnerStorePlannedArtifact(path string, data []byte) error {
	if _, err := os.Lstat(path); err == nil {
		prior, err := runnerPlannedArtifactBytes(path, len(data), store.Hash(data))
		if err != nil || !bytes.Equal(prior, data) {
			return core.Error("RUNNER_RECOVERY_INVALID", "existing planned artifact differs")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

func runnerBuildSubmissionPlan(cfg remoteRunnerConfig, lease remoteRunnerLease, task core.Task, output remote.PortableCandidate, processResult remoteProcessResult, paths runnerAttemptPaths, profileToken string, guard *runnerLeaseGuard, operationID string, chain *runnerWorkspaceChain) (runnerSubmissionPlan, error) {
	var plan runnerSubmissionPlan
	if guard == nil || guard.check() != nil || !remoteRunnerID.MatchString(operationID) {
		return plan, core.Error("LEASE_LOST", "a valid local lease and result operation are required")
	}
	result := sanitizeRunnerResult(processResult.Result, profileToken, lease.Token)
	if strings.TrimSpace(result.Summary) == "" || (result.Status != "pass" && result.Status != "fail" && result.Status != "blocked") {
		result = core.StepResult{Status: "blocked", Summary: "local process did not produce a valid final result"}
	}
	if task.Step == "execute" && result.Status == "pass" && len(result.Artifacts) == 0 {
		result = core.StepResult{Status: "blocked", Summary: "execution result omitted a required deliverable"}
	}
	plan = runnerSubmissionPlan{Schema: 1, RunnerID: cfg.RunnerID, TaskID: lease.TaskID, AttemptID: lease.AttemptID, Fence: lease.Fence, ClaimRevision: lease.ClaimRevision, OperationID: operationID, ObserveID: runnerPlanKey("output", operationID, ""), Candidate: output, Result: result, Method: processResult.Method, Inputs: append([]string{}, processResult.Inputs...), ExitCode: processResult.ExitCode, Artifacts: []runnerPlannedArtifact{}}
	artifactCfg := cfg
	if paths.Snapshot != "" {
		snapshot, err := filepath.EvalSymlinks(paths.Snapshot)
		if err != nil {
			return plan, err
		}
		runtime, err := filepath.EvalSymlinks(cfg.RuntimeDir)
		if err != nil {
			return plan, err
		}
		rel, err := filepath.Rel(runtime, snapshot)
		if err != nil {
			return plan, err
		}
		if _, err = runnerWorkspaceSnapshotPath(runtime, rel, lease.AttemptID); err != nil {
			return plan, err
		}
		plan.SnapshotRelative = filepath.ToSlash(rel)
		artifactCfg.Workspace = snapshot
		if chain == nil {
			return plan, core.Error("RUNNER_RECOVERY_INVALID", "a workspace journal is required for a snapshot")
		}
		candidate, fingerprint, err := runnerWorkspaceFingerprint(snapshot, cfg.Repository)
		if err != nil || candidate.ID != output.ID {
			return plan, core.Error("RUNNER_WORKSPACE_CHANGED", "output snapshot differs from its candidate")
		}
		plan.Journal, plan.ScopeKind, plan.ScopeID = filepath.Base(chain.path), chain.ScopeKind, chain.ScopeID
		plan.InputCandidate, plan.InputFingerprint, plan.OutputFingerprint = chain.CurrentCandidate, chain.CurrentFingerprint, fingerprint
	} else if chain != nil {
		return plan, core.Error("RUNNER_RECOVERY_INVALID", "workspace journal has no output snapshot")
	}
	if sensitive, err := runnerMetadataContainsSecrets(plan, profileToken, lease.Token); err != nil {
		return plan, err
	} else if sensitive {
		return plan, core.Error("ARTIFACT_SENSITIVE", "result metadata contains a control credential")
	}
	refs := append([]string{}, result.Artifacts...)
	if result.Knowledge != nil {
		refs = append(refs, result.Knowledge.Artifacts...)
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		if err := guard.check(); err != nil {
			return plan, err
		}
		data, filename, mimeType, err := runnerOutputFile(artifactCfg, paths.IO, ref, output, profileToken, lease.Token)
		if err != nil {
			return plan, runnerProblem(err, "read explicit artifact")
		}
		dir, err := runnerPrivateDir(cfg.RuntimeDir, "control", "attempts", lease.AttemptID, "submission-artifacts")
		if err != nil {
			return plan, err
		}
		name := store.Hash([]byte(ref)) + ".bin"
		if err = guard.check(); err != nil {
			return plan, err
		}
		if err = runnerStorePlannedArtifact(filepath.Join(dir, name), data); err != nil {
			return plan, err
		}
		plan.Artifacts = append(plan.Artifacts, runnerPlannedArtifact{Ref: ref, OperationID: runnerPlanKey("artifact", operationID, ref), Filename: filename, MIME: mimeType, SHA256: store.Hash(data), Size: len(data), DataFile: name})
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return plan, err
	}
	if sensitive, checkErr := runnerMetadataContainsSecrets(plan, profileToken, lease.Token); checkErr != nil {
		return plan, checkErr
	} else if sensitive {
		return plan, core.Error("ARTIFACT_SENSITIVE", "result metadata contains a control credential")
	}
	if err = guard.check(); err != nil {
		return plan, err
	}
	marker := runnerSubmissionMarker{Schema: 1, AttemptID: lease.AttemptID, Journal: plan.Journal, PlanHash: store.Hash(raw)}
	markerBytes, err := json.Marshal(marker)
	if err != nil {
		return plan, err
	}
	markerPath := filepath.Join(paths.Control, "submission-plan.sha256")
	if err = runnerStorePlannedArtifact(markerPath, markerBytes); err != nil {
		return plan, err
	}
	if err = guard.check(); err != nil {
		return plan, err
	}
	path := filepath.Join(paths.Control, "submission-plan.json")
	if _, err = os.Lstat(path); err == nil {
		prior, readErr := readRunnerPrivateFile(path)
		if readErr != nil || !bytes.Equal(prior, raw) {
			return plan, core.Error("RUNNER_RECOVERY_INVALID", "existing submission plan differs")
		}
	} else if os.IsNotExist(err) {
		if err = store.Atomic(path, raw, 0600); err != nil {
			return plan, err
		}
	} else {
		return plan, err
	}
	return plan, nil
}

func runnerReadSubmissionPlan(path string) (runnerSubmissionPlan, []byte, error) {
	var plan runnerSubmissionPlan
	raw, err := readRunnerPrivateFile(path)
	if err != nil {
		return plan, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&plan) != nil || decoder.Decode(new(any)) != io.EOF {
		return plan, nil, core.Error("RUNNER_RECOVERY_INVALID", "submission plan is invalid")
	}
	markerBytes, err := readRunnerPrivateFile(filepath.Join(filepath.Dir(path), "submission-plan.sha256"))
	if err != nil {
		return plan, nil, core.Error("RUNNER_RECOVERY_REQUIRED", "submission plan marker is missing or invalid")
	}
	var marker runnerSubmissionMarker
	decoder = json.NewDecoder(bytes.NewReader(markerBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&marker) != nil || decoder.Decode(new(any)) != io.EOF || marker.Schema != 1 || marker.AttemptID != plan.AttemptID || marker.Journal != plan.Journal || marker.PlanHash != store.Hash(raw) {
		return plan, nil, core.Error("RUNNER_RECOVERY_INVALID", "submission plan differs from its retained marker")
	}
	return plan, raw, nil
}

func runnerValidateSubmissionPlanIdentity(cfg remoteRunnerConfig, lease remoteRunnerLease, plan runnerSubmissionPlan) error {
	if plan.Schema != 1 || plan.RunnerID != cfg.RunnerID || plan.TaskID != lease.TaskID || plan.AttemptID != lease.AttemptID || plan.Fence != lease.Fence || plan.ClaimRevision != lease.ClaimRevision || !remoteRunnerID.MatchString(plan.OperationID) || plan.ObserveID != runnerPlanKey("output", plan.OperationID, "") || plan.Candidate.ID == "" {
		return core.Error("RUNNER_RECOVERY_INVALID", "submission plan and lease disagree")
	}
	if strings.TrimSpace(plan.Result.Summary) == "" || (plan.Result.Status != "pass" && plan.Result.Status != "fail" && plan.Result.Status != "blocked") {
		return core.Error("RUNNER_RECOVERY_INVALID", "planned result is invalid")
	}
	return nil
}

func runnerValidateSubmissionPlan(cfg remoteRunnerConfig, lease remoteRunnerLease, plan runnerSubmissionPlan) error {
	if err := runnerValidateSubmissionPlanIdentity(cfg, lease, plan); err != nil {
		return err
	}
	if plan.SnapshotRelative != "" {
		snapshot, err := runnerWorkspaceSnapshotPath(cfg.RuntimeDir, plan.SnapshotRelative, lease.AttemptID)
		if err != nil || remote.ValidatePortableCandidate(plan.Candidate) != nil {
			return core.Error("RUNNER_RECOVERY_INVALID", "submission snapshot or candidate is invalid")
		}
		if !strings.HasPrefix(plan.Journal, "run-") || !strings.HasSuffix(plan.Journal, ".json") || !runnerWorkspaceHash(plan.InputCandidate) || !runnerWorkspaceHash(plan.InputFingerprint) || !runnerWorkspaceHash(plan.OutputFingerprint) || (plan.ScopeKind != "task" && plan.ScopeKind != "parent") || !remoteRunnerID.MatchString(plan.ScopeID) {
			return core.Error("RUNNER_RECOVERY_INVALID", "submission workspace identity is invalid")
		}
		referencePath, err := store.SafePath(cfg.RuntimeDir, filepath.Join("control", "attempts", lease.AttemptID, "workspace.json"))
		if err != nil {
			return core.Error("RUNNER_RECOVERY_INVALID", "submission workspace reference is unsafe")
		}
		raw, err := readRunnerPrivateFile(referencePath)
		if err != nil {
			return err
		}
		var reference runnerWorkspaceReference
		if json.Unmarshal(raw, &reference) != nil || reference.Journal != plan.Journal || reference.ScopeKind != plan.ScopeKind || reference.ScopeID != plan.ScopeID || reference.Pending.AttemptID != lease.AttemptID || reference.Pending.TaskID != lease.TaskID || reference.Pending.OperationID != plan.OperationID || reference.Pending.SnapshotRelative != plan.SnapshotRelative || reference.Pending.Input != plan.InputCandidate || reference.Pending.Candidate != plan.Candidate.ID || reference.Pending.Fingerprint != plan.OutputFingerprint {
			return core.Error("RUNNER_RECOVERY_INVALID", "submission plan and workspace reference disagree")
		}
		candidate, fingerprint, err := runnerWorkspaceFingerprint(snapshot, cfg.Repository)
		if err != nil || candidate.ID != plan.Candidate.ID || fingerprint != plan.OutputFingerprint {
			return core.Error("RUNNER_WORKSPACE_CHANGED", "submission snapshot changed after planning")
		}
	}
	if plan.SnapshotRelative == "" && (plan.Journal != "" || plan.ScopeKind != "" || plan.ScopeID != "" || plan.InputCandidate != "" || plan.InputFingerprint != "" || plan.OutputFingerprint != "") {
		return core.Error("RUNNER_RECOVERY_INVALID", "legacy submission has unexpected workspace state")
	}
	seen := map[string]bool{}
	for _, item := range plan.Artifacts {
		if item.Ref == "" || seen[item.Ref] || item.OperationID != runnerPlanKey("artifact", plan.OperationID, item.Ref) || item.DataFile != store.Hash([]byte(item.Ref))+".bin" || !runnerWorkspaceHash(item.SHA256) || item.Size <= 0 || item.Size > remoteArtifactLimit || item.Filename == "" || item.MIME == "" {
			return core.Error("RUNNER_RECOVERY_INVALID", "planned artifact metadata is invalid")
		}
		seen[item.Ref] = true
	}
	for _, ref := range plan.Result.Artifacts {
		if !seen[ref] {
			return core.Error("RUNNER_RECOVERY_INVALID", "planned result artifact is missing")
		}
	}
	if plan.Result.Knowledge != nil {
		for _, ref := range plan.Result.Knowledge.Artifacts {
			if !seen[ref] {
				return core.Error("RUNNER_RECOVERY_INVALID", "planned knowledge artifact is missing")
			}
		}
	}
	refs := map[string]bool{}
	for _, ref := range plan.Result.Artifacts {
		refs[ref] = true
	}
	if plan.Result.Knowledge != nil {
		for _, ref := range plan.Result.Knowledge.Artifacts {
			refs[ref] = true
		}
	}
	if len(refs) != len(plan.Artifacts) {
		return core.Error("RUNNER_RECOVERY_INVALID", "submission plan contains an unreferenced artifact")
	}
	return nil
}

func runnerSubmissionPending(path, stage string, err error) error {
	return core.Error("RUNNER_RESULT_PENDING", fmt.Sprintf("%s; result plan retained at %s: %v", stage, path, err))
}

func runnerEnsureSubmissionChain(cfg remoteRunnerConfig, project string, binding remote.Binding, lease remoteRunnerLease, plan runnerSubmissionPlan, guard *runnerLeaseGuard) error {
	if plan.SnapshotRelative == "" {
		return nil
	}
	if (plan.ScopeKind != "task" && plan.ScopeKind != "parent") || !remoteRunnerID.MatchString(plan.ScopeID) || !runnerWorkspaceHash(plan.InputCandidate) || !runnerWorkspaceHash(plan.InputFingerprint) || !runnerWorkspaceHash(plan.OutputFingerprint) {
		return core.Error("RUNNER_RECOVERY_INVALID", "submission plan workspace identity is invalid")
	}
	taskID, parentID := "", ""
	if plan.ScopeKind == "task" {
		taskID = plan.ScopeID
	} else {
		parentID = plan.ScopeID
	}
	chain, err := runnerWorkspaceChainForScope(cfg, project, binding, taskID, parentID, false)
	if err != nil {
		return err
	}
	if filepath.Base(chain.path) != plan.Journal {
		return core.Error("RUNNER_RECOVERY_INVALID", "submission plan belongs to another workspace journal")
	}
	if chain.Pending != nil {
		p := chain.Pending
		if p.TaskID != plan.TaskID || p.AttemptID != plan.AttemptID || p.OperationID != plan.OperationID || p.Input != plan.InputCandidate || p.Candidate != plan.Candidate.ID || p.Fingerprint != plan.OutputFingerprint || p.SnapshotRelative != plan.SnapshotRelative || chain.CurrentFingerprint != plan.InputFingerprint {
			return core.Error("RUNNER_RECOVERY_INVALID", "submission plan and pending workspace disagree")
		}
		return chain.ensurePendingReference(guard)
	}
	if chain.LastAttemptID == plan.AttemptID && chain.LastOperation == plan.OperationID {
		if chain.LastTaskID != plan.TaskID || chain.CurrentCandidate != plan.Candidate.ID || chain.CurrentFingerprint != plan.OutputFingerprint || chain.CurrentRelative != plan.SnapshotRelative {
			return core.Error("RUNNER_RECOVERY_INVALID", "accepted workspace differs from the submission plan")
		}
		_, err = chain.currentWorkspace()
		return err
	}
	if chain.CurrentCandidate != plan.InputCandidate || chain.CurrentFingerprint != plan.InputFingerprint {
		return core.Error("RUNNER_RECOVERY_REQUIRED", "submission plan no longer follows the current workspace")
	}
	if _, err = chain.currentWorkspace(); err != nil {
		return err
	}
	snapshot, err := runnerWorkspaceSnapshotPath(chain.runtime, plan.SnapshotRelative, lease.AttemptID)
	if err != nil {
		return err
	}
	return chain.prepareGuarded(plan.TaskID, plan.AttemptID, plan.OperationID, snapshot, plan.Candidate.ID, guard)
}

func runnerExecuteSubmissionPlan(cfg remoteRunnerConfig, config, project string, binding remote.Binding, lease remoteRunnerLease, plan runnerSubmissionPlan, paths runnerAttemptPaths, guard *runnerLeaseGuard) (map[string]any, error) {
	planPath := filepath.Join(paths.Control, "submission-plan.json")
	stored, raw, err := runnerReadSubmissionPlan(planPath)
	if err != nil {
		return nil, err
	}
	expected, err := json.Marshal(plan)
	if err != nil || !bytes.Equal(raw, expected) || stored.OperationID != plan.OperationID {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "submission plan changed before execution")
	}
	if err := runnerValidateSubmissionPlan(cfg, lease, plan); err != nil {
		return nil, err
	}
	lookup, err := runnerCall(config, project, binding, http.MethodGet, "/portable-operations/"+url.PathEscape(plan.ObserveID), nil)
	if err != nil {
		return nil, runnerSubmissionPending(planPath, "query candidate observation", err)
	}
	found, ok := lookup["found"].(bool)
	if !ok {
		return nil, core.Error("REMOTE_RESPONSE_INVALID", "candidate operation lookup is invalid")
	}
	if found {
		receipt, ok := lookup["result"].(map[string]any)
		if !ok || receipt["operation_id"] != plan.ObserveID || receipt["attempt_id"] != lease.AttemptID || receipt["candidate_id"] != plan.Candidate.ID {
			return nil, core.Error("IDEMPOTENCY_CONFLICT", "candidate observation differs from the retained plan")
		}
	} else {
		observed := map[string]any{"task_id": lease.TaskID, "attempt_id": lease.AttemptID, "token": lease.Token, "fence": lease.Fence, "expected_revision": lease.ClaimRevision, "candidate": plan.Candidate, "operation_id": plan.ObserveID, "idempotency_key": plan.ObserveID}
		if _, err = runnerGuardedCall(config, project, binding, guard, http.MethodPost, "/runners/"+url.PathEscape(cfg.RunnerID)+"/observe", observed); err != nil {
			return nil, runnerSubmissionPending(planPath, "record candidate", err)
		}
	}
	ids := map[string]string{}
	bindings := make([]map[string]string, 0, len(plan.Artifacts))
	for _, artifact := range plan.Artifacts {
		if err = guard.check(); err != nil {
			return nil, runnerSubmissionPending(planPath, "lease lost before artifact upload", err)
		}
		data, err := runnerPlannedArtifactBytes(filepath.Join(paths.Control, "submission-artifacts", artifact.DataFile), artifact.Size, artifact.SHA256)
		if err != nil {
			return nil, err
		}
		_, queryErr := runnerCall(config, project, binding, http.MethodGet, "/artifacts/uploads/by-key/"+url.PathEscape(artifact.OperationID), nil)
		if queryErr != nil && !strings.Contains(queryErr.Error(), "ARTIFACT_NOT_FOUND") {
			return nil, runnerSubmissionPending(planPath, "query artifact upload", queryErr)
		}
		uploadArgs := args{Flags: map[string]string{"task": lease.TaskID, "attempt": lease.AttemptID, "candidate": plan.Candidate.ID, "expected-revision": lease.ClaimRevision, "source": "remote runner result", "mime": artifact.MIME, "operation-id": artifact.OperationID, "idempotency-key": artifact.OperationID}}
		receipt, err := remoteUploadBytes(uploadArgs, config, project, binding, artifact.Filename, data, &runnerUploadLease{RunnerID: cfg.RunnerID, Token: lease.Token, Fence: lease.Fence, guard: guard})
		if err != nil {
			return nil, runnerSubmissionPending(planPath, "upload artifact", err)
		}
		artifactID, ok := receipt["id"].(string)
		if !ok || receipt["sha256"] != artifact.SHA256 {
			return nil, core.Error("REMOTE_RESPONSE_INVALID", "finalized artifact receipt does not match")
		}
		ids[artifact.Ref] = artifactID
		bindings = append(bindings, map[string]string{"path": artifact.Ref, "artifact_id": artifactID})
	}
	resultBytes, err := json.Marshal(plan.Result)
	if err != nil {
		return nil, err
	}
	var result core.StepResult
	if err = json.Unmarshal(resultBytes, &result); err != nil {
		return nil, err
	}
	for i, ref := range result.Artifacts {
		result.Artifacts[i] = ids[ref]
	}
	if result.Knowledge != nil {
		for i, ref := range result.Knowledge.Artifacts {
			result.Knowledge.Artifacts[i] = ids[ref]
		}
	}
	payload := map[string]any{"task_id": lease.TaskID, "attempt_id": lease.AttemptID, "token": lease.Token, "fence": lease.Fence, "expected_revision": lease.ClaimRevision, "candidate_id": plan.Candidate.ID, "result": result, "method": plan.Method, "inputs": plan.Inputs, "bindings": bindings, "operation_id": plan.OperationID, "idempotency_key": plan.OperationID}
	if plan.ExitCode != nil {
		payload["exit_code"] = *plan.ExitCode
	}
	pendingPayload := make(map[string]any, len(payload)-1)
	for name, value := range payload {
		if name != "token" {
			pendingPayload[name] = value
		}
	}
	pending, err := json.Marshal(pendingPayload)
	if err != nil {
		return nil, err
	}
	pendingPath := filepath.Join(paths.Control, "pending-submission.json")
	if _, err = os.Lstat(pendingPath); err == nil {
		prior, readErr := readRunnerPrivateFile(pendingPath)
		if readErr != nil || !bytes.Equal(prior, pending) {
			return nil, core.Error("RUNNER_RECOVERY_INVALID", "pending submission differs from the retained plan")
		}
	} else if os.IsNotExist(err) {
		if err = store.Atomic(pendingPath, pending, 0600); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	if err = guard.check(); err != nil {
		return nil, runnerSubmissionPending(planPath, "lease lost before result submission", err)
	}
	receipt, err := runnerGuardedCall(config, project, binding, guard, http.MethodPost, "/runners/"+url.PathEscape(cfg.RunnerID)+"/submit", payload)
	if err != nil {
		lookup, lookupErr := runnerCall(config, project, binding, http.MethodGet, "/runners/"+url.PathEscape(cfg.RunnerID)+"/operations/"+url.PathEscape(plan.OperationID), nil)
		if lookupErr == nil && lookup["found"] == true && lookup["operation_id"] == plan.OperationID {
			return lookup, nil
		}
		return nil, runnerSubmissionPending(planPath, "submit result", err)
	}
	return receipt, nil
}

func runnerFinishSubmission(cfg remoteRunnerConfig, project string, binding remote.Binding, lease remoteRunnerLease, paths runnerAttemptPaths, receipt map[string]any) error {
	planPath := filepath.Join(paths.Control, "submission-plan.json")
	if _, err := os.Lstat(planPath); err == nil {
		plan, _, err := runnerReadSubmissionPlan(planPath)
		if err != nil || runnerValidateSubmissionPlan(cfg, lease, plan) != nil || receipt["operation_id"] != plan.OperationID {
			return core.Error("RUNNER_RECOVERY_INVALID", "stored submission plan and receipt disagree")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	receiptBytes, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if err = store.Atomic(filepath.Join(paths.Control, "receipt.json"), receiptBytes, 0600); err != nil {
		return err
	}
	if err = runnerAcceptWorkspaceReceipt(cfg, project, binding, lease.AttemptID, receipt); err != nil {
		return err
	}
	for _, name := range []string{"pending-submission.json", "submission-plan.json", "submission-plan.sha256", "lease.json"} {
		if err = os.Remove(filepath.Join(paths.Control, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func resumeRunnerSubmissionPlan(cfg remoteRunnerConfig, config, project string, binding remote.Binding, planPath string) (any, error) {
	if !filepath.IsAbs(planPath) {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "provide an absolute submission plan path")
	}
	real, err := filepath.EvalSymlinks(planPath)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(cfg.RuntimeDir, real)
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if err != nil || len(parts) != 4 || parts[0] != "control" || parts[1] != "attempts" || !remoteRunnerID.MatchString(parts[2]) || parts[3] != "submission-plan.json" {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "submission plan is outside this runner runtime")
	}
	plan, _, err := runnerReadSubmissionPlan(real)
	if err != nil || plan.AttemptID != parts[2] {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "submission plan is invalid")
	}
	leaseBytes, err := readRunnerPrivateFile(filepath.Join(filepath.Dir(real), "lease.json"))
	if err != nil {
		return nil, err
	}
	var lease remoteRunnerLease
	if json.Unmarshal(leaseBytes, &lease) != nil || lease.RunnerID != cfg.RunnerID || lease.Token == "" {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "retained lease is invalid")
	}
	if err = runnerValidateSubmissionPlanIdentity(cfg, lease, plan); err != nil {
		return nil, err
	}
	profile, err := remote.LoadProfile(config, project, binding.Profile)
	if err != nil {
		return nil, err
	}
	if sensitive, checkErr := runnerMetadataContainsSecrets(plan, profile.Token, lease.Token); checkErr != nil {
		return nil, checkErr
	} else if sensitive {
		return nil, core.Error("RUNNER_RECOVERY_INVALID", "submission plan contains a control credential")
	}
	lookup, err := runnerCall(config, project, binding, http.MethodGet, "/runners/"+url.PathEscape(cfg.RunnerID)+"/operations/"+url.PathEscape(plan.OperationID), nil)
	if err != nil {
		return nil, runnerSubmissionPending(real, "query original result", err)
	}
	paths := runnerAttemptPaths{Control: filepath.Dir(real), IO: filepath.Join(cfg.RuntimeDir, "io", "attempts", lease.AttemptID)}
	if plan.SnapshotRelative != "" {
		paths.Snapshot = filepath.Join(cfg.RuntimeDir, filepath.FromSlash(plan.SnapshotRelative))
	}
	found, ok := lookup["found"].(bool)
	if !ok {
		return nil, core.Error("REMOTE_RESPONSE_INVALID", "result operation lookup is invalid")
	}
	if found {
		if lookup["operation_id"] != plan.OperationID {
			return nil, core.Error("IDEMPOTENCY_CONFLICT", "stored result differs from the retained plan")
		}
		if err = runnerEnsureSubmissionChain(cfg, project, binding, lease, plan, nil); err != nil {
			return nil, err
		}
		if err = runnerValidateSubmissionPlan(cfg, lease, plan); err != nil {
			return nil, err
		}
		if err = runnerFinishSubmission(cfg, project, binding, lease, paths, lookup); err != nil {
			return nil, err
		}
		return remoteOutput(lookup), nil
	}
	deadline, err := runnerHeartbeatDeadline(cfg, config, project, binding, lease)
	if err != nil {
		return nil, runnerSubmissionPending(real, "renew retained lease", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	guard := newRunnerLeaseGuard(ctx, deadline)
	done := guard.watch(20*time.Second, cancel, func() (time.Time, error) {
		return runnerHeartbeatDeadlineGuarded(cfg, config, project, binding, lease, guard)
	})
	if err = runnerEnsureSubmissionChain(cfg, project, binding, lease, plan, guard); err != nil {
		cancel()
		<-done
		return nil, err
	}
	receipt, err := runnerExecuteSubmissionPlan(cfg, config, project, binding, lease, plan, paths, guard)
	cancel()
	<-done
	if err != nil {
		return nil, err
	}
	if err = runnerFinishSubmission(cfg, project, binding, lease, paths, receipt); err != nil {
		return nil, err
	}
	return remoteOutput(receipt), nil
}
