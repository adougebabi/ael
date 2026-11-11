package server

import (
	"ael/internal/core"
	"ael/internal/knowledge"
	"ael/internal/remote"
	"ael/internal/store"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

type portableContextSourceInput struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	ArtifactID string `json:"artifact_id"`
	Reason     string `json:"reason"`
}

type portableContextInput struct {
	TaskID           string                       `json:"task_id"`
	ExpectedRevision string                       `json:"expected_revision"`
	ObservationID    string                       `json:"observation_id"`
	Sources          []portableContextSourceInput `json:"sources"`
	Inventory        []portableInventoryEntry     `json:"inventory"`
	OperationID      string                       `json:"operation_id"`
	IdempotencyKey   string                       `json:"idempotency_key"`
}

type portableInventoryEntry struct {
	ID       string   `json:"id"`
	Path     string   `json:"path"`
	SHA256   string   `json:"sha256"`
	Status   string   `json:"status"`
	Kind     string   `json:"kind"`
	Paths    []string `json:"paths"`
	Tasks    []string `json:"tasks"`
	Critical bool     `json:"critical"`
	Action   string   `json:"action"`
	Target   string   `json:"target,omitempty"`
}

type portableContextSource struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	ArtifactID string `json:"artifact_id"`
	SHA256     string `json:"sha256"`
	Status     string `json:"status"`
	Kind       string `json:"kind"`
	Origin     string `json:"origin"`
	Reason     string `json:"reason"`
	Content    string `json:"content,omitempty"`
}

