package core

import (
	"ael/internal/store"
	"database/sql"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"
)

func sameDefinition(a, b Definition) bool {
	a.Revision, b.Revision = 0, 0
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	var first, second any
	_ = json.Unmarshal(left, &first)
	_ = json.Unmarshal(right, &second)
	return equivalent(normalizePlanValue(first), normalizePlanValue(second))
}

// Existing leaves are preserved unless an expected revision is provided.
// Nonempty fields in a proposal must agree with the existing definition.
func matchesSuppliedDefinition(proposed, current Definition) bool {
	left, _ := json.Marshal(proposed)
	right, _ := json.Marshal(current)
	var a, b map[string]any
	_ = json.Unmarshal(left, &a)
	_ = json.Unmarshal(right, &b)
	for key, value := range a {
		if key == "definition_revision" || isEmptyPlanValue(value) {
			continue
		}
		if !equivalent(normalizePlanValue(value), normalizePlanValue(b[key])) {
			return false
		}
	}
	return true
}

func isEmptyPlanValue(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case bool:
		return !v
	case float64:
		return v == 0
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}

func normalizePlanValue(value any) any {
	switch v := value.(type) {
	case []any:
		if len(v) == 0 {
			return nil
		}
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, normalizePlanValue(item))
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for key, item := range v {
			if !isEmptyPlanValue(item) {
				out[key] = normalizePlanValue(item)
			}
		}
		return out
	default:
		return value
	}
}

