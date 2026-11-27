package core

import (
	"ael/internal/store"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ReviewBrief is the executor's explanation of what a human check asks the
// reviewer to look at on one exact candidate: what changed, how to see it and
// which evidence to open. It informs the reviewer; it never records a result
// and never satisfies a check by itself.
type ReviewBrief struct {
	Check     string    `json:"check"`
	Candidate string    `json:"candidate"`
	Summary   string    `json:"summary"`
	Changes   string    `json:"changes,omitempty"`
	Steps     []string  `json:"steps,omitempty"`
	Focus     []string  `json:"focus,omitempty"`
	Evidence  []string  `json:"evidence,omitempty"`
	Actor     string    `json:"actor,omitempty"`
	Recorded  time.Time `json:"recorded_at"`
}

const (
	reviewBriefMaxEvidence = 500
	reviewBriefMaxItems    = 30
)

func reviewBriefLine(value string, limit int) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= limit && !strings.ContainsAny(value, "\r\n\x00")
}

func validateReviewBrief(t Task, brief ReviewBrief) (Check, error) {
	var check Check
	found := false
	for _, c := range t.Definition.Checks {
		if c.ID == brief.Check {
			check, found = c, true
		}
	}
	if !found {
		return check, Error("CHECK_NOT_FOUND", brief.Check)
	}
	if EffectiveCheckMethod(check) != "human" {
		return check, Error("CHECK_METHOD_MISMATCH", brief.Check+" is not a human check")
	}
	if t.Status != "open" {
		return check, Error("TERMINAL_TASK", t.Definition.ID)
	}
	if t.Candidate == "" {
		return check, Error("REVIEW_BRIEF_INVALID", "submit the work first; a brief describes an exact candidate")
	}
	if !reviewBriefLine(brief.Summary, 300) {
		return check, Error("REVIEW_BRIEF_INVALID", "summary is one line of at most 300 bytes saying what the reviewer decides")
	}
	if len(brief.Changes) > 4000 || strings.ContainsRune(brief.Changes, 0) {
		return check, Error("REVIEW_BRIEF_INVALID", "changes is at most 4000 bytes")
	}
	for name, items := range map[string][]string{"steps": brief.Steps, "focus": brief.Focus} {
		if len(items) > reviewBriefMaxItems {
			return check, Error("REVIEW_BRIEF_INVALID", name+" has at most 30 items")
		}
		for _, item := range items {
			if !reviewBriefLine(item, 500) {
				return check, Error("REVIEW_BRIEF_INVALID", name+" items are single lines of at most 500 bytes")
			}
		}
	}
	if len(brief.Evidence) > reviewBriefMaxEvidence {
		return check, Error("REVIEW_BRIEF_INVALID", "evidence has at most 500 references")
	}
	seen := map[string]bool{}
	for _, ref := range brief.Evidence {
		if !reviewBriefLine(ref, 512) || seen[ref] {
			return check, Error("REVIEW_BRIEF_INVALID", "evidence references are distinct single lines")
		}
		seen[ref] = true
	}
	return check, nil
}

func normalizedReviewBrief(t Task, brief ReviewBrief, actor string) ReviewBrief {
	brief.Candidate = t.Candidate
	brief.Summary = strings.TrimSpace(brief.Summary)
	brief.Changes = strings.TrimSpace(brief.Changes)
	brief.Actor = actor
	brief.Recorded = time.Now().UTC()
	return brief
}

func recordReviewBrief(t *Task, brief ReviewBrief) {
	kept := t.ReviewBriefs[:0:0]
	for _, existing := range t.ReviewBriefs {
		if existing.Check != brief.Check {
			kept = append(kept, existing)
		}
	}
	t.ReviewBriefs = append(kept, brief)
}

// CurrentReviewBrief returns the brief for a check only while it still
// describes the task's current candidate.
func CurrentReviewBrief(t Task, check string) *ReviewBrief {
	for i := range t.ReviewBriefs {
		if t.ReviewBriefs[i].Check == check && t.Candidate != "" && t.ReviewBriefs[i].Candidate == t.Candidate {
			brief := t.ReviewBriefs[i]
			return &brief
		}
	}
	return nil
}

// RecordReviewBrief binds a local review brief to the current candidate.
// Evidence references must exist in the candidate workspace or runtime store.
func (s *Service) RecordReviewBrief(id string, brief ReviewBrief, actor string) (Next, error) {
	if err := s.active(); err != nil {
		return Next{}, err
	}
	if s.RemoteAuthority {
		return Next{}, Error("REMOTE_OPERATION_REQUIRED", "record the brief through the central server")
	}
	return s.mutate(id, "review_brief_recorded", func(_ *store.Tx, t *Task) error {
		if _, err := validateReviewBrief(*t, brief); err != nil {
			return err
		}
		workspace := t.CandidateWorkspace
		if workspace == "" {
			workspace = s.Root
		}
		for _, ref := range brief.Evidence {
			if !strings.HasPrefix(ref, "runtime:") && (filepath.IsAbs(ref) || strings.HasPrefix(filepath.Clean(ref), "..")) {
				return Error("REVIEW_BRIEF_INVALID", "evidence must be a project-relative path or runtime: reference: "+ref)
			}
			if !s.artifactExists(workspace, ref) {
				return Error("ARTIFACT_MISSING", ref)
			}
		}
		recordReviewBrief(t, normalizedReviewBrief(*t, brief, actor))
		return nil
	})
}

func (s *Service) artifactExists(workspace, ref string) bool {
	root, rel := workspace, ref
	if strings.HasPrefix(ref, "runtime:") {
		root, rel = s.Store.Dir, strings.TrimPrefix(ref, "runtime:")
	}
	p, err := store.SafePath(root, rel)
	if err != nil {
		return false
	}
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

// RecordRemoteReviewBriefTx is the central variant. The caller has already
// verified that every evidence ID is a finalized attachment of this task on
// the current candidate.
func (s *Service) RecordRemoteReviewBriefTx(tx *store.Tx, id, revision string, brief ReviewBrief, actor string, evidenceOK func(task Task, ref string) bool) (Task, error) {
	var task Task
	if !s.RemoteAuthority || evidenceOK == nil {
		return task, Error("REMOTE_OPERATION_REQUIRED", "validated central evidence is required")
	}
	task, err := load(tx, id)
	if err != nil {
		return task, err
	}
	if RemoteRevision(task) != revision {
		return task, Error("REVISION_CONFLICT", "task changed before the review brief was recorded")
	}
	if _, err = validateReviewBrief(task, brief); err != nil {
		return task, err
	}
	for _, ref := range brief.Evidence {
		if !evidenceOK(task, ref) {
			return task, Error("ARTIFACT_REQUIRED", "evidence must be a finalized attachment of this task on the current candidate: "+ref)
		}
	}
	recordReviewBrief(&task, normalizedReviewBrief(task, brief, actor))
	if err = s.save(tx, &task); err != nil {
		return task, err
	}
	return task, tx.Event(id, "review_brief_recorded", map[string]any{"check": brief.Check, "candidate": task.Candidate})
}

// humanWaitReason tells the executor what the reviewer still lacks.
func humanWaitReason(t Task, check string) string {
	if CurrentReviewBrief(t, check) == nil {
		return "check requires a human decision: " + check + "; record a review brief (ael task review-brief) so the reviewer knows what to look at"
	}
	return "check requires a human decision: " + check
}
