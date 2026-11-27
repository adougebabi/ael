package core

import (
	"strings"
	"testing"
)

func TestHumanWaitReasonAsksForBriefUntilOneDescribesCurrentCandidate(t *testing.T) {
	task := Task{Candidate: "c2", ReviewBriefs: []ReviewBrief{{Check: "ui", Candidate: "c1", Summary: "old"}}}
	if reason := humanWaitReason(task, "ui"); !strings.Contains(reason, "ael task review-brief") {
		t.Fatalf("stale brief silenced the request: %s", reason)
	}
	task.ReviewBriefs[0].Candidate = "c2"
	if reason := humanWaitReason(task, "ui"); strings.Contains(reason, "review-brief") || !strings.HasPrefix(reason, "check requires a human decision: ui") {
		t.Fatalf("current brief still requested: %s", reason)
	}
}
