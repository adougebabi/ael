package main

import (
	"ael/internal/core"
	"ael/internal/knowledge"
	"ael/internal/remote"
	"ael/internal/store"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func remoteContextCommand(cmd string, a args, config, project string, binding remote.Binding) (any, error) {
	return remoteContextCommandAtWorkspace(cmd, a, config, project, project, binding)
}

// Remote identity and profile checks remain anchored to project; candidate and
// knowledge reads may instead use a verified execution workspace.
func remoteContextCommandAtWorkspace(cmd string, a args, config, project, workspace string, binding remote.Binding) (any, error) {
	call := func(method, path string, body any) (map[string]any, error) {
		return remote.Call(config, project, binding, method, path, body, nil)
	}
	switch cmd {
	case "knowledge index":
		catalog, err := knowledge.Index(workspace)
		if err != nil {
			return nil, err
		}
		return map[string]any{"mode": "remote", "status": "indexed_locally", "counts": catalog.Counts}, nil
	case "context show":
		id, task := a.get("context", ""), a.get("task", "")
		if (id == "") == (task == "") {
			return nil, core.Error("REMOTE_REQUEST_INVALID", "provide exactly one of --context or --task")
		}
		path := "/contexts/" + url.PathEscape(id)
		if task != "" {
			path = "/contexts?task=" + url.QueryEscape(task)
		}
		result, err := call(http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		if result["status"] == "empty" {
			return remoteOutput(result), nil
		}
		repository, ok := result["repository"].(string)
		if !ok {
			return nil, core.Error("REMOTE_RESPONSE_INVALID", "missing context repository")
		}
		local, localErr := remote.CapturePortableCandidate(workspace, repository)
		matches := localErr == nil && result["candidate_id"] == local.ID
		if localErr == nil && !matches {
			if id, ok := result["candidate_id"].(string); ok {
				if prior, fetchErr := call(http.MethodGet, "/candidates/"+url.PathEscape(id), nil); fetchErr == nil {
					raw, marshalErr := json.Marshal(prior)
					var before remote.PortableCandidate
					if marshalErr == nil && json.Unmarshal(raw, &before) == nil && remote.ValidatePortableCandidate(before) == nil {
						diff := remote.ComparePortableCandidates(before, local)
						result["changed_paths"] = diff.Paths
						if diff.CommitChanged || diff.RepositoryChanged || !local.InputsKnown {
							result["impact_unknown"] = true
						}
					}
				}
			}
		}
		contextMatches := matches && result["validity"] == "current"
		usable := contextMatches && local.InputsKnown && local.StabilityProven && result["inputs_known"] == true
		result["workspace_matches"] = matches
		result["usable_for_context"] = contextMatches && result["inventory_unverified"] != true
		result["input_equivalence_proven"] = usable
		result["capture_stability_proven"] = local.StabilityProven
		result["usable_for_current_work"] = usable
		if !contextMatches && task != "" {
			reason := "task or local candidate differs"
			if !matches {
				reason = "local workspace differs from the recorded candidate"
			} else if result["validity"] != "current" {
				reason = "task revision or latest candidate observation changed"
			}
			var names []string
			switch paths := result["changed_paths"].(type) {
			case []string:
				names = paths
			case []any:
				for _, path := range paths {
					if name, ok := path.(string); ok {
						names = append(names, name)
					}
				}
			}
			if len(names) > 20 {
				names = names[:20]
			}
			if len(names) > 0 {
				reason += "; affected paths: " + strings.Join(names, ", ")
			}
			if result["impact_unknown"] == true {
				reason += "; other impact is unknown"
			}
			return nil, core.Error("CONTEXT_STALE", reason+"; read the old context by ID and publish a new one")
		}
		entries, ok := result["sources"].([]any)
		if !ok {
			return nil, core.Error("REMOTE_RESPONSE_INVALID", "missing context sources")
		}
		for _, entry := range entries {
			source, ok := entry.(map[string]any)
			if !ok {
				return nil, core.Error("REMOTE_RESPONSE_INVALID", "invalid context source")
			}
			artifactID, ok := source["artifact_id"].(string)
			if !ok {
				return nil, core.Error("REMOTE_RESPONSE_INVALID", "missing source artifact")
			}
			data, err := remote.ArtifactContent(config, project, binding, artifactID, nil)
			if err != nil {
				return nil, err
			}
			if source["sha256"] != store.Hash(data) {
				return nil, core.Error("CONTEXT_SOURCE_MISMATCH", "snapshot content does not match metadata")
			}
			source["content"] = string(data)
		}
		if matches {
			after, err := remote.CapturePortableCandidate(workspace, repository)
			if err != nil || after.ID != local.ID || (local.InputsKnown && !after.InputsKnown) {
				result["workspace_matches"], result["usable_for_context"], result["input_equivalence_proven"], result["usable_for_current_work"] = false, false, false, false
				if task != "" {
					return nil, core.Error("CONTEXT_STALE", "local workspace changed while reading the context")
				}
			}
		}
		return remoteOutput(result), nil
	case "context publish":
		taskID, observation := a.get("task", ""), a.get("observation", "")
		revision, op, key := a.get("expected-revision", ""), a.get("operation-id", ""), a.get("idempotency-key", "")
		if taskID == "" || observation == "" || revision == "" || op == "" || key == "" {
			return nil, core.Error("REMOTE_OPERATION_REQUIRED", "provide --task, --observation, --expected-revision and operation keys")
		}
		prior, err := call(http.MethodGet, "/portable-operations/"+url.PathEscape(key), nil)
		if err != nil {
			return nil, err
		}
		if prior["found"] == true {
			result, ok := prior["result"].(map[string]any)
			if !ok || result["task_id"] != taskID || result["observation_id"] != observation || result["task_revision"] != revision || result["operation_id"] != op {
				return nil, core.Error("IDEMPOTENCY_CONFLICT", "key belongs to another operation")
			}
			result["replayed"] = true
			result["usable_for_current_work"] = false
			result["reason"] = "saved receipt; inspect the context against current task and workspace before work"
			return remoteOutput(result), nil
		}
		observed, err := call(http.MethodGet, "/observations/"+url.PathEscape(observation), nil)
		if err != nil {
			return nil, err
		}
		if observed["task_id"] != taskID || observed["task_revision"] != revision {
			return nil, core.Error("REVISION_CONFLICT", "observation does not match the requested task revision")
		}
		repository, ok := observed["repository"].(string)
		if !ok {
			return nil, core.Error("REMOTE_RESPONSE_INVALID", "missing repository")
		}
		candidate, err := remote.CapturePortableCandidate(workspace, repository)
		if err != nil {
			return nil, err
		}
		if observed["candidate_id"] != candidate.ID {
			return nil, core.Error("CANDIDATE_MISMATCH", "local checkout differs from the observed candidate; capture it again")
		}
		if observed["inputs_known"] == true && !candidate.InputsKnown {
			return nil, core.Error("CANDIDATE_INPUTS_CHANGED", "ignored inputs appeared after the observation")
		}
		current, err := call(http.MethodGet, "/tasks/"+url.PathEscape(taskID), nil)
		if err != nil {
			return nil, err
		}
		if current["online"] != true || current["authoritative"] != true {
			return nil, core.Error("REMOTE_UNAVAILABLE", "current task authority is required")
		}
		if current["revision"] != revision {
			return nil, core.Error("REVISION_CONFLICT", "task changed since the observation")
		}
		rawTask, err := json.Marshal(current["task"])
		var task core.Task
		if err != nil || json.Unmarshal(rawTask, &task) != nil || task.Definition.ID != taskID {
			return nil, core.Error("REMOTE_RESPONSE_INVALID", "missing task definition")
		}
		if err = knowledge.CheckFresh(workspace); err != nil {
			return nil, err
		}
		basis, err := knowledge.Basis(workspace, task.Definition)
		if err != nil {
			return nil, err
		}
		records, err := knowledge.ReadAll(workspace)
		if err != nil {
			return nil, err
		}
		byID := map[string]knowledge.Record{}
		byPath := map[string]knowledge.Record{}
		for _, record := range records {
			byID[record.ID] = record
			byPath[record.Path] = record
		}
		catalog, err := knowledge.PortableCatalog(workspace)
		if err != nil {
			return nil, err
		}
		catalogByPath := map[string]knowledge.Source{}
		for _, source := range catalog.Sources {
			catalogByPath[source.Path] = source
		}
		files := map[string]remote.PortableFile{}
		for _, file := range candidate.Files {
			files[file.Path] = file
		}
		inventory := make([]map[string]any, 0)
		requiredIDs := map[string]bool{}
		for _, reference := range basis {
			requiredIDs[reference.ID] = true
		}
		for _, file := range candidate.Files {
			if !knowledge.PortableSourcePath(file.Path) {
				continue
			}
			if file.Deleted {
				inventory = append(inventory, map[string]any{"id": "deleted:" + file.Path, "path": file.Path, "sha256": "", "status": "unclassified", "kind": "reference", "paths": []string{}, "tasks": []string{}, "critical": false, "action": "deleted", "target": ""})
				continue
			}
			record, present := byPath[file.Path]
			catalogSource, indexed := catalogByPath[file.Path]
			action, target := "connected", ""
			if indexed {
				action, target = catalogSource.Action, catalogSource.Target
				if !present {
					record = catalogSource.Record
				}
			} else if !present {
				return nil, core.Error("KNOWLEDGE_INDEX_STALE", file.Path+": source absent from inventory")
			} else if record.Status == "historical" || record.Status == "superseded" {
				action = "historical"
			} else if record.Status == "conflict" {
				action = "conflict"
			}
			if record.Hash != file.SHA256 || record.ID == "" {
				return nil, core.Error("KNOWLEDGE_SOURCE_CHANGED", file.Path)
			}
			if !slices.Contains([]string{"spec", "decision", "research", "reference"}, record.Kind) || !slices.Contains([]string{"current", "accepted", "proposed", "rejected", "historical", "conflict", "superseded"}, record.Status) {
				if requiredIDs[record.ID] {
					return nil, core.Error("KNOWLEDGE_STATE_UNSUPPORTED", record.ID+": explicitly required source has an unrecognized state")
				}
				record.Kind, record.Status, action = "reference", "unclassified", "opaque"
			}
			inventory = append(inventory, map[string]any{"id": record.ID, "path": file.Path, "sha256": file.SHA256, "status": record.Status, "kind": record.Kind, "paths": record.Paths, "tasks": record.Tasks, "critical": record.Critical, "action": action, "target": target})
		}
		type selectedSource struct {
			reference core.BasisReference
			data      []byte
		}
		selected := make([]selectedSource, 0, len(basis))
		for _, reference := range basis {
			record, ok := byID[reference.ID]
			file, found := files[reference.Source]
			if !ok || !found || file.Deleted || file.SHA256 != reference.Version || record.Hash != reference.Version {
				return nil, core.Error("KNOWLEDGE_NOT_IN_CANDIDATE", reference.ID+": source is missing, ignored or changed")
			}
			path, err := store.SafePath(workspace, filepath.FromSlash(reference.Source))
			if err != nil {
				return nil, core.Error("KNOWLEDGE_SOURCE_UNAVAILABLE", reference.ID)
			}
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > remoteArtifactLimit {
				return nil, core.Error("KNOWLEDGE_SOURCE_UNAVAILABLE", reference.ID)
			}
			raw, err := os.ReadFile(path)
			if err != nil || store.Hash(raw) != reference.Version {
				return nil, core.Error("KNOWLEDGE_SOURCE_CHANGED", reference.ID)
			}
			selected = append(selected, selectedSource{reference: reference, data: raw})
		}
		sources := make([]map[string]any, 0, len(selected))
		for _, source := range selected {
			uploadKey := "context_" + store.Hash([]byte(key + "\n" + source.reference.ID))[:40]
			uploadArgs := args{Flags: map[string]string{"task": taskID, "source": "knowledge snapshot", "candidate": task.Candidate, "expected-revision": revision, "operation-id": uploadKey, "idempotency-key": uploadKey}}
			result, err := remoteUploadBytes(uploadArgs, config, project, binding, filepath.Base(source.reference.Source), source.data, nil)
			if err != nil {
				return nil, fmt.Errorf("%w; context source %s", err, source.reference.ID)
			}
			if result["sha256"] != source.reference.Version {
				return nil, core.Error("KNOWLEDGE_SOURCE_CHANGED", source.reference.ID)
			}
			artifactID, ok := result["id"].(string)
			if !ok {
				return nil, core.Error("REMOTE_RESPONSE_INVALID", "missing source artifact ID")
			}
			sources = append(sources, map[string]any{"id": source.reference.ID, "path": source.reference.Source, "artifact_id": artifactID, "reason": source.reference.Reason})
		}
		after, err := remote.CapturePortableCandidate(workspace, repository)
		if err != nil {
			return nil, err
		}
		if after.ID != candidate.ID {
			return nil, core.Error("CANDIDATE_MISMATCH", "workspace changed while preparing the context")
		}
		if observed["inputs_known"] == true && !after.InputsKnown {
			return nil, core.Error("CANDIDATE_INPUTS_CHANGED", "ignored inputs appeared while preparing the context")
		}
		if err = knowledge.CheckFresh(workspace); err != nil {
			return nil, err
		}
		payload := map[string]any{"task_id": taskID, "expected_revision": revision, "observation_id": observation, "sources": sources, "inventory": inventory, "operation_id": op, "idempotency_key": key}
		result, err := call(http.MethodPost, "/contexts", payload)
		return remoteOutput(result), err
	case "context record-check":
		id, taskID, checkID, artifactID := a.get("context", ""), a.get("task", ""), a.get("check", ""), a.get("artifact", "")
		revision, op, key := a.get("expected-revision", ""), a.get("operation-id", ""), a.get("idempotency-key", "")
		if id == "" || taskID == "" || checkID == "" || artifactID == "" || revision == "" || op == "" || key == "" {
			return nil, core.Error("REMOTE_OPERATION_REQUIRED", "provide context, task, check, artifact, expected revision and operation keys")
		}
		prior, err := call(http.MethodGet, "/portable-operations/"+url.PathEscape(key), nil)
		if err != nil {
			return nil, err
		}
		if prior["found"] == true {
			result, ok := prior["result"].(map[string]any)
			if !ok || result["context_id"] != id || result["task_id"] != taskID || result["check_id"] != checkID || result["artifact_id"] != artifactID || result["task_revision"] != revision || result["operation_id"] != op {
				return nil, core.Error("IDEMPOTENCY_CONFLICT", "key belongs to another operation")
			}
			result["replayed"] = true
			return remoteOutput(result), nil
		}
		context, err := call(http.MethodGet, "/contexts/"+url.PathEscape(id), nil)
		if err != nil {
			return nil, err
		}
		if context["task_id"] != taskID || context["task_revision"] != revision || context["validity"] != "current" {
			return nil, core.Error("CONTEXT_STALE", "task or candidate changed before the check report")
		}
		repository, ok := context["repository"].(string)
		if !ok {
			return nil, core.Error("REMOTE_RESPONSE_INVALID", "missing context repository")
		}
		candidate, err := remote.CapturePortableCandidate(workspace, repository)
		if err != nil {
			return nil, err
		}
		if context["candidate_id"] != candidate.ID {
			return nil, core.Error("CANDIDATE_MISMATCH", "local checkout differs from the reported check candidate")
		}
		payload := map[string]any{"task_id": taskID, "expected_revision": revision, "candidate_id": candidate.ID, "check_id": checkID, "artifact_id": artifactID, "inputs_known": candidate.InputsKnown && context["inputs_known"] == true, "operation_id": op, "idempotency_key": key}
		result, err := call(http.MethodPost, "/contexts/"+url.PathEscape(id)+"/checks", payload)
		return remoteOutput(result), err
	}
	return nil, core.Error("REMOTE_COMMAND_UNSUPPORTED", cmd)
}
