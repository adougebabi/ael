package core

import (
	"slices"
	"testing"
)

func TestFindingIdentityUsesExactLocationCandidateAndVersions(t *testing.T) {
	input, err := NormalizeFinding(FindingInput{Rule: "ui.clipped", Title: "Clipped", Kind: "functional", Route: "/page", Location: FindingLocation{Selector: `[data-label="two  spaces"]`}}, "ui")
	if err != nil {
		t.Fatal(err)
	}
	if input.Kind != "ui" {
		t.Fatal("finding downgraded the declared UI evidence requirement")
	}
	key := FindingFingerprint("task", "check", "candidate", "config", 1, 1, input)
	renamed := input
	renamed.Title = "Different wording"
	if FindingFingerprint("task", "check", "candidate", "config", 1, 1, renamed) != key {
		t.Fatal("title became issue identity")
	}
	moved := input
	moved.Location.Selector = `[data-label="two spaces"]`
	if FindingFingerprint("task", "check", "candidate", "config", 1, 1, moved) == key {
		t.Fatal("different selectors were merged")
	}
	if FindingFingerprint("task", "check", "next-candidate", "config", 1, 1, input) == key || FindingFingerprint("task", "check", "candidate", "config", 2, 1, input) == key {
		t.Fatal("candidate or requirement version was mixed")
	}
}

func TestFindingEvidenceCompletenessKeepsUIAndUXRequirements(t *testing.T) {
	input := FindingInput{Kind: "ui", Expected: "visible", Actual: "clipped", Reproduction: []string{"open"}, Impact: "cannot read"}
	if !slices.Contains(FindingMissing(input, "report", nil), "screenshot") {
		t.Fatal("UI without screenshot appeared complete")
	}
	image := FindingEvidence{ID: "image", MIME: "image/png", Status: "finalized"}
	if len(FindingMissing(input, "report", []FindingEvidence{image})) != 0 {
		t.Fatal("valid UI evidence remained incomplete")
	}
	input.Kind = "ux"
	if !slices.Contains(FindingMissing(input, "report", []FindingEvidence{image}), "interaction_trace") {
		t.Fatal("screenshot substituted for interaction evidence")
	}
	input.TraceArtifacts = []string{"trace"}
	trace := FindingEvidence{ID: "trace", MIME: "text/plain", Status: "finalized"}
	if len(FindingMissing(input, "report", []FindingEvidence{image, trace})) != 0 {
		t.Fatal("explicit interaction evidence not accepted")
	}
	trace.Status = "unavailable"
	if !slices.Contains(FindingMissing(input, "report", []FindingEvidence{trace}), "interaction_trace") {
		t.Fatal("missing original kept valid interaction proof")
	}
}

func TestFindingLocationCannotEscapeCandidateRoot(t *testing.T) {
	_, err := NormalizeFinding(FindingInput{Rule: "rule", Title: "title", Location: FindingLocation{Path: "src/../../private"}}, "functional")
	if err == nil {
		t.Fatal("normalized path escaped the project")
	}
}
