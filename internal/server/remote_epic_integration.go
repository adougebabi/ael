package server

import (
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/store"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type remoteIntegrationInput struct {
	ContextID        string `json:"context_id"`
	ImpactArtifact   string `json:"impact_artifact"`
	ExpectedRevision string `json:"expected_revision"`
	OperationID      string `json:"operation_id"`
	IdempotencyKey   string `json:"idempotency_key"`
}

func integrationCandidate(tx *store.Tx, id string) (remote.PortableCandidate, map[string]core.CandidateFile, error) {
	var candidate remote.PortableCandidate
	var body string
	if err := tx.Row("SELECT body FROM portable_candidates WHERE id=?", id).Scan(&body); err != nil {
		return candidate, nil, errors.New("CANDIDATE_NOT_FOUND")
	}
	if json.Unmarshal([]byte(body), &candidate) != nil || candidate.ID != id || remote.ValidatePortableCandidate(candidate) != nil {
		return candidate, nil, errors.New("CANDIDATE_STORAGE_ERROR")
	}
	files := map[string]core.CandidateFile{}
	for _, file := range candidate.Files {
		if file.Deleted {
			continue
		}
		mode := uint32(0644)
		if file.Executable {
			mode = 0755
		}
		files[file.Path] = core.CandidateFile{Hash: file.SHA256, Mode: mode}
	}
	return candidate, files, nil
}

func validateRemoteEpicIntegration(tx *store.Tx, parent *core.Task, project string) error {
	integration := parent.Integration
	if integration == nil || integration.RemoteRepository == "" || integration.ProjectID != project {
		return core.Error("EPIC_INTEGRATION_STALE", "portable project binding changed")
	}
	candidate, files, err := integrationCandidate(tx, parent.Candidate)
	if err != nil {
		return err
	}
	if candidate.Repository != integration.RemoteRepository || candidate.Commit != integration.GitHead || !equalIntegrationFiles(files, integration.Files) {
		return core.Error("EPIC_INTEGRATION_STALE", "parent inventory changed")
	}
	var observed string
	if err = tx.Row("SELECT candidate_id FROM portable_observations WHERE task_id=? ORDER BY rowid DESC LIMIT 1", parent.Definition.ID).Scan(&observed); err != nil || observed != parent.Candidate {
		return core.Error("EPIC_CANDIDATE_CHANGED", "read the current parent candidate")
	}
	for _, leaf := range integration.Leaves {
		manifest, _, err := integrationCandidate(tx, leaf.Candidate)
		if err != nil || manifest.Repository != candidate.Repository {
			return core.Error("EPIC_REPOSITORY_UNVERIFIED", leaf.ID)
		}
		if leaf.Method != "parent_rechecked" || len(leaf.Rechecks) == 0 {
			return core.Error("EPIC_RECHECK_REQUIRED", leaf.ID)
		}
	}
	workspace := strings.SplitN(strings.TrimPrefix(parent.CandidateWorkspace, "remote:"), "/", 2)
	if len(workspace) != 2 || !standaloneID.MatchString(workspace[1]) {
		return core.Error("EPIC_INTEGRATION_STALE", "remote workspace binding is invalid")
	}
	var held int
	if err = tx.Row("SELECT count(*) FROM remote_workspace_holds WHERE repository=? AND workspace_id=? AND state='unknown'", candidate.Repository, workspace[1]).Scan(&held); err != nil {
		return err
	}
	if held > 0 {
		return core.Error("EPIC_WORKSPACE_BUSY", "the declared workspace has unknown prior responsibility")
	}
	if err = tx.Row("SELECT count(*) FROM remote_runner_attempts a JOIN remote_runners r ON r.id=a.runner_id WHERE r.repository=? AND r.workspace_id=? AND a.task_id<>? AND a.state IN ('running','interrupted')", candidate.Repository, workspace[1], parent.Definition.ID).Scan(&held); err != nil {
		return err
	}
	if held > 0 {
		return core.Error("EPIC_WORKSPACE_BUSY", "another responsibility is using the declared workspace")
	}
	return nil
}

