package core

import (
	"ael/internal/process"
	"ael/internal/store"
	"context"
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
		ctx, cancel := context.WithTimeout(leaseCtx, time.Duration(seconds)*time.Second)
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
	return s.review(id, check, actor, reason, refs, nil)
}

type ReviewExpectation struct {
	Candidate          string
	DefinitionRevision int
	PlanRevision       int
}

func (s *Service) ReviewExact(id, check, actor, reason string, refs []string, expected ReviewExpectation) (Next, error) {
	return s.review(id, check, actor, reason, refs, &expected)
}
func (s *Service) review(id, check, actor, reason string, refs []string, expected *ReviewExpectation) (Next, error) {
	if actor == "" || reason == "" || len(refs) == 0 {
		return Next{}, Error("HUMAN_EVIDENCE_REQUIRED", "actor, reason, artifact references")
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
			return Next{}, Error("REVIEW_VERSION_MISMATCH", "review the updated candidate before confirming human pass")
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
	if c.Method == "model" || c.Method == "scenario" {
		return Next{}, Error("CHECK_METHOD_MISMATCH", "declared model/scenario capability cannot be replaced with human pass")
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
		if strings.HasSuffix(strings.ToLower(r), ".png") || strings.HasSuffix(strings.ToLower(r), ".jpg") {
			if mime := http.DetectContentType(b); mime == "image/png" || mime == "image/jpeg" {
				image = true
			}
		}
	}
	if c.Kind == "ui" && !image {
		return Next{}, Error("SCREENSHOT_REQUIRED", check)
	}
	n, e := s.Claim(id, check, actor, "", "")
	if e != nil {
		return n, e
	}
	a := n.Attempt
	config, _ := json.Marshal(c)
	ev := Evidence{Check: check, Candidate: t.Candidate, Revision: t.Definition.Revision, PlanRevision: t.Plan.Revision, Config: store.Hash(config), Result: "pass", Source: "human:" + actor, Environment: runtime.GOOS + "/" + runtime.GOARCH, Artifacts: refs, ArtifactHashes: hashes, Reason: reason, At: time.Now().UTC()}
	return s.submit(Submission{id, a.ID, a.Token, ID("human-"), StepResult{Status: "pass", Summary: reason, Artifacts: refs}}, &ev)
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
		p, e := s.recordPath(id)
		if e != nil {
			return e
		}
		if e = tx.DeleteFile(p); e != nil {
			return e
		}
		if _, e = tx.Exec("DELETE FROM metadata WHERE key=?", "record:"+id); e != nil {
			return e
		}
		_, e = tx.Exec("DELETE FROM tasks WHERE id=?", id)
		return e
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
		if len(outcome) > 0 {
			out, e := store.SafePath(s.Root, ".ael/outcomes/"+t.Definition.ID+".json")
			if e != nil {
				return e
			}
			if e = tx.File(out, outcome); e != nil {
				return e
			}
		}
		return tx.Event(t.Definition.ID, "migration_revision_restored", map[string]string{"result": t.Result})
	})
	if e == nil {
		e = s.Store.Flush()
	}
	return e
}
