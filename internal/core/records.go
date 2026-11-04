package core

import (
	"ael/internal/store"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// TaskRecord is a reviewable checkpoint, not a second live scheduler. It carries
// no leases, process identities, session IDs, workspace paths or approvals.
type TaskRecord struct {
	Schema           int           `json:"schema"`
	ID               string        `json:"id"`
	DefinitionHash   string        `json:"definition_hash"`
	PolicyHash       string        `json:"policy_hash"`
	Revision         int           `json:"definition_revision"`
	Plan             Plan          `json:"plan"`
	Step             string        `json:"current_step"`
	Status           string        `json:"status"`
	Candidate        string        `json:"candidate"`
	Failure          string        `json:"failure"`
	Result           string        `json:"result"`
	Created          time.Time     `json:"created"`
	Updated          time.Time     `json:"updated"`
	Completed        *time.Time    `json:"completed"`
	Feedback         []string      `json:"feedback"`
	TimeLimitSeconds *int          `json:"time_limit_seconds,omitempty"`
	History          RecordHistory `json:"history"`
}
type RecordHistory struct {
	Attempts []RecordAttempt  `json:"attempts"`
	Checks   []OutcomeCheck   `json:"checks"`
	Delivery *OutcomeDelivery `json:"delivery,omitempty"`
}
type RecordAttempt struct {
	ID        string     `json:"id"`
	Unit      string     `json:"unit"`
	Executor  string     `json:"executor"`
	Input     string     `json:"input_version"`
	Candidate string     `json:"candidate_version"`
	Started   time.Time  `json:"started"`
	Ended     *time.Time `json:"ended"`
	Status    string     `json:"status"`
	Reason    string     `json:"reason"`
}
type TaskDeletion struct {
	Schema         int       `json:"schema"`
	ID             string    `json:"id"`
	DefinitionHash string    `json:"definition_hash"`
	At             time.Time `json:"deleted"`
	Reason         string    `json:"reason"`
}

func (s *Service) recordPath(id string) (string, error) {
	return store.SafePath(s.Root, ".ael/records/"+id+".json")
}
func recordOf(t Task) TaskRecord {
	r := TaskRecord{Schema: 1, ID: t.Definition.ID, DefinitionHash: t.DefinitionHash, PolicyHash: t.PolicyHash, Revision: t.Definition.Revision, Plan: t.Plan, Step: t.Step, Status: t.Status, Candidate: t.Candidate, Failure: t.Failure, Result: t.Result, Created: t.Created, Updated: t.Updated, Completed: t.Completed, Feedback: t.Feedback}
	r.Plan.Approved = false
	r.TimeLimitSeconds = t.TimeLimitSeconds
	if t.History != nil {
		r.History = *t.History
		r.History.Attempts = slices.Clone(t.History.Attempts)
		r.History.Checks = slices.Clone(t.History.Checks)
	}
	for _, a := range t.Attempts {
		r.History.Attempts = append(r.History.Attempts, RecordAttempt{a.ID, a.Unit, a.Executor, a.Input, a.Candidate, a.Started, a.Ended, a.Status, a.Reason})
	}
	for _, e := range append(slices.Clone(t.EvidenceHistory), t.Evidence...) {
		hashes := map[string]string{}
		for ref, hash := range e.ArtifactHashes {
			if !filepath.IsAbs(ref) && !strings.Contains(ref, "..") {
				hashes[ref] = hash
			}
		}
		r.History.Checks = append(r.History.Checks, OutcomeCheck{e.Check, e.Result, e.Source, e.Config, e.At, hashes})
	}
	if t.Delivery != nil {
		d := t.Delivery
		r.History.Delivery = &OutcomeDelivery{d.RunID, d.Environment, d.Target, d.Status}
	}
	return r
}
func (s *Service) writeRecord(tx *store.Tx, t Task) error {
	path, e := s.recordPath(t.Definition.ID)
	if e != nil {
		return e
	}
	var known, state string
	e = tx.Row("SELECT body FROM metadata WHERE key=?", "record:"+t.Definition.ID).Scan(&known)
	if e != nil && e != sql.ErrNoRows {
		return e
	}
	_ = tx.Row("SELECT state FROM outbox WHERE path=?", path).Scan(&state)
	if known != "" && state != "pending" {
		b, e := os.ReadFile(path)
		if e != nil {
			return Error("TASK_RECORD_CONFLICT", "checkpoint removed or unreadable: "+t.Definition.ID)
		}
		if store.Hash(b) != known {
			return Error("TASK_RECORD_CONFLICT", "external checkpoint changed; reopen project before writing: "+t.Definition.ID)
		}
	}
	b, e := json.MarshalIndent(recordOf(t), "", "  ")
	if e != nil {
		return e
	}
	if e = tx.File(path, append(b, '\n')); e != nil {
		return e
	}
	_, e = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", "record:"+t.Definition.ID, store.Hash(append(b, '\n')))
	return e
}
func (s *Service) save(tx *store.Tx, t *Task) error {
	if e := save(tx, t); e != nil {
		return e
	}
	return s.writeRecord(tx, *t)
}
func hasWriter(t Task) bool {
	for _, a := range t.Attempts {
		if a.Status == "running" || a.Status == "interrupted" {
			return true
		}
	}
	return false
}

func (s *Service) recoverRecords() error {
	paths, e := filepath.Glob(filepath.Join(s.Root, ".ael/deleted/*.json"))
	if e != nil {
		return e
	}
	var deletions []TaskDeletion
	for _, path := range paths {
		if _, e = store.SafePath(s.Root, filepath.ToSlash(strings.TrimPrefix(path, s.Root+string(os.PathSeparator)))); e != nil {
			return e
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		var d TaskDeletion
		if e = json.Unmarshal(b, &d); e != nil {
			return e
		}
		if d.Schema != 1 || filepath.Base(path) != d.ID+".json" || d.At.IsZero() {
			return Error("INVALID_TASK_DELETION", path)
		}
		deletions = append(deletions, d)
	}
	for len(deletions) > 0 {
		var next []TaskDeletion
		for _, d := range deletions {
			e = s.Store.Write(func(tx *store.Tx) error {
				t, e := load(tx, d.ID)
				if e == sql.ErrNoRows {
					return nil
				}
				if e != nil {
					return e
				}
				if t.DefinitionHash != d.DefinitionHash || t.Updated.After(d.At) {
					return Error("TASK_DELETE_CONFLICT", d.ID)
				}
				return s.deleteTask(tx, t, d.Reason, false)
			})
			if e != nil {
				if strings.HasPrefix(e.Error(), "TASK_REFERENCED:") {
					next = append(next, d)
					continue
				}
				return e
			}
		}
		if len(next) == len(deletions) {
			return Error("TASK_DELETE_CONFLICT", "remaining tasks reference deleted tasks")
		}
		deletions = next
	}

	paths, e = filepath.Glob(filepath.Join(s.Root, ".ael/records/*.json"))
	if e != nil {
		return e
	}
	for _, path := range paths {
		if _, e = store.SafePath(s.Root, filepath.ToSlash(strings.TrimPrefix(path, s.Root+string(os.PathSeparator)))); e != nil {
			return e
		}
		if e = s.Store.Write(func(tx *store.Tx) error {
			var state string
			_ = tx.Row("SELECT state FROM outbox WHERE path=?", path).Scan(&state)
			if state == "pending" || state == "delete_pending" {
				return nil
			}
			b, e := os.ReadFile(path)
			if os.IsNotExist(e) {
				return nil
			}
			if e != nil {
				return e
			}
			var r TaskRecord
			if e = json.Unmarshal(b, &r); e != nil {
				return e
			}
			if r.Schema != 1 || filepath.Base(path) != r.ID+".json" || r.ID == "" || r.Created.IsZero() || !slices.Contains([]string{"open", "completed", "cancelled"}, r.Status) {
				return Error("INVALID_TASK_RECORD", path)
			}
			var known string
			e = tx.Row("SELECT body FROM metadata WHERE key=?", "record:"+r.ID).Scan(&known)
			if e != nil && e != sql.ErrNoRows {
				return e
			}
			if known == store.Hash(b) {
				return nil
			}
			old, e := load(tx, r.ID)
			if e != nil && e != sql.ErrNoRows {
				return e
			}
			if e == nil && hasWriter(old) {
				return Error("TASK_RECORD_CONFLICT", "local writer exists: "+r.ID)
			}
			p, e := s.taskPath(r.ID)
			if e != nil {
				return e
			}
			d, raw, e := readDefinition(p)
			if e != nil {
				return e
			}
			if d.ID != r.ID || d.Revision != r.Revision || store.Hash(raw) != r.DefinitionHash {
				return Error("TASK_RECORD_DEFINITION_MISMATCH", r.ID)
			}
			if e = Validate(d); e != nil {
				return e
			}
			// The portable plan is history, not permission to skip responsibilities.
			// Regenerate its steps from the authoritative definition/risk signals.
			plan := MakePlan(&d)
			if r.Plan.Revision < 1 {
				return Error("INVALID_RECORD_PLAN_REVISION", r.ID)
			}
			plan.Revision = r.Plan.Revision
			plan.Approved = false
			if !slices.Contains(plan.Steps, r.Step) && !(r.Step == "done" && r.Status != "open") {
				return Error("INVALID_RECORD_STEP", r.Step)
			}
			t := Task{PolicyHash: r.PolicyHash, Definition: d, DefinitionHash: r.DefinitionHash, Plan: plan, Step: r.Step, Status: r.Status, Candidate: r.Candidate, Failure: r.Failure, Result: r.Result, Created: r.Created, Updated: r.Updated, Completed: r.Completed, Feedback: r.Feedback, History: &r.History, Historical: r.Status != "open"}
			t.Plan.Approved = false
			t.TimeLimitSeconds = r.TimeLimitSeconds
			if t.Status == "open" {
				t.Paused = true
				t.Candidate = ""
				// A clone has no live evidence. Re-run verification of the current files.
				if t.Step == "verify" || t.Step == "delivery" {
					t.Step = "execute"
				}
				t.Confirmation = &Confirmation{"restore", "checkpoint imported from project; review files and historical checks before resuming", d.Revision, r.Plan.Revision, ""}
			}
			// Do not publish the local recovery projection back into the imported file.
			body, e := json.Marshal(t)
			if e != nil {
				return e
			}
			completed := ""
			if t.Completed != nil {
				completed = t.Completed.Format(time.RFC3339Nano)
			}
			if _, e = tx.Exec("INSERT INTO tasks(id,body,status,step,updated,completed) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body,status=excluded.status,step=excluded.step,updated=excluded.updated,completed=excluded.completed", r.ID, string(body), t.Status, t.Step, t.Updated.Format(time.RFC3339Nano), completed); e != nil {
				return e
			}
			if _, e = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", "record:"+r.ID, store.Hash(b)); e != nil {
				return e
			}
			return tx.Event(r.ID, "record_recovered", map[string]string{"status": t.Status, "checkpoint": r.Step})
		}); e != nil {
			return e
		}
	}
	return s.Store.Flush()
}

// PublishRecords upgrades pre-record databases without altering definitions or budgets.
func (s *Service) PublishRecords() error {
	rows, e := s.Store.DB.Query("SELECT id FROM tasks WHERE NOT EXISTS(SELECT 1 FROM metadata WHERE key='record:'||tasks.id)")
	if e != nil {
		return e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		if e = s.Store.Write(func(tx *store.Tx) error {
			t, e := load(tx, id)
			if e != nil {
				return e
			}
			return s.writeRecord(tx, t)
		}); e != nil {
			return e
		}
	}
	return s.Store.Flush()
}

func (s *Service) Delete(id, reason string) (map[string]any, error) {
	if e := s.active(); e != nil {
		return nil, e
	}
	if strings.TrimSpace(reason) == "" {
		return nil, Error("REASON_REQUIRED", "delete")
	}
	if _, e := s.Get(id); e != nil {
		return nil, e
	}
	e := s.Store.Write(func(tx *store.Tx) error {
		t, e := load(tx, id)
		if e != nil {
			return e
		}
		return s.deleteTask(tx, t, reason, true)
	})
	if e == nil {
		e = s.Store.Flush()
	}
	return map[string]any{"next_action": "deleted", "deleted": id}, e
}
func (s *Service) deleteTask(tx *store.Tx, t Task, reason string, tombstone bool) error {
	id := t.Definition.ID
	if hasWriter(t) {
		return Error("RUNNING_ATTEMPT", "stop and resume --ack-stopped before deleting")
	}
	rows, e := tx.Conn.QueryContext(context.Background(), "SELECT body FROM tasks WHERE id!=?", id)
	if e != nil {
		return e
	}
	var others []Task
	for rows.Next() {
		var body string
		if e = rows.Scan(&body); e != nil {
			rows.Close()
			return e
		}
		var other Task
		if e = json.Unmarshal([]byte(body), &other); e != nil {
			rows.Close()
			return e
		}
		others = append(others, other)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, other := range others {
		if slices.Contains(other.Definition.Dependencies, id) || other.Definition.FeedbackOf == id || other.Definition.Parent == id {
			return Error("TASK_REFERENCED", other.Definition.ID)
		}
		if slices.Contains(other.Feedback, id) {
			other.Feedback = slices.DeleteFunc(other.Feedback, func(x string) bool { return x == id })
			if e = s.save(tx, &other); e != nil {
				return e
			}
		}
	}
	for _, rel := range []string{".ael/work-items/" + id + ".md", ".ael/records/" + id + ".json", ".ael/outcomes/" + id + ".json"} {
		p, e := store.SafePath(s.Root, rel)
		if e != nil {
			return e
		}
		if e = tx.DeleteFile(p); e != nil {
			return e
		}
	}
	// Remove only this task's owned runtime files, never project deliverables.
	for _, a := range t.Attempts {
		if strings.ContainsAny(a.ID, "/\\") || a.ID == "" {
			return Error("INVALID_ATTEMPT_ID", a.ID)
		}
		for _, rel := range []string{"artifacts/" + a.ID + ".log", "artifacts/" + a.ID} {
			p, e := store.SafePath(s.Store.Dir, rel)
			if e != nil {
				return e
			}
			st, e := os.Stat(p)
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return e
			}
			if !st.IsDir() {
				if e = tx.DeleteFile(p); e != nil {
					return e
				}
				continue
			}
			if e = filepath.WalkDir(p, func(path string, d os.DirEntry, e error) error {
				if e != nil {
					return e
				}
				if d.IsDir() {
					return nil
				}
				if d.Type()&os.ModeSymlink != 0 {
					return Error("SYMLINK_PATH", path)
				}
				return tx.DeleteFile(path)
			}); e != nil {
				return e
			}
		}
	}
	if tombstone {
		d := TaskDeletion{1, id, t.DefinitionHash, time.Now().UTC(), reason}
		b, _ := json.MarshalIndent(d, "", "  ")
		p, e := store.SafePath(s.Root, ".ael/deleted/"+id+".json")
		if e != nil {
			return e
		}
		if e = tx.File(p, b); e != nil {
			return e
		}
	}
	for _, q := range []string{"DELETE FROM tasks WHERE id=?", "DELETE FROM events WHERE task_id=?", "DELETE FROM workspace_locks WHERE task=?", "DELETE FROM submissions WHERE json_extract(response,'$.task.definition.id')=?"} {
		if _, e = tx.Exec(q, id); e != nil {
			return e
		}
	}
	if _, e = tx.Exec("DELETE FROM metadata WHERE key=?", "record:"+id); e != nil {
		return e
	}
	return tx.Event("", "task_deleted", map[string]string{"id": id, "reason": reason})
}
