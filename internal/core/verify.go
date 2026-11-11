package core

import (
	"ael/internal/process"
	"ael/internal/store"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"

	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func (s *Service) Verify(id, check, executor string) (Next, error) {
	n, e := s.Claim(id, check, executor, "", "")
	if e != nil {
		return n, e
	}
	return s.verifyClaimed(id, check, executor, n)
}

// VerifyClaim executes the exact check already atomically claimed by a Runner.
func (s *Service) VerifyClaim(id, check, executor, attemptID, token string) (Next, error) {
	if os.Getenv("AEL_MANAGED_CHILD") == "1" {
		return Next{}, Error("MANAGED_CHILD_FORBIDDEN", "managed child must return its current result to the parent runner")
	}
	t, err := s.Get(id)
	if err != nil {
		return Next{}, err
	}
	a, err := findAttempt(&t, attemptID, token)
	if err != nil {
		return Next{}, err
	}
	if t.Step != "verify" || a.Unit != "check:"+check || a.Executor != executor {
		return Next{}, Error("WRONG_UNIT", check)
	}
	n := t.BaseNext()
	n.Task = &t
	copy := *a
	n.Attempt = &copy
	return s.verifyClaimed(id, check, executor, n)
}

func (s *Service) verifyClaimed(id, check, executor string, n Next) (Next, error) {
	var e error
	a := n.Attempt
	t := n.Task
	finished := false
	defer func() {
		if !finished {
			_, _ = s.Interrupt(id, a.ID, a.Token, "checker preparation or result persistence failed; inspect runtime and resume verify")
		}
	}()
	leaseCtx, leaseCancel := context.WithCancel(context.Background())
	defer leaseCancel()
	keeper, e := s.KeepLease(leaseCtx, id, a, 20*time.Second, leaseCancel)
	if e != nil {
		return n, e
	}
	defer keeper.Stop()
	var c Check
	for _, x := range t.Definition.Checks {
		if x.ID == check {
			c = x
		}
	}
	ev := Evidence{Check: check, Candidate: t.Candidate, Revision: t.Definition.Revision, PlanRevision: t.Plan.Revision, Result: "blocked", Source: "command", Executor: executor, Environment: runtime.GOOS + "/" + runtime.GOARCH, At: time.Now().UTC(), ArtifactHashes: map[string]string{}}
	b, _ := json.Marshal(c)
	ev.Config = store.Hash(b)
	result := StepResult{Status: "blocked", Summary: "no executable checker configured; provide the declared model/scenario/human capability with inspected inputs and evidence"}
	if len(c.Command) > 0 {
		seconds := c.Timeout
		if seconds <= 0 {
			seconds = 60
		}
		deadline := time.Now().UTC().Add(time.Duration(seconds) * time.Second)
		if _, e = s.Progress(id, a.ID, a.Token, Progress{Summary: "检查已启动", Next: "等待检查结束并保留日志", Stage: "check:" + check, ExpectedUntil: &deadline, EvidenceKind: "diagnostic"}); e != nil {
			return n, e
		}
		ctx, cancel := context.WithDeadline(leaseCtx, deadline)
		defer cancel()
		cmd := process.Command(ctx, c.Command[0], c.Command[1:]...)
		cmd.Dir = a.Workspace
		path := filepath.Join(s.Store.Dir, "artifacts", a.ID+".log")
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return Next{}, e
		}
		f, e := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
		if e != nil {
			return Next{}, e
		}
		cmd.Stdout = f
		cmd.Stderr = f
		background := false
		runErr := cmd.Start()
		if runErr == nil {
			if e = s.RecordProcess(id, a.ID, a.Token, cmd.Process.Pid); e != nil {
				cancel()
				_ = cmd.Wait()
				f.Close()
				_, _ = s.Interrupt(id, a.ID, a.Token, "checker process registration failed: "+e.Error())
				return n, e
			}
			runErr = cmd.Wait()
			if process.GroupAlive(cmd.Process.Pid) {
				background = true
				_ = process.StopGroup(cmd.Process.Pid)
				runErr = Error("BACKGROUND_PROCESSES", "checker left a process group alive")
			}
		}
		_ = f.Close()
		if leaseErr := keeper.Err(); leaseErr != nil {
			_, _ = s.Interrupt(id, a.ID, a.Token, "lease renewal failed; checker stopped: "+leaseErr.Error())
			return n, Error("LEASE_LOST", leaseErr.Error())
		}
		ref := "runtime:artifacts/" + a.ID + ".log"
		ev.Artifacts = []string{ref}
		result.Artifacts = ev.Artifacts
		result.Status = "pass"
		result.Summary = "command completed and acceptance coverage recorded"
		if runErr != nil {
			result.Status = "fail"
			result.Summary = runErr.Error()
		}
		if errors.Is(runErr, exec.ErrNotFound) || errors.Is(runErr, os.ErrNotExist) {
			result.Status = "blocked"
			result.Summary = "checker executable unavailable: " + runErr.Error()
		}
		if background {
			result.Status = "blocked"
			result.Summary = "checker left background processes; workspace requires recovery"
		}
		if ctx.Err() != nil {
			result.Status = "blocked"
			result.Summary = "checker timeout"
		}
		after, err := Fingerprint(a.Workspace)
		if err != nil || after != a.Input {
			result.Status = "blocked"
			result.Summary = "input changed during verification; result invalid"
		}
		for _, p := range c.Artifacts {
			data, err := s.artifactAt(a.Workspace, p)
			if err != nil {
				result.Status = "blocked"
				result.Summary = "missing required artifact: " + p
			} else {
				ev.Artifacts = append(ev.Artifacts, p)
				ev.ArtifactHashes[p] = store.Hash(data)
			}
		}
		if c.Kind == "ui" {
			image := false
			for _, r := range c.Artifacts {
				if data, err := s.artifactAt(a.Workspace, r); err == nil {
					mime := http.DetectContentType(data)
					if mime == "image/png" || mime == "image/jpeg" {
						image = true
					}
				}
			}
			if !image {
				result.Status = "blocked"
				result.Summary = "UI checker requires a real screenshot artifact"
			}
		}
		if c.Kind == "ux" && len(c.Artifacts) == 0 {
			result.Status = "blocked"
			result.Summary = "UX checker requires an interaction report or trace artifact"
		}
		data, _ := s.artifact(ref)
		ev.ArtifactHashes[ref] = store.Hash(data)
	}
	ev.Result = result.Status
	ev.Reason = result.Summary
	n, e = s.submit(Submission{id, a.ID, a.Token, ID("verify-"), result}, &ev)
	if e == nil {
		finished = true
	}
	return n, e
}
func (s *Service) Review(id, check, actor, reason string, refs []string) (Next, error) {
	return s.review(id, check, actor, reason, "", "pass", refs, nil, "", "", "")
}

