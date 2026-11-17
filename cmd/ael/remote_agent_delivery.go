package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/store"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type agentDeliveryArtifact struct {
	Path     string `json:"path"`
	Filename string `json:"filename"`
	MIME     string `json:"mime"`
	Data     []byte `json:"data"`
}

type agentDeliveryPlan struct {
	Candidate remote.PortableCandidate `json:"candidate"`
	Artifacts []agentDeliveryArtifact  `json:"artifacts"`
}

// Freeze every selected byte before the first write. The existing operation
// journal then retries this plan even if the checkout or report has changed.
func buildAgentDelivery(a args, project string, body map[string]any, secrets ...string) (*agentDeliveryPlan, error) {
	token, _ := body["token"].(string)
	secrets = append(secrets, token)
	metadata := map[string]any{}
	for field, value := range body {
		if field != "token" {
			metadata[field] = value
		}
	}
	if sensitive, err := runnerMetadataContainsSecrets(metadata, secrets...); err != nil {
		return nil, err
	} else if sensitive {
		return nil, core.Error("ARTIFACT_SENSITIVE", "result metadata contains a control credential")
	}
	if bindings, exists := body["bindings"]; exists && bindings != nil {
		items, ok := bindings.([]any)
		if !ok || len(items) != 0 {
			return nil, core.Error("AGENT_REQUEST_INVALID", "with --repository submit, use result.artifacts paths and omit bindings")
		}
	}
	candidate, err := remote.CapturePortableCandidate(project, a.get("repository", ""))
	if err != nil {
		return nil, err
	}
	if sensitive, err := runnerMetadataContainsSecrets(candidate, secrets...); err != nil {
		return nil, err
	} else if sensitive {
		return nil, core.Error("ARTIFACT_SENSITIVE", "candidate metadata contains a control credential")
	}
	if id, exists := body["candidate_id"]; exists && id != candidate.ID {
		return nil, core.Error("CANDIDATE_CHANGED", "candidate_id differs from the current checkout; inspect changes before submitting")
	}
	result, ok := body["result"].(map[string]any)
	if !ok {
		return nil, core.Error("RESULT_REQUIRED", "provide result with status, summary and selected artifact paths")
	}
	refs, err := agentArtifactPaths(result["artifacts"])
	if err != nil {
		return nil, err
	}
	if knowledge, ok := result["knowledge"].(map[string]any); ok {
		knowledgeRefs, err := agentArtifactPaths(knowledge["artifacts"])
		if err != nil {
			return nil, err
		}
		refs = append(refs, knowledgeRefs...)
	}
	plan := &agentDeliveryPlan{Candidate: candidate}
	seen := map[string]bool{}
	var total int
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		if len(seen) > 32 {
			return nil, core.Error("ARTIFACT_INVALID", "a submission supports at most 32 distinct artifact paths")
		}
		if strings.HasPrefix(ref, "runtime:") && !filepath.IsAbs(a.get("artifacts-dir", "")) {
			return nil, core.Error("ARTIFACT_INVALID", "runtime artifact paths require --artifacts-dir ABSOLUTE_PRIVATE_DIRECTORY")
		}
		data, filename, mime, err := runnerOutputFile(remoteRunnerConfig{Workspace: project}, a.get("artifacts-dir", ""), ref, candidate, secrets...)
		if err != nil {
			return nil, err
		}
		total += len(data)
		if total > 64<<20 {
			return nil, core.Error("ARTIFACT_INVALID", "selected artifacts exceed 64 MiB")
		}
		plan.Artifacts = append(plan.Artifacts, agentDeliveryArtifact{Path: ref, Filename: filename, MIME: mime, Data: data})
	}
	return plan, nil
}