func definitionDiff(before, after Definition) []PlanFieldDiff {
	left, _ := json.Marshal(before)
	right, _ := json.Marshal(after)
	var a, b map[string]any
	_ = json.Unmarshal(left, &a)
	_ = json.Unmarshal(right, &b)
	delete(a, "definition_revision")
	delete(b, "definition_revision")
	keys := map[string]bool{}
	for key := range a {
		keys[key] = true
	}
	for key := range b {
		keys[key] = true
	}
	changed := []string{}
	for key := range keys {
		if !equivalent(normalizePlanValue(a[key]), normalizePlanValue(b[key])) {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)
	diff := make([]PlanFieldDiff, 0, len(changed))
	for _, key := range changed {
		diff = append(diff, PlanFieldDiff{Field: key, Before: a[key], After: b[key]})
	}
	return diff
}

func planNeedsConfirmation(before, after Definition) bool {
	if before.Goal != after.Goal || before.Intent != after.Intent || before.Target != after.Target || before.Environment != after.Environment || !equivalent(before.Scope, after.Scope) ||
		!equivalent(before.Acceptance, after.Acceptance) || !equivalent(before.Risk, after.Risk) || !equivalent(before.NonGoals, after.NonGoals) {
		return true
	}
	for _, old := range before.Checks {
		if !old.Required {
			continue
		}
		found := false
		for _, current := range after.Checks {
			if current.ID == old.ID && current.Required {
				found = true
			}
		}
		if !found {
			return true
		}
	}
	return false
}

func (s *Service) planDefinition(p PlanProposal, leaf PlanLeaf, ids map[string]string, old *Task) (Definition, error) {
	if old != nil && leaf.ExpectedRevision == 0 {
		return old.Definition, nil
	}
	d := leaf.Definition
	if d.Preset != "" && !slicesContains([]string{"fast", "standard", "strict", "research"}, d.Preset) {
		return d, Error("INVALID_PRESET", d.Preset)
	}
	id := ids[leaf.Key]
	if d.ID != "" && d.ID != id {
		return d, Error("PLAN_TASK_ID_MISMATCH", leaf.Key)
	}
	d.ID = id
	if old != nil {
		if d.Parent != "" && d.Parent != old.Definition.Parent {
			return d, Error("PARENT_LINK_REQUIRED", id)
		}
		d.Parent = old.Definition.Parent
		d.Revision = old.Definition.Revision
		if d.Legacy != nil && !equivalent(d.Legacy, old.Definition.Legacy) {
			return d, Error("PLAN_LEGACY_IMMUTABLE", id)
		}
		d.Legacy = old.Definition.Legacy
		alignEmptyPlanFields(&d, old.Definition)
	} else {
		if d.Parent != "" && d.Parent != p.Parent {
			return d, Error("PLAN_PARENT_MISMATCH", leaf.Key)
		}
		d.Parent = p.Parent
		d.Revision = 1
		if d.OriginalRequest == "" {
			d.OriginalRequest = p.Request
		}
	}
	if leaf.InspectionOnly || d.Type == "qa" || d.Type == "verify" {
		d.Intent = "inspect"
	}
	for _, key := range leaf.DependsOn {
		dep := ids[key]
		if !slicesContains(d.Dependencies, dep) {
			d.Dependencies = append(d.Dependencies, dep)
		}
	}
	if err := s.normalize(&d); err != nil {
		return d, err
	}
	_ = MakePlan(&d)
	if err := Validate(d); err != nil {
		return d, err
	}
	return d, nil
}

func alignEmptyPlanFields(d *Definition, old Definition) {
	if len(d.Scope) == 0 && len(old.Scope) == 0 {
		d.Scope = old.Scope
	}
	if len(d.NonGoals) == 0 && len(old.NonGoals) == 0 {
		d.NonGoals = old.NonGoals
	}
	if len(d.Acceptance) == 0 && len(old.Acceptance) == 0 {
		d.Acceptance = old.Acceptance
	}
	if len(d.Dependencies) == 0 && len(old.Dependencies) == 0 {
		d.Dependencies = old.Dependencies
	}
	if len(d.Risk) == 0 && len(old.Risk) == 0 {
		d.Risk = old.Risk
	}
	if len(d.Knowledge) == 0 && len(old.Knowledge) == 0 {
		d.Knowledge = old.Knowledge
	}
	if len(d.Legacy) == 0 && len(old.Legacy) == 0 {
		d.Legacy = old.Legacy
	}
	if len(d.Checks) == 0 && len(old.Checks) == 0 {
		d.Checks = old.Checks
	}
	for i := range d.Checks {
		for _, previous := range old.Checks {
			if d.Checks[i].ID != previous.ID {
				continue
			}
			if len(d.Checks[i].Command) == 0 && len(previous.Command) == 0 {
				d.Checks[i].Command = previous.Command
			}
			if len(d.Checks[i].Covers) == 0 && len(previous.Covers) == 0 {
				d.Checks[i].Covers = previous.Covers
			}
			if len(d.Checks[i].Artifacts) == 0 && len(previous.Artifacts) == 0 {
				d.Checks[i].Artifacts = previous.Artifacts
			}
		}
	}
}

func slicesContains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// PlanApplyResult is a durable receipt. Replaying the same plan returns this
// result, never re-creating tasks or resetting their work.
type PlanApplyResult struct {
	Status       string       `json:"status"`
	Parent       string       `json:"parent"`
	PlanID       string       `json:"plan_id"`
	ProposalHash string       `json:"proposal_hash"`
	Changes      []PlanChange `json:"changes"`
}

type planOperation struct {
	Key             string         `json:"key"`
	TaskID          string         `json:"task_id"`
	Create          bool           `json:"create"`
	Revise          bool           `json:"revise"`
	BeforeHash      string         `json:"before_hash"`
	BeforeStateHash string         `json:"before_state_hash"`
	BeforeLinkHash  string         `json:"before_link_hash"`
	Definition      Definition     `json:"definition"`
	Membership      EpicMembership `json:"membership"`
}

type planJournal struct {
	Proposal        PlanProposal      `json:"proposal"`
	ProposalHash    string            `json:"proposal_hash"`
	Digest          string            `json:"digest"`
	PolicyHash      string            `json:"policy_hash"`
	ParentHash      string            `json:"parent_hash"`
	ParentStateHash string            `json:"parent_state_hash"`
	ParentPlan      *EpicPlanSnapshot `json:"parent_plan,omitempty"`
	TaskIDs         map[string]string `json:"task_ids"`
	Operations      []planOperation   `json:"operations"`
	Changes         []PlanChange      `json:"changes"`
	Next            int               `json:"next"`
	Status          string            `json:"status"`
	Result          *PlanApplyResult  `json:"result,omitempty"`
}

func planJournalKey(parent, id string) string {
	return "decomposition:" + store.Hash([]byte(parent+"\n"+id))
}
func planActiveKey(parent string) string {
	return "decomposition-active:" + store.Hash([]byte(parent))
}

func checkPlanActive(tx *store.Tx, parent, key string) error {
	if key == "" {
		return nil
	}
	var active, raw string
	if err := tx.Row("SELECT body FROM metadata WHERE key=?", planActiveKey(parent)).Scan(&active); err != nil || active != key {
		return Error("PLAN_NOT_RESUMABLE", "plan was abandoned or replaced")
	}
	if err := tx.Row("SELECT body FROM metadata WHERE key=?", key).Scan(&raw); err != nil {
		return Error("PLAN_NOT_RESUMABLE", "plan journal is missing")
	}
	var state struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(raw), &state); err != nil || state.Status != "applying" {
		return Error("PLAN_NOT_RESUMABLE", "plan is no longer applying")
	}
	return nil
}