type ReviewExpectation struct {
	Candidate          string
	DefinitionRevision int
	PlanRevision       int
}

func (s *Service) ReviewExact(id, check, actor, reason string, refs []string, expected ReviewExpectation) (Next, error) {
	return s.review(id, check, actor, reason, "", "pass", refs, &expected, "", "", "")
}

// ReviewResultExact records a specific human outcome against the version the
// reviewer saw. The request key makes a lost HTTP response safe to retry.
func (s *Service) ReviewResultExact(id, check, actor, reason, recovery, result string, refs []string, requestKey string, expected ReviewExpectation) (Next, error) {
	return s.ReviewResultWithNoteExact(id, check, actor, reason, recovery, result, refs, requestKey, expected, "")
}

// ReviewResultWithNoteExact lets a person explicitly apply a saved preliminary
// opinion while recording a real human check against the current candidate.
func (s *Service) ReviewResultWithNoteExact(id, check, actor, reason, recovery, result string, refs []string, requestKey string, expected ReviewExpectation, noteID string) (Next, error) {
	if strings.TrimSpace(requestKey) == "" || len(requestKey) > 128 {
		return Next{}, Error("IDEMPOTENCY_KEY_REQUIRED", "a unique review request key is required")
	}
	request, _ := json.Marshal(struct {
		Task, Check, Actor, Reason, Recovery, Result string
		Artifacts                                    []string
		Expected                                     ReviewExpectation
	}{id, check, actor, reason, recovery, result, refs, expected})
	key, hash := "human-review:"+requestKey, store.Hash(request)
	if noteID != "" {
		hash = store.Hash(append(request, []byte("\nreview-note:"+noteID)...))
	}
	if previous, found, err := s.reviewReplay(key, hash); found || err != nil {
		return previous, err
	}
	return s.review(id, check, actor, reason, recovery, result, refs, &expected, key, hash, noteID)
}

