package core

import (
	"ael/internal/store"
	"slices"
	"sort"
	"strings"
	"time"
)

// EvidenceProgress is persisted separately from contact and diagnostic events.
// Seen hashes/outcomes prevent repeated logs or identical results from resetting
// the no-progress clock. It is a projection, not an acceptance gate.
type EvidenceProgress struct {
	Revision         int               `json:"definition_revision,omitempty"`
	PlanRevision     int               `json:"plan_revision,omitempty"`
	At               *time.Time        `json:"at,omitempty"`
	Kind             string            `json:"kind,omitempty"`
	Unit             string            `json:"unit,omitempty"`
	Candidate        string            `json:"candidate,omitempty"`
	Summary          string            `json:"summary,omitempty"`
	SeenArtifacts    map[string]bool   `json:"seen_artifacts,omitempty"`
	CheckConclusions map[string]string `json:"check_conclusions,omitempty"`
}

type WorkHealth struct {
	LastValidity     string     `json:"last_progress_validity,omitempty"`
	CurrentWork      string     `json:"current_work,omitempty"`
	ReportedNext     string     `json:"reported_next,omitempty"`
	ReportedAt       *time.Time `json:"reported_at,omitempty"`
	LastUnit         string     `json:"last_progress_unit,omitempty"`
	LastCandidate    string     `json:"last_progress_candidate,omitempty"`
	LastContact      *time.Time `json:"last_contact,omitempty"`
	LastProgress     *time.Time `json:"last_progress_at,omitempty"`
	LastProgressKind string     `json:"last_progress_kind,omitempty"`
	LastSummary      string     `json:"last_progress_summary,omitempty"`
	Stage            string     `json:"stage,omitempty"`
	ExpectedUntil    *time.Time `json:"expected_until,omitempty"`
	Candidate        string     `json:"candidate,omitempty"`
	FailureSignature string     `json:"failure_signature,omitempty"`
	ThresholdSeconds int        `json:"threshold_seconds"`
	NoProgress       bool       `json:"no_progress"`
	HealthyLong      bool       `json:"healthy_long"`
	ContactState     string     `json:"contact_state"`
}

func progressState(t *Task) *EvidenceProgress {
	if t.EvidenceProgress == nil {
		t.EvidenceProgress = &EvidenceProgress{}
	}
	if t.EvidenceProgress.SeenArtifacts == nil {
		t.EvidenceProgress.SeenArtifacts = map[string]bool{}
	}
	if t.EvidenceProgress.CheckConclusions == nil {
		t.EvidenceProgress.CheckConclusions = map[string]string{}
	}
	return t.EvidenceProgress
}

func recordArtifactProgress(t *Task, unit, candidate, summary string, hashes map[string]string, at time.Time) bool {
	if len(hashes) == 0 {
		return false
	}
	p := progressState(t)
	changed := false
	for ref, hash := range hashes {
		// A growing diagnostic log can repeat the same work indefinitely; a
		// check's actual new conclusion is handled separately below.
		if strings.HasSuffix(strings.ToLower(ref), ".log") {
			continue
		}
		if !p.SeenArtifacts[hash] {
			changed = true
			p.SeenArtifacts[hash] = true
		}
	}
	if changed {
		p.Revision, p.PlanRevision = t.Definition.Revision, t.Plan.Revision
		p.At, p.Kind, p.Unit, p.Candidate, p.Summary = &at, "artifact", unit, candidate, summary
	}
	return changed
}

func checkProgressKey(e Evidence) string { return e.Check + "\x00" + e.Candidate + "\x00" + e.Config }

func checkConclusion(e Evidence) string {
	hashes := []string{}
	for _, ref := range e.Artifacts {
		if !strings.HasSuffix(strings.ToLower(ref), ".log") && !slices.Contains(e.DiagnosticArtifacts, ref) && e.ArtifactHashes[ref] != "" {
			hashes = append(hashes, e.ArtifactHashes[ref])
		}
	}
	sort.Strings(hashes)
	return store.Hash([]byte(e.Result + "\x00" + strings.Join(hashes, "\x00")))
}

func recordCheckProgress(t *Task, e Evidence, prior []Evidence, at time.Time) bool {
	if len(e.Artifacts) == 0 && e.EvidenceMode != "direct_observation" {
		return false
	}
	p := progressState(t)
	key := checkProgressKey(e)
	if _, known := p.CheckConclusions[key]; !known {
		for _, old := range prior {
			if checkProgressKey(old) == key {
				p.CheckConclusions[key] = checkConclusion(old)
			}
		}
	}
	conclusion := checkConclusion(e)
	if p.CheckConclusions[key] == conclusion {
		return false
	}
	p.CheckConclusions[key] = conclusion
	p.Revision, p.PlanRevision = e.Revision, e.PlanRevision
	p.At, p.Kind, p.Unit, p.Candidate, p.Summary = &at, "check", "check:"+e.Check, e.Candidate, e.Reason
	return true
}

