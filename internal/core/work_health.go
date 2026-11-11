package core

import (
	"ael/internal/store"
	"sort"
	"strings"
	"time"
)

// EvidenceProgress is persisted separately from contact and diagnostic events.
// Seen hashes/outcomes prevent repeated logs or identical results from resetting
// the no-progress clock. It is a projection, not an acceptance gate.
type EvidenceProgress struct {
	At               *time.Time        `json:"at,omitempty"`
	Kind             string            `json:"kind,omitempty"`
	Unit             string            `json:"unit,omitempty"`
	Candidate        string            `json:"candidate,omitempty"`
	Summary          string            `json:"summary,omitempty"`
	SeenArtifacts    map[string]bool   `json:"seen_artifacts,omitempty"`
	CheckConclusions map[string]string `json:"check_conclusions,omitempty"`
}

type WorkHealth struct {
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
		p.At, p.Kind, p.Unit, p.Candidate, p.Summary = &at, "artifact", unit, candidate, summary
	}
	return changed
}

func checkProgressKey(e Evidence) string { return e.Check + "\x00" + e.Candidate + "\x00" + e.Config }

func checkConclusion(e Evidence) string {
	hashes := []string{}
	for _, ref := range e.Artifacts {
		if !strings.HasSuffix(strings.ToLower(ref), ".log") && e.ArtifactHashes[ref] != "" {
			hashes = append(hashes, e.ArtifactHashes[ref])
		}
	}
	sort.Strings(hashes)
	return store.Hash([]byte(e.Result + "\x00" + strings.Join(hashes, "\x00")))
}

func recordCheckProgress(t *Task, e Evidence, prior []Evidence, at time.Time) bool {
	if len(e.Artifacts) == 0 {
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
	p.At, p.Kind, p.Unit, p.Candidate, p.Summary = &at, "check", "check:"+e.Check, e.Candidate, e.Reason
	return true
}

func (s *Service) recordSubmittedProgress(t *Task, a *Attempt, result StepResult, e *Evidence, prior []Evidence, at time.Time) bool {
	if e != nil {
		return recordCheckProgress(t, *e, prior, at)
	}
	if result.Status != "pass" || len(result.Artifacts) == 0 {
		return false
	}
	hashes := map[string]string{}
	for _, ref := range result.Artifacts {
		if data, err := s.artifactAt(a.Workspace, ref); err == nil {
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