func (s *Service) reviewReplay(key, hash string) (Next, bool, error) {
	var oldHash, raw string
	err := s.Store.DB.QueryRow("SELECT hash,response FROM submissions WHERE key=?", key).Scan(&oldHash, &raw)
	if err == sql.ErrNoRows {
		return Next{}, false, nil
	}
	if err != nil {
		return Next{}, false, err
	}
	if oldHash != hash {
		return Next{}, true, Error("IDEMPOTENCY_CONFLICT", key)
	}
	var previous Next
	if err = json.Unmarshal([]byte(raw), &previous); err != nil {
		return Next{}, true, err
	}
	return previous, true, s.Store.Flush()
}

func (s *Service) review(id, check, actor, reason, recovery, result string, refs []string, expected *ReviewExpectation, key, hash, noteID string) (Next, error) {
	if result != "pass" && result != "fail" && result != "blocked" {
		return Next{}, Error("INVALID_RESULT", "select pass, fail or blocked")
	}
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" || (result != "blocked" && len(refs) == 0) {
		return Next{}, Error("HUMAN_EVIDENCE_REQUIRED", "actor and observation are required; pass and fail require actual evidence")
	}
	if result != "pass" && strings.TrimSpace(recovery) == "" {
		return Next{}, Error("RECOVERY_REQUIRED", "describe the correction or conditions needed to continue")
	}
	t, e := s.Get(id)
	if e != nil {
		return Next{}, e
	}
	if expected != nil {
		if expected.Candidate == "" || expected.DefinitionRevision <= 0 || expected.PlanRevision <= 0 {
			return Next{}, Error("REVIEW_VERSION_REQUIRED", "refresh the task and review its exact candidate and revisions")
		}
		if expected.Candidate != t.Candidate || expected.DefinitionRevision != t.Definition.Revision || expected.PlanRevision != t.Plan.Revision {
			return Next{}, Error("REVIEW_VERSION_MISMATCH", "review the updated candidate before recording a result")
		}
	}
	var c Check
	for _, x := range t.Definition.Checks {
		if x.ID == check {
			c = x
		}
	}
	if c.ID == "" || len(c.Command) > 0 {
		return Next{}, Error("MANUAL_CHECK_REQUIRED", check)
	}
	if EffectiveCheckMethod(c) != "human" {
		return Next{}, Error("CHECK_METHOD_MISMATCH", "declared check capability cannot be replaced with human review")
	}
	if noteID != "" {
		var note *HumanReviewNote
		for i := range t.HumanReviewNotes {
			if t.HumanReviewNotes[i].ID == noteID {
				note = &t.HumanReviewNotes[i]
				break
			}
		}
		if note == nil {
			return Next{}, Error("REVIEW_NOTE_MISSING", noteID)
		}
		if note.Status != "pending" {
			return Next{}, Error("REVIEW_NOTE_ALREADY_CONFIRMED", noteID)
		}
		if note.Check != "" && note.Check != check {
			return Next{}, Error("REVIEW_NOTE_CHECK_MISMATCH", check)
		}
		if note.DefinitionRevision != t.Definition.Revision || note.PlanRevision != t.Plan.Revision {
			return Next{}, Error("REVIEW_NOTE_VERSION_MISMATCH", "record a new opinion for the current definition and plan")
		}
	}
	workspace := t.CandidateWorkspace
	if workspace == "" {
		workspace = s.Root
	}
	hashes := map[string]string{}
	image := false
	for _, r := range refs {
		b, e := s.artifactAt(workspace, r)
		if e != nil {
			return Next{}, e
		}
		hashes[r] = store.Hash(b)
		if strings.HasSuffix(strings.ToLower(r), ".png") || strings.HasSuffix(strings.ToLower(r), ".jpg") || strings.HasSuffix(strings.ToLower(r), ".jpeg") {
			if mime := http.DetectContentType(b); mime == "image/png" || mime == "image/jpeg" {
				image = true
			}
		}
	}
	if c.Kind == "ui" && result != "blocked" && !image {
		return Next{}, Error("SCREENSHOT_REQUIRED", check)
	}
	n, e := s.claim(id, check, actor, "", "", expected)
	if e != nil {
		if key != "" {
			if previous, found, err := s.reviewReplay(key, hash); found || err != nil {
				return previous, err
			}
		}
		return n, e
	}
	a := n.Attempt
	config, _ := json.Marshal(c)
	ev := Evidence{ReviewNoteID: noteID, Check: check, Candidate: t.Candidate, Revision: t.Definition.Revision, PlanRevision: t.Plan.Revision, Config: store.Hash(config), Result: result, Source: "human:" + actor, Executor: actor, Recovery: recovery, Environment: runtime.GOOS + "/" + runtime.GOARCH, Artifacts: refs, ArtifactHashes: hashes, Reason: reason, At: time.Now().UTC()}
	step := StepResult{Status: result, Summary: reason, Artifacts: refs}
	if result == "blocked" {
		step.Blocker = &Blocker{Category: "environment", Reason: reason, Scope: t.Definition.Scope, Tried: reason, Recovery: recovery, ResumeAt: "verify"}
	}
	if key == "" {
		key = ID("human-")
	}
	n, e = s.submitWithHash(Submission{id, a.ID, a.Token, key, step}, &ev, hash)
	if e != nil {
		// No process was started for a human review. Release only this attempt
		// after a rejected result; never pause or alter another executor.
		_, _ = s.mutate(id, "review_aborted", func(tx *store.Tx, current *Task) error {
			attempt, err := findAttempt(current, a.ID, a.Token)
			if err != nil || attempt.PID != 0 {
				return Error("STALE_TOKEN", a.ID)
			}
			now := time.Now().UTC()
			attempt.Status, attempt.Reason, attempt.Ended = "aborted", "human result was not recorded", &now
			return nil
		})
	}
	return n, e
}
func (s *Service) Artifact(ref string) ([]byte, error) { return s.artifact(ref) }
func (s *Service) RecoverDefinitions() error {
	if e := s.recoverRecords(); e != nil {
		return e
	}
	paths, e := filepath.Glob(filepath.Join(s.Root, ".ael/work-items/*.md"))
	if e != nil {
		return e
	}
	for _, p := range paths {
		d, b, e := readDefinition(p)
		if e != nil {
			return e
		}
		var count int
		if e = s.Store.DB.QueryRow("SELECT count(*) FROM tasks WHERE id=?", d.ID).Scan(&count); e != nil {
			return e
		}
		if count > 0 {
			continue
		}
		plan := MakePlan(&d)
		policyHash, e := s.policyHash()
		if e != nil {
			return e
		}
		t := Task{PolicyHash: policyHash, Legacy: d.Legacy, Definition: d, DefinitionHash: store.Hash(b), Plan: plan, Step: "define", Status: "open", Created: time.Now().UTC(), Failure: "restored definition; no prior leases or evidence"}
		outcome, e := os.ReadFile(filepath.Join(s.Root, ".ael/outcomes/"+d.ID+".json"))
		if e == nil {
			var historic Outcome
			if json.Unmarshal(outcome, &historic) == nil && historic.ID == d.ID && historic.DefinitionRevision == d.Revision && (historic.Status == "completed" || historic.Status == "cancelled") {
				t.Status = historic.Status
				t.Completed = historic.Completed
				t.Historical = true
				t.Result = historic.Result
				t.Candidate = historic.Candidate
				t.Step = "done"
			}
		}
		if e = s.Store.Write(func(tx *store.Tx) error {
			if e := s.save(tx, &t); e != nil {
				return e
			}
			return tx.Event(d.ID, "definition_recovered", map[string]string{"result": t.Result})
		}); e != nil {
			return e
		}
	}
	return s.PublishRecords()
}
func (s *Service) ImportTask(t Task) error {
	policyHash, e := s.policyHash()
	if e != nil {
		return e
	}
	t.PolicyHash = policyHash
	p, e := s.taskPath(t.Definition.ID)
	if e != nil {
		return e
	}
	b := definitionBytes(t.Definition)
	t.DefinitionHash = store.Hash(b)
	e = s.Store.Write(func(tx *store.Tx) error {
		current, err := load(tx, t.Definition.ID)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil && current.EpicMembership != nil && !equivalent(current.EpicMembership, t.EpicMembership) {
			return Error("TASK_MEMBERSHIP_CONFLICT", "import would replace an existing epic association: "+t.Definition.ID)
		}
		if err == nil && current.EpicPlan != nil && !equivalent(current.EpicPlan, t.EpicPlan) {
			return Error("TASK_PLAN_CONFLICT", "import would replace an existing decomposition: "+t.Definition.ID)
		}
		if err == nil && len(current.EpicPlanHistory) > 0 && !equivalent(current.EpicPlanHistory, t.EpicPlanHistory) {
			return Error("TASK_PLAN_CONFLICT", "import would replace decomposition history: "+t.Definition.ID)
		}
		if e := s.save(tx, &t); e != nil {
			return e
		}
		if e := tx.File(p, b); e != nil {
			return e
		}
		if t.Status == "completed" || t.Status == "cancelled" {
			summary := OutcomeDocument(t)
			out, e := store.SafePath(s.Root, ".ael/outcomes/"+t.Definition.ID+".json")
			if e != nil {
				return e
			}
			if e = tx.File(out, summary); e != nil {
				return e
			}
		}
		return tx.Event(t.Definition.ID, "legacy_imported", t.Legacy)
	})
	if e == nil {
		e = s.Store.Flush()
	}
	return e
}
func (s *Service) DeleteImported(id string) error {
	e := s.Store.Write(func(tx *store.Tx) error {
		t, err := load(tx, id)
		if err != nil {
			return err
		}
		if t.EpicMembership != nil {
			return Error("TASK_REFERENCED", "epic membership retained by "+t.EpicMembership.Parent)
		}
		if hasWriter(t) {
			return Error("RUNNING_ATTEMPT", id)
		}
		var members int
		if err = tx.Row("SELECT count(*) FROM tasks WHERE json_extract(body,'$.epic_membership.parent')=?", id).Scan(&members); err != nil {
			return err
		}
		if members > 0 {
			return Error("TASK_REFERENCED", "epic has associated leaves")
		}
		for _, rel := range []string{".ael/records/" + id + ".json", ".ael/work-items/" + id + ".md", ".ael/outcomes/" + id + ".json"} {
			path, err := store.SafePath(s.Root, rel)
			if err != nil {
				return err
			}
			if err = tx.DeleteFile(path); err != nil {
				return err
			}
		}
		if _, err = tx.Exec("DELETE FROM metadata WHERE key=?", "record:"+id); err != nil {
			return err
		}
		_, err = tx.Exec("DELETE FROM tasks WHERE id=?", id)
		return err
	})
	if e == nil {
		e = s.Store.Flush()
	}
	return e
}