func (s *Service) recordSubmittedProgress(t *Task, a *Attempt, result StepResult, e *Evidence, prior []Evidence, at time.Time) bool {
	return recordSubmittedProgressWith(t, a, result, e, prior, at, func(ref string) ([]byte, error) { return s.artifactAt(a.Workspace, ref) }, nil)
}

func recordSubmittedProgressWith(t *Task, a *Attempt, result StepResult, e *Evidence, prior []Evidence, at time.Time, read func(string) ([]byte, error), diagnostic map[string]bool) bool {
	if e != nil {
		return recordCheckProgress(t, *e, prior, at)
	}
	if result.Status != "pass" || len(result.Artifacts) == 0 {
		return false
	}
	hashes := map[string]string{}
	for _, ref := range result.Artifacts {
		if diagnostic[ref] {
			continue
		}
		if data, err := read(ref); err == nil {
			hashes[ref] = store.Hash(data)
		}
	}
	return recordArtifactProgress(t, a.Unit, a.Candidate, result.Summary, hashes, at)
}

// WorkHealthAt takes its clock explicitly so threshold behavior can be tested
// without waiting for the configured interval.
func (s *Service) WorkHealthAt(id string, now time.Time) (WorkHealth, error) {
	t, err := s.Get(id)
	if err != nil {
		return WorkHealth{}, err
	}
	return s.workHealth(t, now)
}

func (s *Service) workHealth(t Task, now time.Time) (WorkHealth, error) {
	policy, err := s.Policy()
	if err != nil {
		return WorkHealth{}, err
	}
	seconds := policy.NoProgressSeconds
	if seconds == 0 {
		seconds = 1800
	}
	if seconds < 0 {
		return WorkHealth{}, Error("INVALID_NO_PROGRESS_THRESHOLD", "no_progress_seconds must be nonnegative")
	}
	h := WorkHealth{ThresholdSeconds: seconds, Candidate: t.Candidate, ContactState: "no_executor"}
	base := t.Created
	if p := t.EvidenceProgress; p != nil && p.At != nil {
		h.LastProgress, h.LastProgressKind, h.LastSummary = p.At, p.Kind, p.Summary
		h.LastUnit, h.LastCandidate = p.Unit, p.Candidate
		h.LastValidity = "prior"
		if p.Revision == 0 || p.PlanRevision == 0 {
			h.LastValidity = "unknown"
		} else if !t.Historical && p.Revision == t.Definition.Revision && p.PlanRevision == t.Plan.Revision && p.Candidate != "" && p.Candidate == t.Candidate {
			h.LastValidity = "current"
		}
		base = *p.At
	}
	if cycle, ok := t.FailureCycles[t.ActiveFailure]; ok {
		h.FailureSignature = cycle.Signature
	}
	if h.FailureSignature == "" {
		var latest time.Time
		for _, cycle := range t.FailureCycles {
			if cycle.State != "resolved" && (h.FailureSignature == "" || cycle.LastFailureAt.After(latest)) {
				h.FailureSignature, latest = cycle.Signature, cycle.LastFailureAt
			}
		}
	}
	running, expired := false, false
	for i := range t.Attempts {
		a := &t.Attempts[i]
		contact := a.LastContact
		if contact == nil {
			contact = &a.Started
		}
		if h.LastContact == nil || contact.After(*h.LastContact) {
			h.LastContact = contact
		}
		if a.Status == "running" && !a.Lease.After(now) {
			expired = true
		}
		if a.Status == "running" && a.Lease.After(now) {
			running = true
			if a.ProgressAt != nil && (h.ReportedAt == nil || a.ProgressAt.After(*h.ReportedAt)) {
				h.CurrentWork, h.ReportedNext, h.ReportedAt = a.ReportedSummary, a.ReportedNext, a.ProgressAt
			}
			declaredLong := a.Stage != "" && a.ExpectedUntil != nil && a.ExpectedUntil.After(now)
			if declaredLong || (h.Stage == "" && a.Stage != "") {
				h.Stage, h.ExpectedUntil = a.Stage, a.ExpectedUntil
			}
			h.HealthyLong = h.HealthyLong || declaredLong
			if h.Candidate == "" {
				h.Candidate = a.Input
			}
		}
	}
	switch {
	case running && expired:
		h.ContactState = "mixed"
	case running:
		h.ContactState = "running"
	case expired:
		h.ContactState = "expired"
	}
	if t.Status == "open" && !base.IsZero() && now.Sub(base) >= time.Duration(seconds)*time.Second {
		h.NoProgress = true
	}
	return h, nil
}
