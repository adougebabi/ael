package core

import (
	"ael/internal/store"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Failures remain grouped conservatively until the same unit produces new
// evidence of a different problem. Existing signatures keep their budgets.
type FailureCycle struct {
	Unit             string          `json:"unit"`
	Signature        string          `json:"signature"`
	FailureClass     string          `json:"failure_class,omitempty"`
	Count            int             `json:"count"`
	State            string          `json:"state"`
	Input            string          `json:"failure_input"`
	LastReason       string          `json:"last_reason"`
	LastFailureAt    time.Time       `json:"last_failure_at"`
	Review           *FailureReview  `json:"review,omitempty"`
	ReviewedEvidence map[string]bool `json:"reviewed_evidence,omitempty"`
	FailureEvidence  map[string]bool `json:"failure_evidence,omitempty"`
}

type FailureReview struct {
	Actor          string            `json:"actor"`
	Tried          string            `json:"tried"`
	Excluded       string            `json:"excluded"`
	Hypothesis     string            `json:"hypothesis"`
	Next           string            `json:"next"`
	Recovery       string            `json:"recovery"`
	Evidence       []string          `json:"evidence"`
	EvidenceHashes map[string]string `json:"evidence_hashes,omitempty"`
	At             time.Time         `json:"at,omitempty"`
}

func failureSignature(unit, class string) string {
	class = strings.Join(strings.Fields(strings.ToLower(class)), " ")
	if class == "" {
		class = "unclassified"
	}
	return store.Hash([]byte(unit + "\x00" + class))
}

func failureKey(t *Task, unit, class, signature string, hashes map[string]bool) (string, FailureCycle) {
	latestKey := ""
	var latest FailureCycle
	seen := map[string]bool{}
	for key, candidate := range t.FailureCycles {
		if candidate.Unit != unit || candidate.State == "resolved" {
			continue
		}
		if candidate.Signature == signature {
			return key, candidate
		}
		for hash := range candidate.FailureEvidence {
			seen[hash] = true
		}
		if latestKey == "" || candidate.LastFailureAt.After(latest.LastFailureAt) {
			latestKey, latest = key, candidate
		}
	}
	if latestKey != "" {
		newEvidence := false
		for hash := range hashes {
			if !seen[hash] {
				newEvidence = true
			}
		}
		// Descriptions and unrelated progress cannot split a budget. A new
		// explicitly named class must carry previously unseen failure evidence.
		if strings.TrimSpace(class) == "" || !newEvidence {
			return latestKey, latest
		}
	}
	key := unit
	if current, exists := t.FailureCycles[key]; exists && current.State != "resolved" {
		key = unit + "#" + signature
	}
	return key, FailureCycle{Unit: unit, Signature: signature, FailureClass: class, State: "counting"}
}

func (s *Service) failureEvidence(a *Attempt, result StepResult, evidence *Evidence) map[string]bool {
	refs := append([]string{}, result.Artifacts...)
	if evidence != nil {
		refs = append(refs, evidence.Artifacts...)
	}
	hashes := map[string]bool{}
	for _, ref := range refs {
		if strings.HasSuffix(strings.ToLower(ref), ".log") {
			continue
		}
		if data, err := s.artifactAt(a.Workspace, ref); err == nil {
			hashes[store.Hash(data)] = true
		}
	}
	return hashes
}

func failureWouldStop(t *Task, a *Attempt, result StepResult, hashes map[string]bool) bool {
	if pendingFailureKey(t) != "" {
		return true
	}
	_, cycle := failureKey(t, a.Unit, result.FailureClass, failureSignature(a.Unit, result.FailureClass), hashes)
	return cycle.State == "reviewed" || cycle.Count+1 >= t.Definition.MaxRetries
}

func activateFailure(t *Task, key string) {
	cycle := t.FailureCycles[key]
	t.ActiveFailure, t.Paused = key, true
	t.Failure = "重复失败，现场已保留：" + cycle.LastReason
	recovery := "恢复前先记录限定根因复核"
	next := "核对失败输入、已尝试内容和下一条验证假设"
	if cycle.State == "blocked" {
		recovery = "需要新证据和再次限定复核后才能重试"
		next = "保留失败输入，继续核查尚未解决的假设"
	}
	t.Blocker = &Blocker{Category: "repeated_failure", Reason: t.Failure, Scope: t.Definition.Scope,
		Tried:    fmt.Sprintf("%s 已失败 %d 次；最近一次：%s", cycle.Unit, cycle.Count, cycle.LastReason),
		Recovery: recovery, ResumeAt: t.Step, Unit: cycle.Unit, Input: cycle.Input, Next: next}
}

func pendingFailureKey(t *Task) string {
	keys := make([]string, 0, len(t.FailureCycles))
	for key, cycle := range t.FailureCycles {
		if cycle.State == "review_required" || cycle.State == "blocked" {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := t.FailureCycles[keys[i]], t.FailureCycles[keys[j]]
		if !a.LastFailureAt.Equal(b.LastFailureAt) {
			return a.LastFailureAt.Before(b.LastFailureAt)
		}
		return keys[i] < keys[j]
	})
	if len(keys) > 0 {
		return keys[0]
	}
	return ""
}

func hasUnresolvedFailures(t *Task) bool {
	for _, cycle := range t.FailureCycles {
		if cycle.State != "resolved" {
			return true
		}
	}
	return false
}

func noteFailure(t *Task, a *Attempt, result StepResult, hashes map[string]bool) {
	if t.FailureCycles == nil {
		t.FailureCycles = map[string]FailureCycle{}
	}
	key, cycle := failureKey(t, a.Unit, result.FailureClass, failureSignature(a.Unit, result.FailureClass), hashes)
	cycle.Count++
	cycle.Input, cycle.LastReason = a.Input, result.Summary
	if a.Ended != nil {
		cycle.LastFailureAt = *a.Ended
	}
	if cycle.FailureEvidence == nil {
		cycle.FailureEvidence = map[string]bool{}
	}
	for hash := range hashes {
		cycle.FailureEvidence[hash] = true
	}
	if cycle.State == "reviewed" {
		cycle.State = "blocked"
	} else if cycle.Count >= t.Definition.MaxRetries {
		cycle.State = "review_required"
	}
	t.FailureCycles[key] = cycle
	if cycle.State != "review_required" && cycle.State != "blocked" {
		return
	}
	if current, ok := t.FailureCycles[t.ActiveFailure]; t.Paused && ok && t.ActiveFailure != key && (current.State == "review_required" || current.State == "blocked") {
		t.Failure = "重复失败，现场已保留：" + current.LastReason
		return
	}
	activateFailure(t, key)
}

func noteSuccess(t *Task, a *Attempt) {
	p := t.EvidenceProgress
	if p == nil || p.At == nil || p.Unit != a.Unit {
		return
	}
	for key, cycle := range t.FailureCycles {
		if cycle.Unit != a.Unit || cycle.State == "resolved" || !p.At.After(cycle.LastFailureAt) {
			continue
		}
		cycle.State = "resolved"
		t.FailureCycles[key] = cycle
		if t.ActiveFailure == key {
			t.ActiveFailure = ""
		}
	}
}

// ReviewFailure records the actual bounded investigation. It does not approve
// checks or itself resume execution, and never resets the failed-attempt count.
func (s *Service) ReviewFailure(id string, review FailureReview) (Next, error) {
	if err := s.active(); err != nil {
		return Next{}, err
	}
	if strings.TrimSpace(review.Actor) == "" || strings.TrimSpace(review.Tried) == "" ||
		strings.TrimSpace(review.Excluded) == "" || strings.TrimSpace(review.Hypothesis) == "" ||
		strings.TrimSpace(review.Next) == "" || strings.TrimSpace(review.Recovery) == "" || len(review.Evidence) == 0 {
		return Next{}, Error("ROOT_CAUSE_REVIEW_REQUIRED", "actor, tried, excluded, hypothesis, recovery, next and evidence are required")
	}
	return s.mutate(id, "root_cause_reviewed", func(tx *store.Tx, t *Task) error {
		activeKey := t.ActiveFailure
		cycle, ok := t.FailureCycles[activeKey]
		if !ok || !t.Paused || (cycle.State != "review_required" && cycle.State != "blocked") {
			return Error("ROOT_CAUSE_REVIEW_NOT_EXPECTED", id)
		}
		workspace := s.Root
		for i := len(t.Attempts) - 1; i >= 0; i-- {
			if t.Attempts[i].Unit == cycle.Unit && t.Attempts[i].Workspace != "" {
				workspace = t.Attempts[i].Workspace
				break
			}
		}
		review.EvidenceHashes = map[string]string{}
		if cycle.ReviewedEvidence == nil {
			cycle.ReviewedEvidence = map[string]bool{}
		}
		newEvidence := false
		for _, ref := range review.Evidence {
			data, err := s.artifactAt(workspace, ref)
			if err != nil {
				return err
			}
			hash := store.Hash(data)
			review.EvidenceHashes[ref] = hash
			if !cycle.ReviewedEvidence[hash] {
				newEvidence = true
			}
		}
		if cycle.State == "blocked" && !newEvidence {
			return Error("ROOT_CAUSE_NEW_EVIDENCE_REQUIRED", "repeat review requires new retained evidence")
		}
		for _, hash := range review.EvidenceHashes {
			cycle.ReviewedEvidence[hash] = true
		}
		review.At = time.Now().UTC()
		cycle.Review = &review
		cycle.State = "reviewed"
		t.FailureCycles[activeKey] = cycle
		if next := pendingFailureKey(t); next != "" {
			activateFailure(t, next)
		} else if t.Blocker != nil {
			t.Blocker.Tried, t.Blocker.Recovery, t.Blocker.Next = review.Tried, review.Recovery, review.Next
		}
		return tx.Event(id, "bounded_root_cause_review", map[string]any{"unit": cycle.Unit, "signature": cycle.Signature, "count": cycle.Count, "review": review})
	})
}