var _ = fmt.Sprint

// RecordOptionalStatus records administrative dispositions; it cannot satisfy a required gate.
func (s *Service) RecordOptionalStatus(id, check, status, actor, reason string) (Next, error) {
	if status != "not_run" && status != "not_applicable" && status != "waived" {
		return Next{}, Error("INVALID_CHECK_STATUS", status)
	}
	if actor == "" || reason == "" {
		return Next{}, Error("REASON_REQUIRED", "optional disposition")
	}
	return s.mutate(id, "optional_check_disposition", func(tx *store.Tx, t *Task) error {
		if t.Step != "verify" {
			return Error("WRONG_STEP", t.Step)
		}
		for _, c := range t.Definition.Checks {
			if c.ID == check {
				if c.Required {
					return Error("REQUIRED_CHECK_CANNOT_BE_WAIVED", check)
				}
				b, _ := json.Marshal(c)
				t.Evidence = append(t.Evidence, Evidence{Check: check, Candidate: t.Candidate, Revision: t.Definition.Revision, PlanRevision: t.Plan.Revision, Config: store.Hash(b), Result: status, Source: "human:" + actor, Reason: reason, At: time.Now().UTC()})
				return nil
			}
		}
		return Error("CHECK_MISSING", check)
	})
}

// RestoreImportedSnapshot restores a previous migration revision without losing runtime history.
func (s *Service) RestoreImportedSnapshot(t Task, definition, outcome []byte) error {
	p, e := s.taskPath(t.Definition.ID)
	if e != nil {
		return e
	}
	e = s.Store.Write(func(tx *store.Tx) error {
		current, err := load(tx, t.Definition.ID)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil && current.EpicMembership != nil && !equivalent(current.EpicMembership, t.EpicMembership) {
			return Error("TASK_MEMBERSHIP_CONFLICT", "migration snapshot would replace an existing epic association: "+t.Definition.ID)
		}
		if err == nil && current.EpicPlan != nil && !equivalent(current.EpicPlan, t.EpicPlan) {
			return Error("TASK_PLAN_CONFLICT", "migration snapshot would replace an existing decomposition: "+t.Definition.ID)
		}
		if err == nil && len(current.EpicPlanHistory) > 0 && !equivalent(current.EpicPlanHistory, t.EpicPlanHistory) {
			return Error("TASK_PLAN_CONFLICT", "migration snapshot would replace decomposition history: "+t.Definition.ID)
		}
		b, e := json.Marshal(t)
		if e != nil {
			return e
		}
		completed := ""
		if t.Completed != nil {
			completed = t.Completed.Format(time.RFC3339Nano)
		}
		if _, e = tx.Exec("INSERT INTO tasks(id,body,status,step,updated,completed) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body,status=excluded.status,step=excluded.step,updated=excluded.updated,completed=excluded.completed", t.Definition.ID, string(b), t.Status, t.Step, t.Updated.Format(time.RFC3339Nano), completed); e != nil {
			return e
		}
		if e = s.writeRecord(tx, t); e != nil {
			return e
		}
		if e = tx.File(p, definition); e != nil {
			return e
		}
		out, e := store.SafePath(s.Root, ".ael/outcomes/"+t.Definition.ID+".json")
		if e != nil {
			return e
		}
		if len(outcome) > 0 {
			if e = tx.File(out, outcome); e != nil {
				return e
			}
		} else if e = tx.DeleteFile(out); e != nil {
			return e
		}
		return tx.Event(t.Definition.ID, "migration_revision_restored", map[string]string{"result": t.Result})
	})
	if e == nil {
		e = s.Store.Flush()
	}
	return e
}