func (s *Service) loadPlanJournal(key string) (planJournal, bool, error) {
	var j planJournal
	var raw string
	err := s.Store.DB.QueryRow("SELECT body FROM metadata WHERE key=?", key).Scan(&raw)
	if err == sql.ErrNoRows {
		return j, false, nil
	}
	if err != nil {
		return j, false, err
	}
	if err = json.Unmarshal([]byte(raw), &j); err != nil {
		return j, false, err
	}
	return j, true, nil
}

func proposalHash(p PlanProposal) string {
	b, _ := json.Marshal(p)
	return store.Hash(b)
}

func taskStateHash(t Task) string {
	b, _ := json.Marshal(t)
	return store.Hash(b)
}

func taskBeforeLinkHash(t Task) string {
	t.Updated = time.Time{}
	t.EpicMembership = nil
	return taskStateHash(t)
}

func (s *Service) checkTaskFile(t Task) error {
	path, err := s.taskPath(t.Definition.ID)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil || store.Hash(data) != t.DefinitionHash {
		return Error("PLAN_STALE_TASK", t.Definition.ID)
	}
	return nil
}

func (s *Service) ApplyPlan(p PlanProposal, digest string) (PlanApplyResult, error) {
	var empty PlanApplyResult
	if err := s.active(); err != nil {
		return empty, err
	}
	unlock, err := s.lockPlan(p.Parent)
	if err != nil {
		return empty, err
	}
	defer unlock()
	if err := s.Store.Flush(); err != nil {
		return empty, err
	}
	key := planJournalKey(p.Parent, p.ID)
	j, found, err := s.loadPlanJournal(key)
	if err != nil {
		return empty, err
	}
	hash := proposalHash(p)
	if found {
		if j.ProposalHash != hash {
			return empty, Error("IDEMPOTENCY_CONFLICT", p.ID)
		}
		if digest != j.Digest {
			return empty, Error("PLAN_DIGEST_MISMATCH", "use the original preview digest")
		}
		if j.Status == "applied" && j.Result != nil {
			if err := s.verifyPlanSnapshot(p.Parent, &EpicPlanSnapshot{TaskIDs: j.TaskIDs}); err != nil {
				return empty, err
			}
			return *j.Result, nil
		}
		if j.Status == "restore_review_required" {
			return empty, Error("PLAN_RESTORE_REVIEW_REQUIRED", "reconcile restored tasks, then use plan recover")
		}
		if j.Status != "applying" {
			return empty, Error("PLAN_NOT_RESUMABLE", p.ID)
		}
	} else {
		root, err := s.planningTask(p.Parent)
		if err != nil {
			return empty, err
		}
		if prior := findEpicPlan(root, p.ID); prior != nil {
			if prior.ProposalHash != hash {
				return empty, Error("IDEMPOTENCY_CONFLICT", p.ID)
			}
			if digest != prior.Digest {
				return empty, Error("PLAN_DIGEST_MISMATCH", "use the original preview digest")
			}
			if err := s.verifyPlanSnapshot(p.Parent, prior); err != nil {
				return empty, err
			}
			return PlanApplyResult{Status: "applied", Parent: p.Parent, PlanID: p.ID, ProposalHash: hash, Changes: prior.Changes}, nil
		}
		preview, err := s.PreviewPlan(p)
		if err != nil {
			return empty, err
		}
		if digest == "" || digest != preview.Digest {
			return empty, Error("PLAN_DIGEST_MISMATCH", "preview the exact current plan before applying")
		}
		j = planJournal{Proposal: p, ProposalHash: hash, Digest: digest, PolicyHash: preview.parent.PolicyHash,
			ParentHash: preview.parent.DefinitionHash, ParentStateHash: taskStateHash(preview.parent), ParentPlan: preview.parent.EpicPlan, TaskIDs: preview.ids,
			Changes: preview.Changes, Operations: []planOperation{}, Status: "applying"}
		leaves := append([]PlanLeaf(nil), p.Leaves...)
		sort.SliceStable(leaves, func(i, k int) bool { return leaves[i].Order < leaves[k].Order })
		for _, leaf := range leaves {
			var old *Task
			if leaf.ExistingID != "" {
				t := preview.existing[leaf.ExistingID]
				old = &t
			}
			d, err := s.planDefinition(p, leaf, preview.ids, old)
			if err != nil {
				return empty, err
			}
			op := planOperation{Key: leaf.Key, TaskID: preview.ids[leaf.Key], Create: old == nil,
				Definition: d, Membership: EpicMembership{Parent: p.Parent, Required: leaf.Required, Milestone: strings.TrimSpace(leaf.Milestone)}}
			if old != nil {
				op.BeforeHash = old.DefinitionHash
				op.BeforeStateHash = taskStateHash(*old)
				op.BeforeLinkHash = taskBeforeLinkHash(*old)
				op.Revise = leaf.ExpectedRevision > 0 && !sameDefinition(old.Definition, d)
				if op.Revise && hasWriter(*old) {
					return empty, Error("PLAN_TASK_BUSY", old.Definition.ID)
				}
			}
			j.Operations = append(j.Operations, op)
		}
		if j, err = s.registerPlan(key, j, preview); err != nil {
			return empty, err
		}
	}
	return s.resumePlan(key, j)
}