func agentArtifactPaths(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, core.Error("ARTIFACT_INVALID", "result artifact paths must be an array")
	}
	refs := make([]string, 0, len(items))
	for _, item := range items {
		ref, ok := item.(string)
		if !ok || ref == "" {
			return nil, core.Error("ARTIFACT_INVALID", "each artifact must be a repository path or runtime: path")
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func executeAgentDelivery(config, project string, binding remote.Binding, path string, record agentOperationRecord) (map[string]any, error) {
	// The final child operation owns server receipt lookup. Replaying it first
	// allows an accepted submission to be recovered after its lease has ended.
	key, _ := record.Payload["idempotency_key"].(string)
	parent := store.Hash([]byte(record.RequestHash + ":" + record.DeliveryHash))
	finalPath, _, err := agentOperationLocation(config, project, binding, "submit", record.Runner, key)
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Lstat(finalPath); statErr == nil {
		unlock, err := lockAgentOperation(finalPath)
		if err != nil {
			return nil, err
		}
		defer unlock()
		final, readErr := readAgentOperation(finalPath)
		if readErr != nil {
			return nil, readErr
		}
		if final.DeliveryParent != parent {
			return nil, core.Error("IDEMPOTENCY_CONFLICT", "submit key already belongs to a different delivery")
		}
		response, err := executeAgentOperation(config, project, binding, finalPath, final, true)
		if err != nil {
			return nil, err
		}
		response["operation_file"] = path
		return response, nil
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}
	observe := map[string]any{}
	for _, field := range []string{"task_id", "attempt_id", "token", "fence", "expected_revision"} {
		observe[field] = record.Payload[field]
	}
	op := runnerPlanKey("observe", key, "delivery")
	observe["operation_id"], observe["idempotency_key"], observe["candidate"] = op, op, record.Delivery.Candidate
	if _, err := runAgentOperation(args{}, config, project, binding, "observe", record.Runner, observe); err != nil {
		return nil, fmt.Errorf("%w; retry delivery with runner retry --pending %s", err, path)
	}
	raw, _ := json.Marshal(record.Payload)
	var submit map[string]any
	_ = json.Unmarshal(raw, &submit)
	ids := map[string]string{}
	bindings := []any{}
	for _, artifact := range record.Delivery.Artifacts {
		op := runnerPlanKey("artifact", key, artifact.Path)
		flags := map[string]string{"task": fmt.Sprint(record.Payload["task_id"]), "attempt": fmt.Sprint(record.Payload["attempt_id"]), "candidate": record.Delivery.Candidate.ID, "expected-revision": fmt.Sprint(record.Payload["expected_revision"]), "mime": artifact.MIME, "source": "runner:" + record.Runner, "operation-id": op, "idempotency-key": op}
		var lease agentUploadLease
		leaseRaw, _ := json.Marshal(record.Payload)
		_ = json.Unmarshal(leaseRaw, &lease)
		receipt, err := retainAgentUpload(args{Flags: flags}, config, project, binding, artifact.Filename, artifact.Data, &runnerUploadLease{RunnerID: record.Runner, Token: lease.Token, Fence: lease.Fence})
		if err != nil {
			return nil, fmt.Errorf("%w; retry delivery with runner retry --pending %s", err, path)
		}
		id, _ := receipt["id"].(string)
		if id == "" {
			return nil, core.Error("REMOTE_RESPONSE_INVALID", "upload receipt missing artifact id")
		}
		ids[artifact.Path] = id
		bindings = append(bindings, map[string]any{"path": artifact.Path, "artifact_id": id})
	}
	result := submit["result"].(map[string]any)
	replaceAgentArtifactPaths(result, ids)
	if knowledge, ok := result["knowledge"].(map[string]any); ok {
		replaceAgentArtifactPaths(knowledge, ids)
	}
	submit["candidate_id"], submit["bindings"] = record.Delivery.Candidate.ID, bindings
	response, err := runAgentOperation(args{Flags: map[string]string{"delivery-parent": parent}}, config, project, binding, "submit", record.Runner, submit)
	if err != nil {
		return nil, fmt.Errorf("%w; retry delivery with runner retry --pending %s", err, path)
	}
	response["operation_file"] = path
	return response, nil
}

func replaceAgentArtifactPaths(object map[string]any, ids map[string]string) {
	refs, _ := agentArtifactPaths(object["artifacts"])
	artifacts := make([]string, 0, len(refs))
	for _, ref := range refs {
		artifacts = append(artifacts, ids[ref])
	}
	object["artifacts"] = artifacts
}