func equalIntegrationFiles(left, right map[string]core.CandidateFile) bool {
	if len(left) != len(right) {
		return false
	}
	for path, file := range left {
		if other, exists := right[path]; !exists || file != other {
			return false
		}
	}
	return true
}

func (p *portableStore) prepareEpicIntegration(principal standalonePrincipal, taskID string, input remoteIntegrationInput, artifacts *artifactStore) (map[string]any, error) {
	if !standaloneID.MatchString(input.ContextID) || input.ExpectedRevision == "" {
		return nil, errors.New("CONTEXT_INVALID")
	}
	return p.mutate(principal, "epic-integration-"+taskID, input.OperationID, input.IdempotencyKey, input, func(tx *store.Tx) (map[string]any, error) {
		var body string
		if tx.Row("SELECT body FROM portable_contexts WHERE id=? AND task_id=?", input.ContextID, taskID).Scan(&body) != nil {
			return nil, errors.New("CONTEXT_NOT_FOUND")
		}
		var snapshotContext map[string]any
		if json.Unmarshal([]byte(body), &snapshotContext) != nil || snapshotContext["task_revision"] != input.ExpectedRevision || snapshotContext["inventory_unverified"] == true {
			return nil, errors.New("CONTEXT_STALE")
		}
		candidateID, _ := snapshotContext["candidate_id"].(string)
		runnerID, _ := snapshotContext["runner_id"].(string)
		workspace, _ := snapshotContext["workspace_id"].(string)
		var actor, registeredWorkspace, repository string
		if tx.Row("SELECT actor,workspace_id,repository FROM remote_runners WHERE id=?", runnerID).Scan(&actor, &registeredWorkspace, &repository) != nil || actor != principal.UserID || registeredWorkspace != workspace {
			return nil, errors.New("FORBIDDEN")
		}
		candidate, files, err := integrationCandidate(tx, candidateID)
		if err != nil {
			return nil, err
		}
		if candidate.Repository != repository {
			return nil, errors.New("CANDIDATE_INVALID")
		}
		var parentBody string
		if err = tx.Row("SELECT body FROM tasks WHERE id=?", taskID).Scan(&parentBody); err != nil {
			return nil, err
		}
		var parent core.Task
		if json.Unmarshal([]byte(parentBody), &parent) != nil {
			return nil, errors.New("TASK_INVALID")
		}
		leafRepositories := map[string]string{}
		leafOutputs := map[string]map[string]core.RemoteIntegrationOutput{}
		leafChanges := map[string]core.RemoteIntegrationFiles{}
		rows, err := tx.Conn.QueryContext(context.Background(), "SELECT body FROM tasks WHERE json_extract(body,'$.epic_membership.parent')=?", taskID)
		if err != nil {
			return nil, err
		}
		leaves := []core.Task{}
		for rows.Next() {
			var raw string
			var leaf core.Task
			if err = rows.Scan(&raw); err != nil {
				break
			}
			if err = json.Unmarshal([]byte(raw), &leaf); err != nil {
				break
			}
			leaves = append(leaves, leaf)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, leaf := range leaves {
			if leaf.Candidate == "" || leaf.EpicMembership == nil || !leaf.EpicMembership.Required {
				continue
			}
			manifest, leafFiles, err := integrationCandidate(tx, leaf.Candidate)
			if err != nil {
				return nil, err
			}
			leafRepositories[leaf.Definition.ID] = manifest.Repository
			leafOutputs[leaf.Definition.ID] = map[string]core.RemoteIntegrationOutput{}
			var execution *core.Attempt
			for _, attempt := range leaf.Attempts {
				if attempt.Unit != "execute" || attempt.Status != "pass" || attempt.Candidate != leaf.Candidate {
					continue
				}
				execution = &attempt
				for _, id := range attempt.Outputs {
					item, err := scanArtifact(tx.Row("SELECT "+artifactColumns+" FROM artifact_objects WHERE id=?", id))
					if err != nil || item.Status != "finalized" || item.TaskID != leaf.Definition.ID || item.Candidate != leaf.Candidate {
						return nil, core.Error("EPIC_ARTIFACT_NOT_BOUND", leaf.Definition.ID)
					}
					if _, err = artifacts.read(item); err != nil {
						return nil, err
					}
					output := core.RemoteIntegrationOutput{Files: map[string]core.CandidateFile{}}
					for path, file := range leafFiles {
						if file.Hash == item.SHA256 {
							output.Files[path] = file
						}
					}
					if len(output.Files) > 0 {
						leafOutputs[leaf.Definition.ID][id] = output
					}
				}
			}
			if execution != nil && len(leafOutputs[leaf.Definition.ID]) == 0 {
				// The first claim in this execution revision retains the original
				// input across failed checks and an expired lease's recovery.
				for _, attempt := range leaf.Attempts {
					if attempt.Unit != "execute" || attempt.DefinitionRevision != execution.DefinitionRevision || attempt.PlanRevision != execution.PlanRevision {
						continue
					}
					baseline, before, err := integrationCandidate(tx, attempt.Input)
					if err != nil || baseline.Repository != manifest.Repository {
						return nil, core.Error("EPIC_REPOSITORY_UNVERIFIED", leaf.Definition.ID)
					}
					leafChanges[leaf.Definition.ID] = core.RemoteIntegrationFiles{Before: before, After: leafFiles}
					break
				}
			}
		}
		impactPath, impactHash := "", ""
		if parent.Definition.IntegrationImpact != nil && parent.Definition.IntegrationImpact.Complete {
			item, err := scanArtifact(tx.Row("SELECT "+artifactColumns+" FROM artifact_objects WHERE id=?", input.ImpactArtifact))
			impactPath = parent.Definition.IntegrationImpact.Evidence
			file, exists := files[impactPath]
			if err != nil || !exists || item.Status != "finalized" || item.TaskID != taskID || item.Candidate != candidateID || item.SHA256 != file.Hash {
				return nil, core.Error("EPIC_IMPACT_EVIDENCE_REQUIRED", "upload the declared impact file for this candidate")
			}
			if _, err = artifacts.read(item); err != nil {
				return nil, err
			}
			impactHash = item.SHA256
		}
		basis, err := portableContextBasis(tx, input.ContextID, artifacts)
		if err != nil {
			return nil, err
		}
		prepared, err := p.service.PrepareRemoteEpicIntegrationTx(tx, taskID, input.ExpectedRevision, core.RemoteIntegrationSnapshot{InputsKnown: snapshotContext["inputs_known"] == true, Candidate: candidateID, Repository: repository, Project: p.project, Head: candidate.Commit, Workspace: "remote:" + runnerID + "/" + workspace, Files: files, LeafRepositories: leafRepositories, LeafOutputs: leafOutputs, LeafFiles: leafChanges, Basis: basis, ImpactEvidence: impactPath, ImpactHash: impactHash})
		if err != nil {
			return nil, err
		}
		return map[string]any{"task": core.RemoteTaskView(prepared), "revision": core.RemoteRevision(prepared), "integration": prepared.Integration, "prepared": true}, nil
	})
}

func (h *standaloneHandler) remoteIntegrate(w http.ResponseWriter, r *http.Request, principal standalonePrincipal, project, task string, service *core.Service) {
	p, err := h.server.portable(project, service)
	if err != nil {
		standaloneFail(w, 500, "SERVER_ERROR")
		return
	}
	var input remoteIntegrationInput
	if decodePortable(r, &input, p) != nil {
		standaloneFail(w, 400, "CONTEXT_INVALID")
		return
	}
	artifacts, err := h.server.artifacts(project, service)
	if err != nil {
		artifactError(w, err)
		return
	}
	result, err := p.prepareEpicIntegration(principal, task, input, artifacts)
	if err != nil {
		standaloneTaskError(w, err)
		return
	}
	if service.Store.Flush() != nil {
		standaloneFail(w, 503, "PROJECTION_BLOCKED")
		return
	}
	standaloneReply(w, 200, result)
}