func (s *Service) registerPlan(key string, j planJournal, preview PlanPreview) (planJournal, error) {
	result := j
	err := s.Store.Write(func(tx *store.Tx) error {
		var raw string
		err := tx.Row("SELECT body FROM metadata WHERE key=?", key).Scan(&raw)
		if err == nil {
			if err = json.Unmarshal([]byte(raw), &result); err != nil {
				return err
			}
			if result.ProposalHash != j.ProposalHash {
				return Error("IDEMPOTENCY_CONFLICT", j.Proposal.ID)
			}
			if result.Digest != j.Digest {
				return Error("PLAN_DIGEST_MISMATCH", "use the original preview digest")
			}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		var active string
		err = tx.Row("SELECT body FROM metadata WHERE key=?", planActiveKey(j.Proposal.Parent)).Scan(&active)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if active != "" && active != key {
			return Error("PLAN_APPLY_IN_PROGRESS", "resume or abandon the existing plan before applying another")
		}
		parent, err := load(tx, j.Proposal.Parent)
		if err != nil || !equivalent(parent, preview.parent) {
			return Error("PLAN_PARENT_CHANGED", j.Proposal.Parent)
		}
		policy, err := s.policyHash()
		if err != nil || policy != j.PolicyHash {
			return Error("PLAN_STALE_POLICY", "project policy changed since preview")
		}
		checkFile := func(t Task) error {
			path, err := s.taskPath(t.Definition.ID)
			if err != nil {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil || store.Hash(b) != t.DefinitionHash {
				return Error("PLAN_STALE_TASK", t.Definition.ID)
			}
			return nil
		}
		if err = checkFile(parent); err != nil {
			return err
		}
		for id, expected := range preview.existing {
			current, err := load(tx, id)
			if err != nil || !equivalent(current, expected) {
				return Error("PLAN_TASK_CHANGED", id)
			}
			if err = checkFile(current); err != nil {
				return err
			}
		}
		for _, op := range j.Operations {
			if !op.Create {
				continue
			}
			_, err := load(tx, op.TaskID)
			if err == nil {
				return Error("TASK_EXISTS", op.TaskID)
			}
			if err != sql.ErrNoRows {
				return err
			}
			path, err := s.taskPath(op.TaskID)
			if err != nil {
				return err
			}
			if _, err = os.Lstat(path); err == nil {
				return Error("TASK_EXISTS", op.TaskID)
			} else if !os.IsNotExist(err) {
				return err
			}
			deleted, err := store.SafePath(s.Root, ".ael/deleted/"+op.TaskID+".json")
			if err != nil {
				return err
			}
			if _, err = os.Lstat(deleted); err == nil {
				return Error("TASK_ID_DELETED", op.TaskID)
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		body, err := json.Marshal(j)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?)", key, string(body)); err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", planActiveKey(j.Proposal.Parent), key); err != nil {
			return err
		}
		return tx.Event(j.Proposal.Parent, "plan_apply_started", map[string]string{"plan_id": j.Proposal.ID, "proposal_hash": j.ProposalHash})
	})
	return result, err
}

func (s *Service) resumePlan(key string, initial planJournal) (PlanApplyResult, error) {
	var empty PlanApplyResult
	j := initial
	for {
		current, found, err := s.loadPlanJournal(key)
		if err != nil {
			return empty, err
		}
		if !found || current.ProposalHash != j.ProposalHash {
			return empty, Error("PLAN_JOURNAL_CONFLICT", j.Proposal.ID)
		}
		j = current
		if j.Status == "applied" && j.Result != nil {
			return *j.Result, nil
		}
		if j.Status != "applying" {
			return empty, Error("PLAN_NOT_RESUMABLE", j.Proposal.ID)
		}
		if j.Next >= len(j.Operations) {
			break
		}
		op := j.Operations[j.Next]
		if err = s.applyPlanOperation(j, op); err != nil {
			return empty, Error("PLAN_APPLY_INCOMPLETE", "plan "+j.Proposal.ID+" stopped at "+op.Key+": "+err.Error()+"; fix the cause and retry the same plan and digest")
		}
		index := j.Next
		err = s.Store.Write(func(tx *store.Tx) error {
			var raw string
			if err := tx.Row("SELECT body FROM metadata WHERE key=?", key).Scan(&raw); err != nil {
				return err
			}
			var latest planJournal
			if err := json.Unmarshal([]byte(raw), &latest); err != nil {
				return err
			}
			if latest.ProposalHash != j.ProposalHash || latest.Status != "applying" {
				return Error("PLAN_JOURNAL_CONFLICT", j.Proposal.ID)
			}
			if latest.Next == index {
				latest.Next++
			}
			b, err := json.Marshal(latest)
			if err != nil {
				return err
			}
			_, err = tx.Exec("UPDATE metadata SET body=? WHERE key=?", string(b), key)
			return err
		})
		if err != nil {
			return empty, err
		}
	}
	return s.finishPlan(key, j)
}

func (s *Service) applyPlanOperation(j planJournal, op planOperation) error {
	policy, err := s.policyHash()
	if err != nil || policy != j.PolicyHash {
		return Error("PLAN_STALE_POLICY", "project policy changed since preview")
	}
	parent, err := s.planningTask(j.Proposal.Parent)
	if err != nil {
		return err
	}
	if parent.Status != "open" || parent.DefinitionHash != j.ParentHash || taskStateHash(parent) != j.ParentStateHash || !equivalent(parent.EpicPlan, j.ParentPlan) {
		return Error("PLAN_PARENT_CHANGED", j.Proposal.Parent)
	}
	current, err := s.planningTask(op.TaskID)
	if op.Create && err != nil && strings.HasPrefix(err.Error(), "TASK_MISSING:") {
		_, err = s.create(op.Definition, &op.Membership, j.PolicyHash, planJournalKey(j.Proposal.Parent, j.Proposal.ID))
		if err != nil {
			// Another retry may have completed this exact operation concurrently.
			current, retryErr := s.planningTask(op.TaskID)
			if retryErr == nil && sameDefinition(current.Definition, op.Definition) && equivalent(current.EpicMembership, &op.Membership) {
				return nil
			}
			return err
		}
		return nil
	}
	if err != nil {
		return err
	}
	if op.Create {
		if !sameDefinition(current.Definition, op.Definition) || !equivalent(current.EpicMembership, &op.Membership) {
			return Error("PLAN_TASK_CONFLICT", op.TaskID)
		}
		return nil
	}
	if !op.Revise && taskStateHash(current) != op.BeforeStateHash {
		if !equivalent(current.EpicMembership, &op.Membership) || taskBeforeLinkHash(current) != op.BeforeLinkHash {
			return Error("PLAN_TASK_CHANGED", op.TaskID)
		}
	}
	if op.Revise && !sameDefinition(current.Definition, op.Definition) {
		if current.DefinitionHash != op.BeforeHash || taskStateHash(current) != op.BeforeStateHash || current.Status != "open" || hasWriter(current) {
			return Error("PLAN_TASK_CHANGED", op.TaskID)
		}
		_, err = s.syncDefinitionExpected(current, op.Definition, definitionBytes(op.Definition), j.PolicyHash, true, j.Proposal.Parent, planJournalKey(j.Proposal.Parent, j.Proposal.ID), op.BeforeStateHash)
		if err != nil {
			retry, retryErr := s.planningTask(op.TaskID)
			if retryErr != nil || !sameDefinition(retry.Definition, op.Definition) {
				return err
			}
		}
		current, err = s.planningTask(op.TaskID)
		if err != nil {
			return err
		}
	}
	if current.EpicMembership == nil {
		_, err = s.linkEpic(j.Proposal.Parent, op.TaskID, op.Membership.Required, op.Membership.Milestone, j.PolicyHash, current.DefinitionHash, planJournalKey(j.Proposal.Parent, j.Proposal.ID), taskStateHash(current))
		if err != nil {
			retry, retryErr := s.planningTask(op.TaskID)
			if retryErr != nil || !equivalent(retry.EpicMembership, &op.Membership) {
				return err
			}
		}
	} else if !equivalent(current.EpicMembership, &op.Membership) {
		return Error("PLAN_MEMBERSHIP_CONFLICT", op.TaskID)
	}
	return nil
}

func (s *Service) finishPlan(key string, j planJournal) (PlanApplyResult, error) {
	var result PlanApplyResult
	err := s.Store.Write(func(tx *store.Tx) error {
		var raw string
		if err := tx.Row("SELECT body FROM metadata WHERE key=?", key).Scan(&raw); err != nil {
			return err
		}
		var latest planJournal
		if err := json.Unmarshal([]byte(raw), &latest); err != nil {
			return err
		}
		if latest.ProposalHash != j.ProposalHash {
			return Error("PLAN_JOURNAL_CONFLICT", j.Proposal.ID)
		}
		if latest.Status == "applied" && latest.Result != nil {
			result = *latest.Result
			return nil
		}
		if latest.Status != "applying" || latest.Next != len(latest.Operations) {
			return Error("PLAN_APPLY_INCOMPLETE", j.Proposal.ID)
		}
		if err := checkPlanActive(tx, j.Proposal.Parent, key); err != nil {
			return err
		}
		parent, err := load(tx, j.Proposal.Parent)
		if err != nil || parent.Status != "open" || parent.DefinitionHash != j.ParentHash || taskStateHash(parent) != j.ParentStateHash || !equivalent(parent.EpicPlan, j.ParentPlan) {
			return Error("PLAN_PARENT_CHANGED", j.Proposal.Parent)
		}
		if err = s.checkTaskFile(parent); err != nil {
			return err
		}
		policy, err := s.policyHash()
		if err != nil || policy != j.PolicyHash {
			return Error("PLAN_STALE_POLICY", j.Proposal.Parent)
		}
		if parent.EpicPlan != nil {
			parent.EpicPlanHistory = append(parent.EpicPlanHistory, *parent.EpicPlan)
		}
		parent.EpicPlan = &EpicPlanSnapshot{ProposalHash: j.ProposalHash, Digest: j.Digest, Proposal: j.Proposal, TaskIDs: j.TaskIDs, Changes: j.Changes}
		if err = s.savePlan(tx, &parent); err != nil {
			return err
		}
		result = PlanApplyResult{Status: "applied", Parent: j.Proposal.Parent, PlanID: j.Proposal.ID, ProposalHash: j.ProposalHash, Changes: j.Changes}
		latest.Status, latest.Result = "applied", &result
		b, err := json.Marshal(latest)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE metadata SET body=? WHERE key=?", string(b), key); err != nil {
			return err
		}
		if _, err = tx.Exec("DELETE FROM metadata WHERE key=? AND body=?", planActiveKey(j.Proposal.Parent), key); err != nil {
			return err
		}
		return tx.Event(j.Proposal.Parent, "plan_applied", map[string]string{"plan_id": j.Proposal.ID, "proposal_hash": j.ProposalHash})
	})
	if err != nil {
		return result, err
	}
	if err = s.Store.Flush(); err != nil {
		return PlanApplyResult{}, err
	}
	return result, nil
}

// AbandonPlan releases an incomplete journal without deleting any partial work.
// A later plan must explicitly account for the retained tasks.
func (s *Service) AbandonPlan(parent, id, reason string) (map[string]any, error) {
	if err := s.active(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(reason) == "" {
		return nil, Error("REASON_REQUIRED", "abandoning a plan retains partial work")
	}
	unlock, err := s.lockPlan(parent)
	if err != nil {
		return nil, err
	}
	defer unlock()
	key := planJournalKey(parent, id)
	var result map[string]any
	err = s.Store.Write(func(tx *store.Tx) error {
		var raw string
		if err := tx.Row("SELECT body FROM metadata WHERE key=?", key).Scan(&raw); err != nil {
			return Error("PLAN_MISSING", id)
		}
		var j planJournal
		if err := json.Unmarshal([]byte(raw), &j); err != nil {
			return err
		}
		if j.Status != "applying" && j.Status != "restore_review_required" {
			return Error("PLAN_NOT_RESUMABLE", id)
		}
		retained := []string{}
		for _, op := range j.Operations {
			if _, err := load(tx, op.TaskID); err == nil {
				retained = append(retained, op.TaskID)
			} else if err != sql.ErrNoRows {
				return err
			}
		}
		j.Status = "abandoned"
		body, err := json.Marshal(j)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE metadata SET body=? WHERE key=?", string(body), key); err != nil {
			return err
		}
		if _, err = tx.Exec("DELETE FROM metadata WHERE key=? AND body=?", planActiveKey(parent), key); err != nil {
			return err
		}
		result = map[string]any{"status": "abandoned", "plan_id": id, "parent": parent, "retained_tasks": retained}
		return tx.Event(parent, "plan_abandoned", map[string]any{"plan_id": id, "reason": reason, "retained_tasks": retained})
	})
	return result, err
}

// RecoverRestoredPlan rebinds a restored journal only after the parent and
// affected existing tasks have had their normal restore confirmations resolved.
func (s *Service) RecoverRestoredPlan(parent, id, reason string) (map[string]any, error) {
	if err := s.active(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(reason) == "" {
		return nil, Error("REASON_REQUIRED", "describe the restored-plan review")
	}
	unlock, err := s.lockPlan(parent)
	if err != nil {
		return nil, err
	}
	defer unlock()
	key := planJournalKey(parent, id)
	err = s.Store.Write(func(tx *store.Tx) error {
		var raw, active string
		if err := tx.Row("SELECT body FROM metadata WHERE key=?", key).Scan(&raw); err != nil {
			return Error("PLAN_MISSING", id)
		}
		if err := tx.Row("SELECT body FROM metadata WHERE key=?", planActiveKey(parent)).Scan(&active); err != nil || active != key {
			return Error("PLAN_JOURNAL_CONFLICT", id)
		}
		var j planJournal
		if err := json.Unmarshal([]byte(raw), &j); err != nil {
			return err
		}
		if j.Status != "restore_review_required" {
			return Error("PLAN_NOT_RESTORED", id)
		}
		root, err := load(tx, parent)
		if err != nil || root.Status != "open" || root.DefinitionHash != j.ParentHash || !equivalent(root.EpicPlan, j.ParentPlan) {
			return Error("PLAN_PARENT_CHANGED", parent)
		}
		if root.Paused || root.Confirmation != nil {
			return Error("PLAN_RESTORE_REVIEW_REQUIRED", "resolve the parent's restore confirmation and pause first")
		}
		if err = s.checkTaskFile(root); err != nil {
			return err
		}
		policy, err := s.policyHash()
		if err != nil || policy != j.PolicyHash {
			return Error("PLAN_STALE_POLICY", parent)
		}
		j.ParentStateHash = taskStateHash(root)
		for i := j.Next; i < len(j.Operations); i++ {
			op := &j.Operations[i]
			current, err := load(tx, op.TaskID)
			if err == sql.ErrNoRows && op.Create {
				continue
			}
			if err != nil {
				return Error("PLAN_TASK_CHANGED", op.TaskID)
			}
			if err = s.checkTaskFile(current); err != nil {
				return err
			}
			if op.Create {
				if !sameDefinition(current.Definition, op.Definition) || !equivalent(current.EpicMembership, &op.Membership) {
					return Error("PLAN_TASK_CONFLICT", op.TaskID)
				}
				continue
			}
			if current.Paused || current.Confirmation != nil || hasWriter(current) {
				return Error("PLAN_RESTORE_REVIEW_REQUIRED", "resolve the restored task before resuming: "+op.TaskID)
			}
			if current.DefinitionHash != op.BeforeHash && !(op.Revise && sameDefinition(current.Definition, op.Definition)) {
				return Error("PLAN_TASK_CHANGED", op.TaskID)
			}
			if current.EpicMembership != nil && !equivalent(current.EpicMembership, &op.Membership) {
				return Error("PLAN_MEMBERSHIP_CONFLICT", op.TaskID)
			}
			op.BeforeStateHash = taskStateHash(current)
			op.BeforeLinkHash = taskBeforeLinkHash(current)
		}
		j.Status = "applying"
		body, err := json.Marshal(j)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE metadata SET body=? WHERE key=?", string(body), key); err != nil {
			return err
		}
		return tx.Event(parent, "plan_restore_reviewed", map[string]string{"plan_id": id, "reason": reason})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "ready_to_resume", "parent": parent, "plan_id": id}, nil
}
