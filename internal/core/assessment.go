package core

import (
	"ael/internal/store"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"
)

type Assessment struct {
	FailureClass string   `json:"failure_class,omitempty"`
	Task         string   `json:"task"`
	Check        string   `json:"check"`
	Candidate    string   `json:"candidate"`
	Method       string   `json:"method"`
	Executor     string   `json:"executor"`
	Result       string   `json:"result"`
	Reason       string   `json:"reason"`
	Inputs       []string `json:"inputs"`
	Artifacts    []string `json:"artifacts"`
	ReturnTo     string   `json:"return_to"`
}

func (s *Service) Assess(r Assessment) (Next, error)                  { return s.assess(r, nil) }
func (s *Service) AssessClaim(r Assessment, a *Attempt) (Next, error) { return s.assess(r, a) }

// AssessExisting submits evidence against the exact attempt claimed by work claim.
func (s *Service) AssessExisting(r Assessment, attemptID, token string) (Next, error) {
	if os.Getenv("AEL_MANAGED_CHILD") == "1" {
		return Next{}, Error("MANAGED_CHILD_FORBIDDEN", "managed child must return its current result to the parent runner")
	}
	t, err := s.Get(r.Task)
	if err != nil {
		return Next{}, err
	}
	a, err := findAttempt(&t, attemptID, token)
	if err != nil {
		return Next{}, err
	}
	if a.Unit != "check:"+r.Check || a.Executor != r.Executor {
		return Next{}, Error("WRONG_UNIT", r.Check)
	}
	copy := *a
	return s.assess(r, &copy)
}
func (s *Service) assess(r Assessment, a *Attempt) (Next, error) {
	t, e := s.Get(r.Task)
	if e != nil {
		return Next{}, e
	}
	if t.Step != "verify" || r.Candidate != t.Candidate || r.Executor == "" || strings.TrimSpace(r.Reason) == "" {
		return Next{}, Error("ASSESSMENT_INPUT_REQUIRED", "exact candidate, executor and findings/reason required")
	}
	if !slices.Contains([]string{"pass", "fail", "blocked"}, r.Result) || !slices.Contains([]string{"model", "scenario", "human"}, r.Method) {
		return Next{}, Error("INVALID_ASSESSMENT", "method/result")
	}
	var c Check
	for _, x := range t.Definition.Checks {
		if x.ID == r.Check {
			c = x
		}
	}
	if c.ID == "" || len(c.Command) > 0 || (c.Method != "" && c.Method != r.Method) {
		return Next{}, Error("CHECK_METHOD_MISMATCH", r.Check)
	}
	workspace := t.CandidateWorkspace
	if workspace == "" {
		workspace = s.Root
	}
	changedClaimedEpic := false
	if a != nil && t.Definition.Type == "epic" && r.Method != "human" {
		if current, err := Fingerprint(workspace); err == nil && (current != a.Input || a.WorkspaceConflict != "") {
			changedClaimedEpic = true
		}
	}
	hashes := map[string]string{}
	status := map[string]string{}
	screenshot := false
	for _, ref := range append(slices.Clone(r.Inputs), r.Artifacts...) {
		b, e := s.artifactAt(workspace, ref)
		if e != nil {
			if changedClaimedEpic && strings.HasPrefix(e.Error(), "ARTIFACT_MISSING:") {
				status[ref] = "missing_after_candidate_change"
				continue
			}
			return Next{}, e
		}
		hashes[ref] = store.Hash(b)
		mime := http.DetectContentType(b)
		if slices.Contains(r.Artifacts, ref) && (mime == "image/png" || mime == "image/jpeg") {
			screenshot = true
		}
	}
	if r.Result != "blocked" && !changedClaimedEpic {
		if err := RequireArtifactPaths(c.Artifacts, r.Artifacts); err != nil {
			return Next{}, err
		}
		attachment, needsScreenshot := CheckEvidenceRequirements(Task{}, c)
		if needsScreenshot && !screenshot {
			return Next{}, Error("SCREENSHOT_REQUIRED", r.Check)
		}
		if attachment && len(r.Artifacts) == 0 {
			return Next{}, Error("ARTIFACT_REQUIRED", "missing required attachment for check "+r.Check)
		}
	}
	if a == nil {
		n, e := s.Claim(r.Task, r.Check, r.Executor, "", "")
		if e != nil {
			return n, e
		}
		a = n.Attempt
	}
	if a.Unit != "check:"+r.Check {
		return Next{}, Error("WRONG_UNIT", a.Unit)
	}
	config, _ := json.Marshal(c)
	source := r.Method + ":" + r.Executor
	ev := Evidence{EvidenceMode: "direct_observation", Check: c.ID, Candidate: t.Candidate, Revision: t.Definition.Revision, PlanRevision: t.Plan.Revision, Config: store.Hash(config), Result: r.Result, Source: source, Executor: r.Executor, Inputs: r.Inputs, Environment: runtime.GOOS + "/" + runtime.GOARCH, Artifacts: r.Artifacts, ArtifactHashes: hashes, ArtifactStatus: status, Reason: r.Reason, At: time.Now().UTC()}
	return s.submit(Submission{Task: r.Task, Attempt: a.ID, Token: a.Token, Key: a.ID + "-assessment", Result: StepResult{Status: r.Result, Summary: r.Reason, Artifacts: r.Artifacts, ReturnTo: r.ReturnTo, FailureClass: r.FailureClass}}, &ev)
}
