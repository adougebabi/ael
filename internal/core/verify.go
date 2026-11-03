package core

import (
	"ael/internal/process"
	"ael/internal/store"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

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
	var c Check
	for _, x := range t.Definition.Checks {
		if x.ID == check {
			c = x
		}
	}
	ev := Evidence{Check: check, Candidate: t.Candidate, Revision: t.Definition.Revision, PlanRevision: t.Plan.Revision, Result: "blocked", Source: "command", Environment: runtime.GOOS + "/" + runtime.GOARCH, At: time.Now().UTC(), ArtifactHashes: map[string]string{}}
	b, _ := json.Marshal(c)
	ev.Config = store.Hash(b)
	result := StepResult{Status: "blocked", Summary: "no executable checker configured; use explicit human review with artifacts"}
	if len(c.Command) > 0 {
		seconds := c.Timeout
		if seconds <= 0 {
			seconds = 60
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
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
			_ = s.RecordProcess(id, a.ID, a.Token, cmd.Process.Pid)
			done := make(chan struct{})
			go func() {
				tick := time.NewTicker(20 * time.Second)
				defer tick.Stop()
				for {
					select {
					case <-done:
						return
					case <-tick.C:
						_, _ = s.Heartbeat(id, a.ID, a.Token)
					}
				}
			}()
			runErr = cmd.Wait()
			if process.GroupAlive(cmd.Process.Pid) {
				background = true
				_ = process.StopGroup(cmd.Process.Pid)
				runErr = Error("BACKGROUND_PROCESSES", "checker left a process group alive")
			}
			close(done)
		}
		_ = f.Close()
		ref := "runtime:artifacts/" + a.ID + ".log"
		ev.Artifacts = []string{ref}
		result.Artifacts = ev.Artifacts
		result.Status = "pass"
		result.Summary = "command completed and acceptance coverage recorded"
		if runErr != nil {
			result.Status = "fail"
			result.Summary = runErr.Error()
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
	return s.submit(Submission{id, a.ID, a.Token, ID("verify-"), result}, &ev)
}
func (s *Service) Review(id, check, actor, reason string, refs []string) (Next, error) {
	if actor == "" || reason == "" || len(refs) == 0 {
		return Next{}, Error("HUMAN_EVIDENCE_REQUIRED", "actor, reason, artifact references")
	}
	t, e := s.Get(id)
	if e != nil {
		return Next{}, e
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
		t := Task{Legacy: d.Legacy, Definition: d, DefinitionHash: store.Hash(b), Plan: plan, Step: "define", Status: "open", Created: time.Now().UTC(), Failure: "restored definition; no prior leases or evidence"}
		outcome, e := os.ReadFile(filepath.Join(s.Root, ".ael/outcomes/"+d.ID+".json"))
		if e == nil {
			var historic Outcome
			if json.Unmarshal(outcome, &historic) == nil && historic.ID == d.ID && historic.DefinitionRevision == d.Revision && (historic.Status == "completed" || historic.Status == "cancelled") {
				t.Status = historic.Status
				t.Completed = historic.Completed
				t.Historical = true
				t.Result = "historical summary; original runtime evidence not restored"
				t.Step = "done"
			}
		}
		if e = s.Store.Write(func(tx *store.Tx) error {
			if e := save(tx, &t); e != nil {
				return e
			}
			return tx.Event(d.ID, "definition_recovered", map[string]string{"result": t.Result})
		}); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) ImportTask(t Task) error {
	p, e := s.taskPath(t.Definition.ID)
	if e != nil {
		return e
	}
	b := definitionBytes(t.Definition)
	t.DefinitionHash = store.Hash(b)
	e = s.Store.Write(func(tx *store.Tx) error {
		if e := save(tx, &t); e != nil {
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
	_, e := s.Store.DB.Exec("DELETE FROM tasks WHERE id=?", id)
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