func (p *portableStore) context(principal standalonePrincipal, input portableContextInput, artifacts *artifactStore) (map[string]any, error) {
	if !standaloneID.MatchString(input.TaskID) || !standaloneID.MatchString(input.ObservationID) || input.ExpectedRevision == "" || input.Sources == nil || input.Inventory == nil || len(input.Sources) > 256 || len(input.Inventory) > 20000 {
		return nil, errors.New("CANDIDATE_INVALID")
	}
	return p.mutate(principal, "publish-context", input.OperationID, input.IdempotencyKey, input, func(tx *store.Tx) (map[string]any, error) {
		var taskID, taskRevision, candidateID, checkoutID, workspaceID, runnerID string
		var inputsKnown bool
		if err := tx.Row("SELECT task_id,task_revision,candidate_id,checkout_id,workspace_id,runner_id,inputs_known FROM portable_observations WHERE id=?", input.ObservationID).Scan(&taskID, &taskRevision, &candidateID, &checkoutID, &workspaceID, &runnerID, &inputsKnown); err != nil {
			return nil, errors.New("OBSERVATION_NOT_FOUND")
		}
		if taskID != input.TaskID || taskRevision != input.ExpectedRevision {
			return nil, errors.New("REVISION_CONFLICT")
		}
		task, err := artifactTask(tx, taskID)
		if err != nil {
			return nil, errors.New("TASK_NOT_FOUND")
		}
		if core.RemoteRevision(task) != input.ExpectedRevision {
			return nil, errors.New("REVISION_CONFLICT")
		}
		var candidateBody string
		if err = tx.Row("SELECT body FROM portable_candidates WHERE id=?", candidateID).Scan(&candidateBody); err != nil {
			return nil, errors.New("CANDIDATE_NOT_FOUND")
		}
		var candidate remote.PortableCandidate
		if json.Unmarshal([]byte(candidateBody), &candidate) != nil || remote.ValidatePortableCandidate(candidate) != nil {
			return nil, errors.New("CANDIDATE_STORAGE_ERROR")
		}
		files := map[string]remote.PortableFile{}
		for _, file := range candidate.Files {
			files[file.Path] = file
		}
		inventory := map[string]portableInventoryEntry{}
		inventoryIDs := map[string]bool{}
		inventoryUnverified := false
		for _, item := range input.Inventory {
			file, ok := files[item.Path]
			if !ok || !knowledge.PortableSourcePath(item.Path) || inventory[item.Path].Path != "" || item.ID == "" || len(item.ID) > 512 || inventoryIDs[item.ID] || len(item.Paths) > 1000 || len(item.Tasks) > 1000 {
				return nil, errors.New("CONTEXT_SOURCE_MISMATCH")
			}
			if file.Deleted {
				if item.Action != "deleted" || item.ID != "deleted:"+item.Path || item.SHA256 != "" || item.Status != "unclassified" || item.Kind != "reference" {
					return nil, errors.New("CONTEXT_SOURCE_MISMATCH")
				}
				inventoryUnverified = true
			} else if file.SHA256 != item.SHA256 {
				return nil, errors.New("CONTEXT_SOURCE_MISMATCH")
			}
			if !slices.Contains([]string{"spec", "decision", "research", "reference"}, item.Kind) || !slices.Contains([]string{"current", "accepted", "proposed", "rejected", "historical", "conflict", "superseded", "unclassified"}, item.Status) || !slices.Contains([]string{"connected", "historical", "conflict", "ignored", "retired", "opaque", "deleted"}, item.Action) {
				return nil, errors.New("CONTEXT_SOURCE_MISMATCH")
			}
			if !file.Deleted && item.Action == "deleted" {
				return nil, errors.New("CONTEXT_SOURCE_MISMATCH")
			}
			if item.Action == "opaque" {
				if item.Status != "unclassified" {
					return nil, errors.New("CONTEXT_SOURCE_MISMATCH")
				}
				inventoryUnverified = true
			}
			inventory[item.Path], inventoryIDs[item.ID] = item, true
		}
		for _, file := range candidate.Files {
			if knowledge.PortableSourcePath(file.Path) {
				if inventory[file.Path].Path == "" {
					return nil, fmt.Errorf("KNOWLEDGE_MISSING: %s", file.Path)
				}
			}
		}
		seenIDs, seenPaths := map[string]bool{}, map[string]bool{}
		parsedSources := map[string]knowledge.Record{}
		sources := make([]portableContextSource, 0, len(input.Sources))
		for _, source := range input.Sources {
			file, ok := files[source.Path]
			if !ok || file.Deleted || seenIDs[source.ID] || seenPaths[source.Path] || strings.TrimSpace(source.ID) == "" || len(source.ID) > 512 || strings.TrimSpace(source.Reason) == "" || len(source.Reason) > 500 || !standaloneID.MatchString(source.ArtifactID) {
				return nil, errors.New("CONTEXT_SOURCE_MISMATCH")
			}
			item, err := scanArtifact(tx.Row("SELECT "+artifactColumns+" FROM artifact_objects WHERE id=?", source.ArtifactID))
			if err != nil || item.Status != "finalized" || item.TaskID != taskID || item.SHA256 != file.SHA256 {
				return nil, errors.New("CONTEXT_SOURCE_MISMATCH")
			}
			raw, err := artifacts.read(item)
			if err != nil {
				return nil, errors.New("CONTEXT_SOURCE_UNAVAILABLE")
			}
			record, err := knowledge.ParseSnapshot(source.Path, raw)
			if err != nil || record.ID != source.ID || record.Hash != file.SHA256 {
				return nil, errors.New("CONTEXT_SOURCE_MISMATCH")
			}
			entry, ok := inventory[source.Path]
			if !ok || entry.ID != record.ID || entry.Status != record.Status || entry.Kind != record.Kind || !slices.Equal(entry.Paths, record.Paths) || !slices.Equal(entry.Tasks, record.Tasks) || (record.Critical && !entry.Critical) || entry.Action == "retired" || entry.Action == "ignored" {
				return nil, errors.New("CONTEXT_SOURCE_MISMATCH")
			}
			if record.Status == "conflict" {
				return nil, fmt.Errorf("KNOWLEDGE_CONFLICT: %s", record.ID)
			}
			seenIDs[source.ID], seenPaths[source.Path] = true, true
			parsedSources[source.Path] = record
			sources = append(sources, portableContextSource{ID: record.ID, Path: record.Path, ArtifactID: source.ArtifactID, SHA256: record.Hash, Status: record.Status, Kind: record.Kind, Origin: record.Source, Reason: source.Reason})
		}
		for _, id := range task.Definition.Knowledge {
			if !seenIDs[id] {
				return nil, fmt.Errorf("KNOWLEDGE_MISSING: %s", id)
			}
		}
		for _, entry := range inventory {
			if entry.Action == "deleted" || entry.Action == "opaque" {
				continue
			}
			if entry.Action == "retired" {
				if entry.Target == "" || inventory[entry.Target].Path == "" {
					return nil, fmt.Errorf("KNOWLEDGE_MISSING: %s", entry.Target)
				}
				if target, ok := parsedSources[entry.Target]; ok {
					prefix := entry.Path + "#sha256="
					if !strings.HasPrefix(target.Source, prefix) || !portableDigestID(strings.TrimPrefix(target.Source, prefix)) {
						return nil, errors.New("CONTEXT_SOURCE_MISMATCH")
					}
				}
				continue
			}
			if entry.Action == "ignored" {
				continue
			}
			record := knowledge.Record{ID: entry.ID, Kind: entry.Kind, Status: entry.Status, Paths: entry.Paths, Tasks: entry.Tasks, Critical: entry.Critical}
			applicable := knowledge.Applies(record, task.Definition)
			if applicable && entry.Status == "conflict" {
				return nil, fmt.Errorf("KNOWLEDGE_CONFLICT: %s", entry.ID)
			}
			if applicable && (entry.Status == "current" || entry.Status == "accepted" || slices.Contains(task.Definition.Knowledge, entry.ID)) && !seenIDs[entry.ID] {
				return nil, fmt.Errorf("KNOWLEDGE_MISSING: %s", entry.ID)
			}
		}
		for _, path := range []string{"AGENTS.md", "CLAUDE.md"} {
			if file, ok := files[path]; ok && !file.Deleted && !seenPaths[path] {
				entry := inventory[path]
				if entry.Action != "retired" || !seenPaths[entry.Target] {
					return nil, fmt.Errorf("KNOWLEDGE_MISSING: %s", path)
				}
			}
		}
		id, err := standaloneRandom("context_")
		if err != nil {
			return nil, err
		}
		created := time.Now().UTC().Format(time.RFC3339Nano)
		inventoryBytes, _ := json.Marshal(input.Inventory)
		result := map[string]any{"id": id, "task_id": taskID, "task_revision": taskRevision, "candidate_id": candidateID, "repository": candidate.Repository, "commit": candidate.Commit, "observation_id": input.ObservationID, "checkout_id": checkoutID, "workspace_id": workspaceID, "runner_id": runnerID, "inputs_known": inputsKnown, "capture_stability_proven": false, "actor": principal.Username, "created_at": created, "sources": sources, "inventory_count": len(input.Inventory), "inventory_hash": store.Hash(inventoryBytes), "inventory_origin": "client_attested", "inventory_unverified": inventoryUnverified, "check_status": "not_recorded", "operation_id": input.OperationID}
		body, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec("INSERT INTO portable_contexts(id,task_id,candidate_id,body,created) VALUES(?,?,?,?,?)", id, taskID, candidateID, string(body), created); err != nil {
			return nil, err
		}
		for _, source := range sources {
			if _, err = tx.Exec("INSERT INTO portable_context_sources(context_id,id,path,artifact_id,sha256,status,kind,reason) VALUES(?,?,?,?,?,?,?,?)", id, source.ID, source.Path, source.ArtifactID, source.SHA256, source.Status, source.Kind, source.Reason); err != nil {
				return nil, err
			}
		}
		return result, nil
	})
}

