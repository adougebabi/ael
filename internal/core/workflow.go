package core

import (
	"ael/internal/store"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

func equivalent(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func semantic(d Definition) Definition {
	d.Title = ""
	d.Source = ""
	d.Priority = 0
	d.Revision = 0
	return d
}

func (s *Service) displayEdit(old Task, d Definition, raw []byte) (Task, error) {
	return s.displayEditExpected(old, d, raw, "", false, "", "", "")
}

func (s *Service) displayEditExpected(old Task, d Definition, raw []byte, expectedPolicy string, refuseWriter bool, planParent, planKey, expectedState string) (Task, error) {
	d.Revision = old.Definition.Revision
	var t Task
	e := s.Store.Write(func(tx *store.Tx) error {
		if err := checkPlanActive(tx, planParent, planKey); err != nil {
			return err
		}
		var e error
		t, e = load(tx, d.ID)
		if e != nil {
			return e
		}
		if t.DefinitionHash != old.DefinitionHash {
			return Error("REVISION_CONFLICT", d.ID)
		}
		if expectedState != "" && taskStateHash(t) != expectedState {
			return Error("PLAN_TASK_CHANGED", d.ID)
		}
		if refuseWriter && (t.Status != "open" || hasWriter(t)) {
			return Error("PLAN_TASK_BUSY", d.ID)
		}
		if expectedPolicy != "" {
			current, err := s.policyHash()
			if err != nil || current != expectedPolicy {
				return Error("PLAN_STALE_POLICY", "project policy changed during application")
			}
		}
		t.Definition = d
		t.DefinitionHash = store.Hash(raw)
		if planKey != "" {
			e = s.savePlan(tx, &t)
		} else {
			e = s.save(tx, &t)
		}
		if e != nil {
			return e
		}
		p, e := s.taskPath(d.ID)
		if e != nil {
			return e
		}
		if expectedState != "" {
			e = tx.FileExpected(p, raw, old.DefinitionHash)
		} else {
			e = tx.File(p, raw)
		}
		if e != nil {
			return e
		}
		return tx.Event(d.ID, "display_updated", map[string]any{"title": d.Title, "revision": d.Revision, "effect": "presentation only; evidence and approvals retained"})
	})
	if e == nil {
		e = s.Store.Flush()
	}
	return t, e
}

// RecordContext binds what was supplied to the current attempt. It does not claim
// that a model understood or complied with that content.
func (s *Service) RecordContext(id string, refs []BasisReference) error {
	_, e := s.mutate(id, "context_loaded", func(tx *store.Tx, t *Task) error {
		t.Basis = refs
		t.ContextAttempt = ""
		for _, a := range t.Attempts {
			if a.Status == "running" {
				t.ContextAttempt = a.ID
			}
		}
		return tx.Event(id, "basis_supplied", map[string]any{"attempt": t.ContextAttempt, "references": refs})
	})
	return e
}
func sameBasis(a, b []BasisReference) bool {
	key := func(x []BasisReference) []string {
		out := []string{}
		for _, r := range x {
			out = append(out, r.ID+"@"+r.Version)
		}
		slices.Sort(out)
		return out
	}
	return equivalent(key(a), key(b))
}
func (s *Service) validateContext(t *Task, a *Attempt, result StepResult) error {
	if s.ResolveBasis == nil {
		return nil
	}
	refs, e := s.ResolveBasis(t.Definition)
	if e != nil {
		return e
	}
	return validateContextWith(t, a, result, refs, func(ref string) ([]byte, error) { return s.artifactAt(a.Workspace, ref) })
}

func validateContextWith(t *Task, a *Attempt, result StepResult, refs []BasisReference, read func(string) ([]byte, error)) error {
	if len(refs) > 0 && (t.ContextAttempt != a.ID || !sameBasis(t.Basis, refs)) {
		return Error("TASK_CONTEXT_REQUIRED", "load context for this attempt and current source versions before submitting actual work")
	}
	if result.Knowledge != nil {
		k := result.Knowledge
		if !slices.Contains([]string{"updated", "unchanged"}, k.Status) || strings.TrimSpace(k.Reason) == "" {
			return Error("KNOWLEDGE_OUTCOME_REQUIRED", "updated/unchanged and a concrete reason")
		}
		if k.Status == "updated" && len(k.Artifacts) == 0 {
			return Error("KNOWLEDGE_ARTIFACT_REQUIRED", "reference changed spec/decision")
		}
		for _, ref := range k.Artifacts {
			if _, e := read(ref); e != nil {
				return e
			}
		}
		t.KnowledgeOutcome = k
	} else if len(refs) > 0 {
		return Error("KNOWLEDGE_OUTCOME_REQUIRED", "assess spec/decision changes before verification; include result.knowledge")
	} else {
		t.KnowledgeOutcome = &KnowledgeOutcome{Status: "unchanged", Reason: "No applicable specification or decision is configured; no inferred contract was invented."}
	}
	return nil
}

// CheckComplete and CheckPassed are intentionally separate, including for report goals.
func CurrentEvidence(t *Task, c Check) *Evidence {
	config, _ := json.Marshal(c)
	for i := len(t.Evidence) - 1; i >= 0; i-- {
		e := &t.Evidence[i]
		if e.Check == c.ID && e.Candidate == t.Candidate && e.Revision == t.Definition.Revision && e.PlanRevision == t.Plan.Revision && e.Config == store.Hash(config) {
			return e
		}
	}
	return nil
}
func ChecksComplete(t *Task) bool {
	coverage := map[string]bool{}
	for _, c := range t.Definition.Checks {
		if !c.Required {
			continue
		}
		e := CurrentEvidence(t, c)
		if e == nil || (e.Result != "pass" && e.Result != "fail") {
			return false
		}
		for _, a := range c.Covers {
			coverage[a] = true
		}
	}
	for _, a := range t.Definition.Acceptance {
		if !coverage[a] {
			return false
		}
	}
	return len(coverage) > 0
}

type Progress struct {
	Summary         string     `json:"summary"`
	Artifacts       []string   `json:"artifacts"`
	EvidenceKind    string     `json:"evidence_kind,omitempty"`
	Next            string     `json:"next"`
	Actor           string     `json:"actor"`
	At              time.Time  `json:"at"`
	Stage           string     `json:"stage,omitempty"`
	ExpectedUntil   *time.Time `json:"expected_until,omitempty"`
	EvidenceChanged *bool      `json:"evidence_changed,omitempty"`
}

func (s *Service) Progress(id, attempt, token string, p Progress) (Next, error) {
	if p.Summary == "" || p.Next == "" {
		return Next{}, Error("PROGRESS_REQUIRED", "fact summary and next action required")
	}
	if p.EvidenceKind != "" && p.EvidenceKind != "deliverable" && p.EvidenceKind != "diagnostic" {
		return Next{}, Error("INVALID_EVIDENCE_KIND", "use deliverable or diagnostic")
	}
	if p.EvidenceKind == "deliverable" && len(p.Artifacts) == 0 {
		return Next{}, Error("ARTIFACT_REQUIRED", "a deliverable needs an actual artifact")
	}
	return s.mutate(id, "progress_recorded", func(tx *store.Tx, t *Task) error {
		a, e := findAttempt(t, attempt, token)
		if e != nil {
			return e
		}
		p.Actor = a.Executor
		p.At = time.Now().UTC()
		if p.ExpectedUntil != nil && (strings.TrimSpace(p.Stage) == "" || !p.ExpectedUntil.After(p.At)) {
			return Error("INVALID_WORK_STAGE", "a future expected_until requires a named stage")
		}
		a.LastContact = &p.At
		if p.Stage != "" || p.ExpectedUntil != nil {
			a.Stage, a.ExpectedUntil = p.Stage, p.ExpectedUntil
		}
		hashes := map[string]string{}
		for _, ref := range p.Artifacts {
			var data []byte
			if data, e = s.artifactAt(a.Workspace, ref); e != nil {
				return e
			}
			hashes[ref] = store.Hash(data)
		}
		changed := false
		if p.EvidenceKind == "deliverable" {
			changed = recordArtifactProgress(t, a.Unit, a.Input, p.Summary, hashes, p.At)
		}
		p.EvidenceChanged = &changed
		return tx.Event(id, "work_progress", p)
	})
}
