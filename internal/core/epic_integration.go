package core

import (
	"ael/internal/store"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type IntegratedLeaf struct {
	ID                  string               `json:"id"`
	Candidate           string               `json:"candidate"`
	Definition          int                  `json:"definition_revision"`
	Plan                int                  `json:"plan_revision"`
	Proof               *CandidateProof      `json:"proof,omitempty"`
	Method              string               `json:"method"`
	GitAncestryVerified bool                 `json:"git_ancestry_verified,omitempty"`
	Rechecks            []IntegrationRecheck `json:"rechecks,omitempty"`
}

type IntegrationRecheck struct {
	LeafCheck   string `json:"leaf_check"`
	ParentCheck string `json:"parent_check"`
}

type IntegrationSupport struct {
	ID         string          `json:"id"`
	Candidate  string          `json:"candidate"`
	Definition int             `json:"definition_revision"`
	Plan       int             `json:"plan_revision"`
	Proof      *CandidateProof `json:"proof"`
}

// EpicIntegration records the actual parent candidate and the exact leaf
// states whose content was checked against it. It is not a passing check.
type EpicIntegration struct {
	ImpactUnknown    bool                     `json:"impact_unknown,omitempty"`
	RemoteRepository string                   `json:"remote_repository,omitempty"`
	Candidate        string                   `json:"candidate"`
	ProjectID        string                   `json:"project_id"`
	LocalRepository  string                   `json:"local_repository,omitempty"`
	GitHead          string                   `json:"git_head,omitempty"`
	ToolBuild        string                   `json:"tool_build,omitempty"`
	Files            map[string]CandidateFile `json:"files"`
	Definition       int                      `json:"definition_revision"`
	Plan             int                      `json:"plan_revision"`
	PlanDigest       string                   `json:"plan_digest,omitempty"`
	ImpactEvidence   string                   `json:"impact_evidence,omitempty"`
	ImpactHash       string                   `json:"impact_hash,omitempty"`
	Leaves           []IntegratedLeaf         `json:"leaves"`
	Supporting       []IntegrationSupport     `json:"supporting,omitempty"`
	Prepared         time.Time                `json:"prepared_at"`
	StaleReason      string                   `json:"stale_reason,omitempty"`
	WaitReason       string                   `json:"wait_reason,omitempty"`
}

// Both candidate workspaces and attempt workspaces are normally canonicalized
// on claim. Treat unknown paths conservatively rather than asserting isolation.
func overlappingWorkspaces(a, b string) bool {
	if a == "" || b == "" {
		return true
	}
	a, errA := filepath.Abs(a)
	b, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return true
	}
	if resolved, err := filepath.EvalSymlinks(a); err == nil {
		a = resolved
	}
	if resolved, err := filepath.EvalSymlinks(b); err == nil {
		b = resolved
	}
	contains := func(parent, child string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	return contains(a, b) || contains(b, a)
}

// Called under the same SQLite write transaction as the final snapshot and
// completion. A cooperating writer must register its attempt in that runtime
// before modifying a workspace. Expiration alone does not prove it has stopped.
func epicWorkspaceWait(tx *store.Tx, parent *Task) (string, error) {
	rows, err := tx.Conn.QueryContext(context.Background(), "SELECT id,body FROM tasks WHERE id<>? ORDER BY id", parent.Definition.ID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var id, body string
		if err = rows.Scan(&id, &body); err != nil {
			return "", err
		}
		var task Task
		if err = json.Unmarshal([]byte(body), &task); err != nil {
			return "", err
		}
		for _, a := range task.Attempts {
			if (a.Status == "running" || a.Status == "interrupted") && overlappingWorkspaces(parent.CandidateWorkspace, a.Workspace) {
				return fmt.Sprintf("workspace attempt %s on task %s is active or not confirmed stopped; after it stops, resume this parent", a.ID, id), nil
			}
		}
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	if err = rows.Close(); err != nil {
		return "", err
	}
	rows, err = tx.Conn.QueryContext(context.Background(), "SELECT path FROM outbox WHERE state IN ('pending','delete_pending') ORDER BY path")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		if err = rows.Scan(&path); err != nil {
			return "", err
		}
		rel, relErr := filepath.Rel(parent.CandidateWorkspace, path)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		rel = filepath.ToSlash(rel)
		skipped := false
		for _, dir := range []string{".ael/work-items", ".ael/outcomes", ".ael/records", ".ael/deleted", ".ael/imports", ".ael/bin", ".ael/tool-update.lock"} {
			if rel == dir || strings.HasPrefix(rel, dir+"/") {
				skipped = true
				break
			}
		}
		if !skipped {
			return "candidate workspace has a pending file update; flush it, then resume this parent", nil
		}
	}
	return "", rows.Err()
}

// Record overlap on the check itself in the claim transaction. Looking only
// at currently running attempts at submission is insufficient: another
// writer may have changed and restored the files, then ended or been deleted.
func (s *Service) markOverlappingEpicChecks(tx *store.Tx, taskID, attemptID, workspace string) error {
	rows, err := tx.Conn.QueryContext(context.Background(), "SELECT body FROM tasks WHERE id<>? AND status='open' AND step='verify' AND json_extract(body,'$.definition.type')='epic' ORDER BY id", taskID)
	if err != nil {
		return err
	}
	var affected []Task
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			rows.Close()
			return err
		}
		var parent Task
		if err = json.Unmarshal([]byte(body), &parent); err != nil {
			rows.Close()
			return err
		}
		changed := false
		for i := range parent.Attempts {
			a := &parent.Attempts[i]
			if a.Status == "running" && strings.HasPrefix(a.Unit, "check:") && overlappingWorkspaces(a.Workspace, workspace) {
				a.WorkspaceConflict = fmt.Sprintf("workspace attempt %s on task %s overlapped this check", attemptID, taskID)
				changed = true
			}
		}
		if changed {
			affected = append(affected, parent)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for i := range affected {
		if err = s.save(tx, &affected[i]); err != nil {
			return err
		}
	}
	return nil
}

func noActiveEpicPlan(tx *store.Tx, parent string) error {
	var active string
	err := tx.Row("SELECT body FROM metadata WHERE key=?", planActiveKey(parent)).Scan(&active)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	return Error("PLAN_APPLY_INCOMPLETE", "finish or recover the active decomposition plan")
}

func epicMembersTx(tx *store.Tx, parent string) ([]Task, error) {
	rows, err := tx.Conn.QueryContext(context.Background(), "SELECT body FROM tasks WHERE json_extract(body,'$.epic_membership.parent')=? ORDER BY id", parent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []Task{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var task Task
		if err = json.Unmarshal([]byte(raw), &task); err != nil {
			return nil, err
		}
		members = append(members, task)
	}
	return members, rows.Err()
}

func epicCoverage(parent Task) error {
	for _, criterion := range parent.Definition.Acceptance {
		covered := false
		for _, check := range parent.Definition.Checks {
			if check.Required && slices.Contains(check.Covers, criterion) {
				covered = true
			}
		}
		if !covered {
			return Error("EPIC_INTEGRATION_COVERAGE_REQUIRED", criterion)
		}
	}
	if parent.EpicPlan != nil {
		plan := parent.EpicPlan
		if plan.Proposal.ParentRevision != parent.Definition.Revision {
			return Error("EPIC_PLAN_STALE", "parent acceptance changed since decomposition")
		}
		for _, criterion := range parent.Definition.Acceptance {
			covered := false
			for _, mapping := range plan.Proposal.Coverage {
				if mapping.Acceptance == criterion {
					covered = len(mapping.Leaves) > 0 || strings.TrimSpace(mapping.Integration) != ""
					for _, key := range mapping.Leaves {
						if plan.TaskIDs[key] == "" {
							return Error("EPIC_PLAN_COVERAGE_MISSING", key)
						}
					}
				}
			}
			if !covered {
				return Error("EPIC_PLAN_COVERAGE_MISSING", criterion)
			}
		}
	}
	return nil
}

func acceptedEpicLeaf(parent, leaf Task) bool {
	if leaf.Status != "completed" || leaf.Historical || leaf.Candidate == "" {
		return false
	}
	if gates(&leaf) {
		return true
	}
	return (parent.Definition.Goal == "report" || parent.Definition.Goal == "analysis") &&
		(leaf.Definition.Goal == "report" || leaf.Definition.Goal == "analysis") && ChecksComplete(&leaf)
}

func validCandidateProof(proof *CandidateProof, candidate string) bool {
	if proof == nil || proof.Candidate != candidate || proof.Files == nil || fingerprintCandidateFiles(proof.Files, proof.Head) != proof.Candidate {
		return false
	}
	before := make(map[string]CandidateFile, len(proof.Files))
	for path, file := range proof.Files {
		before[path] = file
	}
	seen := map[string]bool{}
	for _, change := range proof.Changes {
		if change.Path == "" || seen[change.Path] {
			return false
		}
		seen[change.Path] = true
		actual, exists := proof.Files[change.Path]
		if (change.After == nil && exists) || (change.After != nil && (!exists || actual != *change.After)) {
			return false
		}
		if change.Before == nil {
			delete(before, change.Path)
		} else {
			before[change.Path] = *change.Before
		}
	}
	return fingerprintCandidateFiles(before, proof.InputHead) == proof.Input
}

func scopeContainsPath(scope []string, path string) bool {
	for _, item := range scope {
		clean := filepath.ToSlash(filepath.Clean(strings.ReplaceAll(item, "\\", "/")))
		if clean == "." || clean == path || strings.HasPrefix(path, clean+"/") || strings.HasPrefix(clean, path+"/") {
			return true
		}
	}
	return false
}

func builtInExcludedScope(scope string) bool {
	clean := filepath.ToSlash(filepath.Clean(strings.ReplaceAll(scope, "\\", "/")))
	for _, part := range strings.Split(clean, "/") {
		if part == ".git" || part == "node_modules" || part == ".DS_Store" {
			return true
		}
	}
	for _, skipped := range []string{".ael/work-items", ".ael/outcomes", ".ael/records", ".ael/deleted", ".ael/imports", ".ael/bin", ".ael/tool-update.lock"} {
		if clean == skipped || strings.HasPrefix(clean, skipped+"/") {
			return true
		}
	}
	return false
}

func integrationImpact(parent Task, snapshot candidateSnapshot) (bool, string, string, error) {
	impact := parent.Definition.IntegrationImpact
	if impact == nil || !impact.Complete {
		return false, "", "", nil
	}
	path := filepath.ToSlash(filepath.Clean(impact.Evidence))
	if filepath.IsAbs(impact.Evidence) || !strings.HasPrefix(path, ".ael/integration-evidence/") {
		return false, "", "", Error("EPIC_IMPACT_EVIDENCE_REQUIRED", "use a project .ael/integration-evidence/ file")
	}
	file, ok := snapshot.Files[path]
	if !ok || file.Hash == store.Hash(nil) {
		return false, "", "", Error("EPIC_IMPACT_EVIDENCE_REQUIRED", path)
	}
	for _, check := range parent.Definition.Checks {
		if check.Required && len(check.Affected) == 0 {
			return false, "", "", Error("EPIC_IMPACT_INCOMPLETE", "every required parent check needs an affected-task declaration")
		}
	}
	return true, path, file.Hash, nil
}

func epicRechecks(parent, leaf Task) ([]IntegrationRecheck, error) {
	rechecks := []IntegrationRecheck{}
	for _, required := range leaf.Definition.Checks {
		if !required.Required {
			continue
		}
		match := ""
		for _, check := range parent.Definition.Checks {
			if !check.Required || !slices.Contains(check.Affected, leaf.Definition.ID) || check.Kind != required.Kind || EffectiveCheckMethod(check) != EffectiveCheckMethod(required) || !slices.Equal(check.Command, required.Command) || check.Timeout != required.Timeout || !slices.Equal(check.Artifacts, required.Artifacts) {
				continue
			}
			if EffectiveCheckMethod(required) != "command" && !slices.Equal(check.Covers, required.Covers) {
				continue
			}
			match = check.ID
			break
		}
		if match == "" {
			return nil, Error("EPIC_RECHECK_REQUIRED", leaf.Definition.ID+": "+required.ID+"; the leaf stays completed: edit the parent definition to add a required check with the same kind, method, command, timeout and artifacts and list \""+leaf.Definition.ID+"\" in its affected_tasks, then integrate again")
		}
		rechecks = append(rechecks, IntegrationRecheck{LeafCheck: required.ID, ParentCheck: match})
	}
	if len(rechecks) == 0 {
		return nil, Error("EPIC_RECHECK_REQUIRED", leaf.Definition.ID+": no required check")
	}
	return rechecks, nil
}

func gitBlob(root, head, path string) ([]byte, bool) {
	if !validGitObjectID(head) || filepath.IsAbs(path) || path == "" || strings.HasPrefix(filepath.Clean(path), "..") {
		return nil, false
	}
	object := head + ":" + filepath.ToSlash(path)
	rawSize, err := gitCommand(root, "cat-file", "-s", object).Output()
	if err != nil {
		return nil, false
	}
	size, err := strconv.Atoi(strings.TrimSpace(string(rawSize)))
	if err != nil || size < 0 || size > 1<<20 {
		return nil, false
	}
	data, err := gitCommand(root, "cat-file", "blob", object).Output()
	return data, err == nil && len(data) == size
}

// An overlapping text change is accepted only when the original committed
// before/after blobs match the recorded hashes and the final file preserves
// the after-lines in order without restoring removed lines. Uncommitted,
// binary or oversized overlaps require a separately provable repair.
func preservesOverlappingChange(root string, proof *CandidateProof, change CandidateChange, snapshot candidateSnapshot) bool {
	if change.After == nil {
		_, exists := snapshot.Files[change.Path]
		return !exists
	}
	current, exists := snapshot.Files[change.Path]
	if !exists || current.Mode != change.After.Mode || proof.Head == "" || proof.InputHead == "" {
		return false
	}
	after, ok := gitBlob(root, proof.Head, change.Path)
	if !ok || store.Hash(after) != change.After.Hash {
		return false
	}
	var before []byte
	if change.Before != nil {
		before, ok = gitBlob(root, proof.InputHead, change.Path)
		if !ok || store.Hash(before) != change.Before.Hash {
			return false
		}
	}
	file, err := store.SafePath(root, change.Path)
	if err != nil {
		return false
	}
	final, err := os.ReadFile(file)
	if err != nil || len(final) > 1<<20 || store.Hash(final) != current.Hash || !utf8.Valid(before) || !utf8.Valid(after) || !utf8.Valid(final) || strings.ContainsRune(string(before), '\x00') || strings.ContainsRune(string(after), '\x00') || strings.ContainsRune(string(final), '\x00') {
		return false
	}
	oldLines, afterLines, finalLines := strings.Split(string(before), "\n"), strings.Split(string(after), "\n"), strings.Split(string(final), "\n")
	if change.Before == nil {
		oldLines = nil
	}
	position := 0
	for _, line := range finalLines {
		if position < len(afterLines) && line == afterLines[position] {
			position++
		}
	}
	if position != len(afterLines) {
		return false
	}
	oldCounts, afterCounts, finalCounts := map[string]int{}, map[string]int{}, map[string]int{}
	for _, line := range oldLines {
		oldCounts[line]++
	}
	for _, line := range afterLines {
		afterCounts[line]++
	}
	for _, line := range finalLines {
		finalCounts[line]++
	}
	for line, count := range oldCounts {
		if count > afterCounts[line] && finalCounts[line] > afterCounts[line] {
			return false
		}
	}
	return true
}

type leafPathChange struct {
	leaf   string
	change CandidateChange
}

// supersedingLeaves proves that a later accepted leaf started from this
// result (its recorded input equals this result) and that the chain of such
// leaves ends in the current parent content. The superseded leaf is still
// rechecked on the parent candidate; its own acceptance is never rewritten.
func supersedingLeaves(from *CandidateFile, final *CandidateFile, history []leafPathChange, exclude string) []string {
	return supersedingChain(from, final, history, exclude, make([]bool, len(history)))
}

func supersedingChain(from *CandidateFile, final *CandidateFile, history []leafPathChange, exclude string, seen []bool) []string {
	for i, next := range history {
		if seen[i] || next.leaf == exclude || !sameCandidateFile(next.change.Before, from) || sameCandidateFile(next.change.Before, next.change.After) {
			continue
		}
		seen[i] = true
		if sameCandidateFile(next.change.After, final) {
			return []string{next.leaf}
		}
		if chain := supersedingChain(next.change.After, final, history, exclude, seen); chain != nil {
			return append([]string{next.leaf}, chain...)
		}
	}
	return nil
}

func integratedLeaves(parent Task, members []Task, snapshot candidateSnapshot, impactComplete bool, impactPath, parentRepo, root string, supporting *[]IntegrationSupport) ([]IntegratedLeaf, error) {
	leaves := []IntegratedLeaf{}
	changedBy := map[string]map[string]bool{}
	history := map[string][]leafPathChange{}
	optional := map[string]Task{}
	usedOptional := map[string]bool{}
	for _, leaf := range members {
		if !acceptedEpicLeaf(parent, leaf) || !validCandidateProof(leaf.CandidateProof, leaf.Candidate) {
			continue
		}
		if !leaf.EpicMembership.Required {
			if len(leaf.UnboundArtifacts) > 0 || len(leaf.CandidateProof.UnboundArtifacts) > 0 {
				continue
			}
			if leaf.CandidateProof.Head != "" && (snapshot.Head == "" || parentRepo == "" || (!gitContainsCommit(root, leaf.CandidateProof.Head, snapshot.Head) && leaf.CandidateProof.LocalRepository != parentRepo)) {
				continue
			}
			optional[leaf.Definition.ID] = leaf
		}
		for _, change := range leaf.CandidateProof.Changes {
			history[change.Path] = append(history[change.Path], leafPathChange{leaf: leaf.Definition.ID, change: change})
			if !leaf.EpicMembership.Required {
				actual, exists := snapshot.Files[change.Path]
				if (change.After == nil && exists) || (change.After != nil && (!exists || actual != *change.After)) {
					continue
				}
			}
			if changedBy[change.Path] == nil {
				changedBy[change.Path] = map[string]bool{}
			}
			changedBy[change.Path][leaf.Definition.ID] = true
		}
	}
	for _, leaf := range members {
		if !leaf.EpicMembership.Required {
			continue
		}
		if !acceptedEpicLeaf(parent, leaf) {
			return nil, Error("EPIC_REQUIRED_LEAF_NOT_ACCEPTED", leaf.Definition.ID)
		}
		if len(leaf.UnboundArtifacts) > 0 {
			return nil, Error("EPIC_ARTIFACT_NOT_BOUND", leaf.Definition.ID+": "+strings.Join(leaf.UnboundArtifacts, ", "))
		}
		if leaf.CandidateProof != nil && len(leaf.CandidateProof.UnboundArtifacts) > 0 {
			return nil, Error("EPIC_ARTIFACT_NOT_BOUND", leaf.Definition.ID+": "+strings.Join(leaf.CandidateProof.UnboundArtifacts, ", "))
		}
		for _, attempt := range leaf.Attempts {
			if attempt.Unit != "execute" || attempt.Status != "pass" || attempt.Candidate != leaf.Candidate {
				continue
			}
			for _, ref := range attempt.Outputs {
				if strings.HasPrefix(ref, "runtime:") {
					continue
				}
				path := filepath.ToSlash(filepath.Clean(ref))
				if _, ok := snapshot.Files[path]; !ok {
					return nil, Error("EPIC_LEAF_NOT_INCLUDED", leaf.Definition.ID+": "+path)
				}
			}
		}
		exclusions := slices.Clone(snapshot.Exclusions)
		if leaf.CandidateProof != nil {
			exclusions = append(exclusions, leaf.CandidateProof.Exclusions...)
		}
		if len(exclusions) > 0 && len(leaf.Definition.Scope) == 0 {
			return nil, Error("EPIC_EXCLUDED_SCOPE_UNKNOWN", leaf.Definition.ID+": scope is not explicit")
		}
		for _, scope := range leaf.Definition.Scope {
			clean := filepath.ToSlash(filepath.Clean(strings.ReplaceAll(scope, "\\", "/")))
			if builtInExcludedScope(scope) {
				return nil, Error("EPIC_EXCLUDED_SCOPE_UNKNOWN", leaf.Definition.ID+": "+scope)
			}
			if (filepath.IsAbs(scope) || clean == ".." || strings.HasPrefix(clean, "../") || clean == "." || strings.ContainsAny(clean, "*?[]")) && len(exclusions) > 0 {
				return nil, Error("EPIC_EXCLUDED_SCOPE_UNKNOWN", leaf.Definition.ID+": "+scope)
			}
			for _, excluded := range exclusions {
				if clean == excluded || strings.HasPrefix(clean, excluded+"/") || strings.HasPrefix(excluded, clean+"/") {
					return nil, Error("EPIC_EXCLUDED_SCOPE_UNKNOWN", leaf.Definition.ID+": "+excluded)
				}
			}
		}
		if impactPath != "" && scopeContainsPath(leaf.Definition.Scope, impactPath) {
			return nil, Error("EPIC_IMPACT_OVERLAPS_SCOPE", leaf.Definition.ID+": "+impactPath)
		}
		item := IntegratedLeaf{ID: leaf.Definition.ID, Candidate: leaf.Candidate, Definition: leaf.Definition.Revision, Plan: leaf.Plan.Revision}
		forceRecheck := false
		if leaf.CandidateProof != nil && leaf.CandidateProof.Head != "" {
			if snapshot.Head == "" || parentRepo == "" {
				return nil, Error("EPIC_REPOSITORY_UNVERIFIED", leaf.Definition.ID)
			}
			item.GitAncestryVerified = gitContainsCommit(root, leaf.CandidateProof.Head, snapshot.Head)
			if !item.GitAncestryVerified && leaf.CandidateProof.LocalRepository != parentRepo {
				return nil, Error("EPIC_REPOSITORY_UNVERIFIED", leaf.Definition.ID)
			}
			if !item.GitAncestryVerified {
				forceRecheck = true
			}
		} else if snapshot.Head != "" {
			if leaf.Candidate != snapshot.Fingerprint || leaf.CandidateWorkspace != root {
				return nil, Error("EPIC_REPOSITORY_UNVERIFIED", leaf.Definition.ID)
			}
		} else if leaf.CandidateWorkspace != "" && leaf.CandidateWorkspace != root {
			forceRecheck = true
		}
		if leaf.Candidate == snapshot.Fingerprint {
			item.Method = "same_candidate"
			if forceRecheck {
				var err error
				item.Rechecks, err = epicRechecks(parent, leaf)
				if err != nil {
					return nil, err
				}
				item.Method = "same_candidate_rechecked"
			}
		} else {
			proof := leaf.CandidateProof
			if !validCandidateProof(proof, leaf.Candidate) {
				return nil, Error("EPIC_LEAF_INCLUSION_UNKNOWN", leaf.Definition.ID+": no retained content change proof")
			}
			unknownImpact, declaredAffected := !impactComplete, false
			ownChanges := map[string]CandidateChange{}
			for _, change := range proof.Changes {
				ownChanges[change.Path] = change
			}
			for _, check := range parent.Definition.Checks {
				if !check.Required {
					continue
				}
				declaredAffected = declaredAffected || slices.Contains(check.Affected, leaf.Definition.ID)
			}
			needsRecheck := forceRecheck
			for _, change := range candidateChanges(proof.Files, snapshot.Files) {
				if change.Path == impactPath && impactPath != "" {
					continue
				}
				if change.Path == ".git" && parentRepo != "" && (proof.LocalRepository == parentRepo || item.GitAncestryVerified) {
					continue
				}
				owners := changedBy[change.Path]
				if len(owners) == 0 {
					return nil, Error("EPIC_LEAF_BASELINE_MISMATCH", leaf.Definition.ID+": "+change.Path)
				}
				for owner := range owners {
					if _, ok := optional[owner]; ok {
						usedOptional[owner] = true
					}
				}
				if own, ok := ownChanges[change.Path]; ok && owners[leaf.Definition.ID] {
					if chain := supersedingLeaves(own.After, change.After, history[change.Path], leaf.Definition.ID); chain != nil {
						// A later accepted leaf demonstrably built on this result;
						// the content is re-proven by the mapped parent recheck.
						for _, id := range chain {
							if _, ok := optional[id]; ok {
								usedOptional[id] = true
							}
						}
						needsRecheck = true
						continue
					}
				}
				if owners[leaf.Definition.ID] && len(owners) == 1 {
					return nil, Error("EPIC_LEAF_NOT_INCLUDED", leaf.Definition.ID+": "+change.Path+"; the parent candidate lost this accepted change and no later accepted leaf records it as its input")
				}
				if owners[leaf.Definition.ID] && !preservesOverlappingChange(root, proof, ownChanges[change.Path], snapshot) {
					return nil, Error("EPIC_LEAF_NOT_INCLUDED", leaf.Definition.ID+": overlapping change cannot be proven: "+change.Path+"; no later accepted leaf records this result as its input")
				}
				if scopeContainsPath(leaf.Definition.Scope, change.Path) || declaredAffected || unknownImpact {
					needsRecheck = true
				}
			}
			item.Method, item.Proof = "content_changes", proof
			if needsRecheck {
				var err error
				item.Rechecks, err = epicRechecks(parent, leaf)
				if err != nil {
					return nil, err
				}
				item.Method = "content_changes_rechecked"
			}
		}
		leaves = append(leaves, item)
	}
	if len(leaves) == 0 {
		return nil, Error("EPIC_REQUIRED_LEAF_MISSING", "add at least one required leaf")
	}
	for _, leaf := range members {
		if usedOptional[leaf.Definition.ID] {
			*supporting = append(*supporting, IntegrationSupport{ID: leaf.Definition.ID, Candidate: leaf.Candidate, Definition: leaf.Definition.Revision, Plan: leaf.Plan.Revision, Proof: leaf.CandidateProof})
		}
	}
	return leaves, nil
}

func validSupportingLeaves(parent Task, members []Task) bool {
	if parent.Integration == nil {
		return false
	}
	byID := make(map[string]Task, len(members))
	for _, member := range members {
		byID[member.Definition.ID] = member
	}
	for _, support := range parent.Integration.Supporting {
		leaf, ok := byID[support.ID]
		if !ok || leaf.EpicMembership == nil || leaf.EpicMembership.Required || !acceptedEpicLeaf(parent, leaf) || leaf.Candidate != support.Candidate || leaf.Definition.Revision != support.Definition || leaf.Plan.Revision != support.Plan || !equivalent(leaf.CandidateProof, support.Proof) {
			return false
		}
	}
	return true
}

// PrepareEpicIntegration freezes the current parent workspace as the candidate
// only after actual required leaf changes are shown to be present. Checks are
// still executed separately on that exact candidate.
func (s *Service) PrepareEpicIntegration(id string) (Next, error) {
	if err := s.active(); err != nil {
		return Next{}, err
	}
	parent, err := s.Get(id)
	if err != nil {
		return Next{}, err
	}
	if parent.Definition.Type != "epic" || parent.Status != "open" {
		return Next{}, Error("EPIC_NOT_OPEN", id)
	}
	var basis []BasisReference
	if s.ResolveBasis != nil {
		basis, err = s.ResolveBasis(parent.Definition)
		if err != nil {
			return Next{}, err
		}
	}
	if s.ConstraintCheck != nil {
		if reasons := s.ConstraintCheck(parent.Definition); len(reasons) > 0 {
			return Next{}, Error("EPIC_BASIS_BLOCKED", strings.Join(reasons, "; "))
		}
	}
	snapshot, err := captureCandidate(s.Root)
	if err != nil {
		return Next{}, err
	}
	parentRepo, err := localGitRepository(s.Root, snapshot.Head)
	if err != nil {
		return Next{}, err
	}
	var project struct {
		ID string `json:"project_id"`
	}
	rawProject, err := os.ReadFile(filepath.Join(s.Root, ".ael", "project.yaml"))
	if err != nil || json.Unmarshal(rawProject, &project) != nil || project.ID == "" {
		return Next{}, Error("EPIC_PROJECT_ID_REQUIRED", "cannot identify the local project")
	}
	err = s.Store.Write(func(tx *store.Tx) error {
		if err := noActiveEpicPlan(tx, id); err != nil {
			return err
		}
		current, err := load(tx, id)
		if err != nil {
			return err
		}
		if current.Status != "open" || current.DefinitionHash != parent.DefinitionHash || current.Plan.Revision != parent.Plan.Revision || current.Paused || current.Confirmation != nil || hasWriter(current) {
			return Error("EPIC_INTEGRATION_NOT_READY", "parent changed, is paused, or has an active decision/writer")
		}
		policy, err := s.policyHash()
		if err != nil || policy != current.PolicyHash {
			return Error("EPIC_POLICY_CHANGED", "review current policy before preparing integration")
		}
		if err = epicCoverage(current); err != nil {
			return err
		}
		members, err := epicMembersTx(tx, id)
		if err != nil {
			return err
		}
		known := map[string]bool{}
		for _, member := range members {
			known[member.Definition.ID] = true
		}
		for _, check := range current.Definition.Checks {
			for _, affected := range check.Affected {
				if !known[affected] {
					return Error("EPIC_AFFECTED_TASK_UNKNOWN", check.ID+": "+affected)
				}
			}
		}
		impactComplete, impactPath, impactHash, err := integrationImpact(current, snapshot)
		if err != nil {
			return err
		}
		var supporting []IntegrationSupport
		leaves, err := integratedLeaves(current, members, snapshot, impactComplete, impactPath, parentRepo, s.Root, &supporting)
		if err != nil {
			return err
		}
		actual, err := Fingerprint(s.Root)
		if err != nil || actual != snapshot.Fingerprint {
			return Error("EPIC_CANDIDATE_CHANGED", "workspace changed while preparing integration")
		}
		digest := ""
		if current.EpicPlan != nil {
			digest = current.EpicPlan.Digest
		}
		if current.Integration != nil && current.Integration.StaleReason == "" && current.Step == "verify" && current.Candidate == snapshot.Fingerprint && current.Integration.Plan == current.Plan.Revision && current.Integration.Candidate == snapshot.Fingerprint && current.Integration.Definition == current.Definition.Revision && current.Integration.PlanDigest == digest && current.Integration.ImpactEvidence == impactPath && current.Integration.ImpactHash == impactHash && equivalent(current.Integration.Leaves, leaves) && equivalent(current.Integration.Supporting, supporting) {
			return nil
		}
		current.Plan.Revision++
		current.Plan.Steps = []string{"define", "integrate", "verify"}
		if current.Definition.Goal == "released" || current.Definition.Goal == "merged" {
			current.Plan.Steps = append(current.Plan.Steps, "delivery")
		}
		current.EvidenceHistory = append(current.EvidenceHistory, current.Evidence...)
		current.Evidence = nil
		current.Candidate, current.CandidateWorkspace = snapshot.Fingerprint, s.Root
		current.Basis = basis
		current.Integration = &EpicIntegration{Candidate: snapshot.Fingerprint, ProjectID: project.ID, LocalRepository: parentRepo, GitHead: snapshot.Head, ToolBuild: s.ToolBuild, Files: snapshot.Files, Definition: current.Definition.Revision, Plan: current.Plan.Revision, PlanDigest: digest, ImpactEvidence: impactPath, ImpactHash: impactHash, Leaves: leaves, Supporting: supporting, Prepared: time.Now().UTC()}
		current.Step, current.Route, current.Failure = "verify", "", ""
		if err = s.save(tx, &current); err != nil {
			return err
		}
		return tx.Event(id, "epic_integration_prepared", current.Integration)
	})
	if err != nil {
		return Next{}, err
	}
	if err = s.Store.Flush(); err != nil {
		return Next{}, err
	}
	return s.Next(id)
}

func validateEpicIntegration(tx *store.Tx, parent *Task) error {
	integration := parent.Integration
	if integration == nil || integration.RemoteRepository != "" || fingerprintCandidateFiles(integration.Files, integration.GitHead) != integration.Candidate {
		return Error("EPIC_INTEGRATION_STALE", "prepare the exact local parent candidate again")
	}
	return validateEpicIntegrationState(tx, parent)
}

func validateEpicIntegrationState(tx *store.Tx, parent *Task) error {
	if err := noActiveEpicPlan(tx, parent.Definition.ID); err != nil {
		return err
	}
	integration := parent.Integration
	if integration == nil || integration.StaleReason != "" || integration.Candidate != parent.Candidate || integration.Definition != parent.Definition.Revision || integration.Plan != parent.Plan.Revision || integration.ProjectID == "" || integration.Files == nil {
		return Error("EPIC_INTEGRATION_STALE", "prepare the exact parent candidate again")
	}
	if err := epicCoverage(*parent); err != nil {
		return err
	}
	digest := ""
	if parent.EpicPlan != nil {
		digest = parent.EpicPlan.Digest
	}
	if digest != integration.PlanDigest {
		return Error("EPIC_PLAN_STALE", "decomposition changed after integration preparation")
	}
	members, err := epicMembersTx(tx, parent.Definition.ID)
	if err != nil {
		return err
	}
	if !validSupportingLeaves(*parent, members) {
		return Error("EPIC_INTEGRATION_STALE", "supporting optional leaf changed")
	}
	index := 0
	for _, leaf := range members {
		if !leaf.EpicMembership.Required {
			continue
		}
		if !acceptedEpicLeaf(*parent, leaf) || index >= len(integration.Leaves) {
			return Error("EPIC_REQUIRED_LEAF_NOT_ACCEPTED", leaf.Definition.ID)
		}
		proof := integration.Leaves[index]
		if proof.ID != leaf.Definition.ID || proof.Candidate != leaf.Candidate || proof.Definition != leaf.Definition.Revision || proof.Plan != leaf.Plan.Revision || (proof.Method != "same_candidate" && proof.Method != "same_candidate_rechecked" && !equivalent(proof.Proof, leaf.CandidateProof)) {
			return Error("EPIC_INTEGRATION_STALE", fmt.Sprintf("leaf %s changed", leaf.Definition.ID))
		}
		index++
	}
	if index == 0 || index != len(integration.Leaves) {
		return Error("EPIC_INTEGRATION_STALE", "required scope changed")
	}
	return nil
}