func (p *portableStore) getContext(id string, artifacts *artifactStore) (map[string]any, error) {
	if !standaloneID.MatchString(id) {
		return nil, errors.New("CONTEXT_NOT_FOUND")
	}
	var body string
	if err := p.service.Store.DB.QueryRow("SELECT body FROM portable_contexts WHERE id=?", id).Scan(&body); err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.New("CONTEXT_NOT_FOUND")
		}
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		return nil, err
	}
	taskID, ok := result["task_id"].(string)
	if !ok {
		return nil, errors.New("CONTEXT_SOURCE_UNAVAILABLE")
	}
	var taskBody string
	if err := p.service.Store.DB.QueryRow("SELECT body FROM tasks WHERE id=?", taskID).Scan(&taskBody); err != nil {
		return nil, errors.New("CONTEXT_SOURCE_UNAVAILABLE")
	}
	var task core.Task
	if err := json.Unmarshal([]byte(taskBody), &task); err != nil {
		return nil, err
	}
	reasons := []string{}
	impactUnknown := false
	if result["task_revision"] != core.RemoteRevision(task) {
		reasons = append(reasons, "task revision changed")
		impactUnknown = true
	}
	var latest string
	if err := p.service.Store.DB.QueryRow("SELECT candidate_id FROM portable_observations WHERE task_id=? ORDER BY rowid DESC LIMIT 1", taskID).Scan(&latest); err != nil {
		reasons = append(reasons, "a newer candidate observation is unavailable")
		impactUnknown = true
	} else if result["candidate_id"] != latest {
		reasons = append(reasons, "a newer candidate observation differs")
		var beforeBody, afterBody string
		beforeID, ok := result["candidate_id"].(string)
		if !ok || p.service.Store.DB.QueryRow("SELECT body FROM portable_candidates WHERE id=?", beforeID).Scan(&beforeBody) != nil || p.service.Store.DB.QueryRow("SELECT body FROM portable_candidates WHERE id=?", latest).Scan(&afterBody) != nil {
			impactUnknown = true
		} else {
			var before, after remote.PortableCandidate
			if json.Unmarshal([]byte(beforeBody), &before) != nil || json.Unmarshal([]byte(afterBody), &after) != nil || remote.ValidatePortableCandidate(before) != nil || remote.ValidatePortableCandidate(after) != nil {
				impactUnknown = true
			} else {
				diff := remote.ComparePortableCandidates(before, after)
				result["changed_paths"], result["commit_changed"], result["repository_changed"] = diff.Paths, diff.CommitChanged, diff.RepositoryChanged
				if diff.CommitChanged || diff.RepositoryChanged {
					impactUnknown = true
				}
			}
		}
	}
	result["validity"] = "current"
	if len(reasons) > 0 {
		result["validity"] = "stale"
	}
	result["validity_reasons"] = reasons
	result["impact_unknown"] = impactUnknown
	entries, ok := result["sources"].([]any)
	if !ok {
		return nil, errors.New("CONTEXT_SOURCE_UNAVAILABLE")
	}
	for _, entry := range entries {
		source, ok := entry.(map[string]any)
		if !ok {
			return nil, errors.New("CONTEXT_SOURCE_UNAVAILABLE")
		}
		id, ok := source["artifact_id"].(string)
		if !ok {
			return nil, errors.New("CONTEXT_SOURCE_UNAVAILABLE")
		}
		item, err := artifacts.get(id)
		if err != nil || item.Status != "finalized" || item.SHA256 != source["sha256"] {
			return nil, errors.New("CONTEXT_SOURCE_UNAVAILABLE")
		}
		if _, err = artifacts.read(item); err != nil {
			return nil, errors.New("CONTEXT_SOURCE_UNAVAILABLE")
		}
	}
	rows, err := p.service.Store.DB.Query("SELECT body FROM portable_checks WHERE context_id=? ORDER BY rowid", id)
	if err != nil {
		return nil, err
	}
	checks := []map[string]any{}
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			break
		}
		var check map[string]any
		if err = json.Unmarshal([]byte(body), &check); err != nil {
			break
		}
		checks = append(checks, check)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	for _, check := range checks {
		artifactID, _ := check["artifact_id"].(string)
		item, err := artifacts.get(artifactID)
		if err != nil {
			check["artifact_status"] = "missing"
			continue
		}
		check["artifact_status"] = item.Status
		if item.Status == "finalized" {
			if _, err = artifacts.read(item); err != nil {
				check["artifact_status"] = "missing"
			}
		}
	}
	result["checks"] = checks
	if len(checks) > 0 {
		result["check_status"] = "client_reported"
	}
	return result, nil
}
