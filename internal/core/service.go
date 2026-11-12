package core

import (
	"ael/internal/process"
	"ael/internal/store"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type Service struct {
	ToolBuild           string
	Store               *store.Store
	Root                string
	Active              bool
	RemoteAuthority     bool
	remoteOperation     *RemoteOperation
	ResolveBasis        func(Definition) ([]BasisReference, error)
	ConstraintCheck     func(Definition) []string
	SnapshotConstraints func() ConstraintResolvers
}

type ConstraintResolvers struct {
	ResolveBasis func(Definition) ([]BasisReference, error)
	Check        func(Definition) []string
}

func ID(prefix string) string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}
func (s *Service) taskPath(id string) (string, error) {
	return store.SafePath(s.Root, ".ael/work-items/"+id+".md")
}
func load(tx *store.Tx, id string) (Task, error) {
	var t Task
	var b string
	e := tx.Row("SELECT body FROM tasks WHERE id=?", id).Scan(&b)
	if e != nil {
		return t, e
	}
	e = json.Unmarshal([]byte(b), &t)
	return t, e
}
func save(tx *store.Tx, t *Task) error {
	t.Updated = time.Now().UTC()
	b, e := json.Marshal(t)
	if e != nil {
		return e
	}
	completed := ""
	if t.Completed != nil {
		completed = t.Completed.Format(time.RFC3339Nano)
	}
	_, e = tx.Exec("INSERT INTO tasks(id,body,status,step,updated,completed) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body,status=excluded.status,step=excluded.step,updated=excluded.updated,completed=excluded.completed", t.Definition.ID, string(b), t.Status, t.Step, t.Updated.Format(time.RFC3339Nano), completed)
	return e
}
func (s *Service) active() error {
	if _, e := os.Lstat(filepath.Join(s.Root, ".ael/tool-update.lock")); e == nil {
		return Error("PROJECT_UPDATING", "wait for the tool update to finish before starting a writer")
	} else if !os.IsNotExist(e) {
		return e
	}
	if !s.Active {
		return Error("SHADOW_MODE", "activate after reviewed migration; read-only functions remain available")
	}
	return nil
}
func (s *Service) Create(d Definition) (Next, error) {
	return s.create(d, nil, "", "")
}

func (s *Service) create(d Definition, membership *EpicMembership, expectedPolicy, planKey string) (Next, error) {
	if e := s.active(); e != nil {
		return Next{}, e
	}
	if d.ID == "" {
		d.ID = ID("WI-")
	}
	if d.Type == "" {
		d.Type = "feature"
	}
	if d.Type == "epic" && d.Parent != "" {
		return Next{}, Error("EPIC_PARENT_INVALID", "epics cannot be child tasks")
	}
	if d.Intent == "" && (d.Type == "qa" || d.Type == "verify") && (d.Goal == "" || d.Goal == "report" || d.Goal == "analysis") {
		d.Intent = "inspect"
		if d.Goal == "" {
			d.Goal = "report"
		}
	}
	if d.Goal == "" {
		d.Goal = "verified"
	}
	if d.Revision == 0 {
		d.Revision = 1
	}
	if d.Preset != "" && !slices.Contains([]string{"fast", "standard", "strict", "research"}, d.Preset) {
		return Next{}, Error("INVALID_PRESET", d.Preset)
	}
	policyHash, e := s.policyHash()
	if e != nil {
		return Next{}, e
	}
	if expectedPolicy != "" && expectedPolicy != policyHash {
		return Next{}, Error("PLAN_STALE_POLICY", "project policy changed since preview")
	}
	if e := s.normalize(&d); e != nil {
		return Next{}, e
	}
	if after, e := s.policyHash(); e != nil || after != policyHash {
		return Next{}, Error("POLICY_CHANGED_DURING_PLAN", "retry with stable policy")
	}

	plan := MakePlan(&d)
	if e := Validate(d); e != nil {
		return Next{}, e
	}
	path, e := s.taskPath(d.ID)
	if e != nil {
		return Next{}, e
	}
	if p, e := store.SafePath(s.Root, ".ael/deleted/"+d.ID+".json"); e != nil {
		return Next{}, e
	} else if _, e = os.Lstat(p); e == nil {
		return Next{}, Error("TASK_ID_DELETED", "use a new stable ID")
	} else if !os.IsNotExist(e) {
		return Next{}, e
	}
	if _, e = os.Lstat(path); e == nil {
		return Next{}, Error("TASK_EXISTS", d.ID)
	}
	b := definitionBytes(d)
	now := time.Now().UTC()
	t := Task{PolicyHash: policyHash, Definition: d, DefinitionHash: store.Hash(b), Plan: plan, Step: "define", Status: "open", Created: now, Updated: now, Attempts: []Attempt{}, Evidence: []Evidence{}, Approvals: []Approval{}, Feedback: []string{}}
	e = s.Store.Write(func(tx *store.Tx) error {
		if err := checkPlanActive(tx, d.Parent, planKey); err != nil {
			return err
		}
		if expectedPolicy != "" {
			current, err := s.policyHash()
			if err != nil || current != expectedPolicy {
				return Error("PLAN_STALE_POLICY", "project policy changed during application")
			}
		}
		var n int
		_ = tx.Row("SELECT count(*) FROM tasks WHERE id=?", d.ID).Scan(&n)
		if n > 0 {
			return Error("TASK_EXISTS", d.ID)
		}
		if d.Parent != "" {
			if e := validateEpicParent(tx, d.Parent, d.ID); e != nil {
				return e
			}
			t.EpicMembership = &EpicMembership{Parent: d.Parent, Required: true}
			if membership != nil {
				if membership.Parent != d.Parent {
					return Error("PLAN_MEMBERSHIP_CONFLICT", d.ID)
				}
				copy := *membership
				t.EpicMembership = &copy
			}
		}
		for _, dep := range d.Dependencies {
			if dep == d.ID {
				return Error("DEPENDENCY_CYCLE", dep)
			}
			if _, e := load(tx, dep); e != nil {
				return Error("DEPENDENCY_MISSING", dep)
			}
		}
		// Plan-created feedback links are applied when the plan finishes or
		// is abandoned, so they do not invalidate later planned task states.
		if d.FeedbackOf != "" {
			old, e := load(tx, d.FeedbackOf)
			if e != nil {
				return e
			}
			if planKey == "" {
				old.Feedback = append(old.Feedback, d.ID)
				e = s.save(tx, &old)
				if e != nil {
					return e
				}
			}
		}
		if planKey != "" {
			e = s.savePlan(tx, &t)
		} else {
			e = s.save(tx, &t)
		}
		if e != nil {
			return e
		}
		if planKey != "" {
			e = tx.FileExpected(path, b, "")
		} else {
			e = tx.File(path, b)
		}
		if e != nil {
			return e
		}
		return tx.Event(d.ID, "created", d)
	})
	if e != nil {
		return Next{}, e
	}
	if e = s.Store.Flush(); e != nil {
		return Next{}, e
	}
	return s.Next(d.ID)
}
func (s *Service) Get(id string) (Task, error) {
	var t Task
	var b string
	e := s.Store.DB.QueryRow("SELECT body FROM tasks WHERE id=?", id).Scan(&b)
	if e != nil {
		return t, e
	}
	if e = json.Unmarshal([]byte(b), &t); e != nil {
		return t, e
	}
	if s.RemoteAuthority {
		return t, nil
	}
	currentPolicy, e := s.policyHash()
	if e != nil {
		return t, e
	}
	if t.Status == "open" && t.PolicyHash != currentPolicy {
		return s.syncPolicy(t)
	}
	path, e := s.taskPath(id)
	if e != nil {
		return t, e
	}
	d, raw, e := readDefinition(path)
	if e != nil {
		return t, e
	}
	if store.Hash(raw) != t.DefinitionHash && equivalent(semantic(d), semantic(t.Definition)) {
		return s.displayEdit(t, d, raw)
	}
	if store.Hash(raw) != t.DefinitionHash && (t.Status == "completed" || t.Status == "cancelled") {
		return t, Error("TERMINAL_DEFINITION_CHANGED", "restore original definition and create linked feedback task")
	}
	if store.Hash(raw) != t.DefinitionHash && t.Status != "completed" && t.Status != "cancelled" {
		return s.syncDefinition(t, d, raw)
	}
	return t, nil
}
func (s *Service) syncDefinition(old Task, d Definition, raw []byte) (Task, error) {
	return s.syncDefinitionExpected(old, d, raw, "", false, "", "", "")
}

func (s *Service) syncDefinitionExpected(old Task, d Definition, raw []byte, expectedPolicy string, refuseWriter bool, planParent, planKey, expectedState string) (Task, error) {
	if d.ID != old.Definition.ID {
		return old, Error("ID_IMMUTABLE", d.ID)
	}
	if d.Parent != old.Definition.Parent {
		return old, Error("PARENT_LINK_REQUIRED", "associate an existing leaf using task link")
	}
	if (d.Type == "epic") != (old.Definition.Type == "epic") {
		return old, Error("EPIC_TYPE_IMMUTABLE", "create an epic separately instead of changing task kind")
	}
	if e := Validate(d); e != nil {
		return old, e
	}
	if expectedPolicy != "" {
		current, err := s.policyHash()
		if err != nil || current != expectedPolicy {
			return old, Error("PLAN_STALE_POLICY", "project policy changed since preview")
		}
	}
	d.Revision = old.Definition.Revision
	if equivalent(semantic(d), semantic(old.Definition)) && old.PolicyHash == func() string { h, _ := s.policyHash(); return h }() {
		return s.displayEditExpected(old, d, definitionBytes(d), expectedPolicy, refuseWriter, planParent, planKey, expectedState)
	}
	d.Revision = old.Definition.Revision + 1
	policyHash, e := s.policyHash()
	if e != nil {
		return old, e
	}
	if e := s.normalize(&d); e != nil {
		return old, e
	}
	if after, e := s.policyHash(); e != nil || after != policyHash {
		return old, Error("POLICY_CHANGED_DURING_PLAN", "retry with stable policy")
	}
	plan := MakePlan(&d)
	plan.Revision = old.Plan.Revision + 1
	boundary := d.Goal != old.Definition.Goal || !equivalent(d.Acceptance, old.Definition.Acceptance) || !equivalent(d.Risk, old.Definition.Risk) || old.PolicyHash != policyHash
	boundary = boundary || d.Intent != old.Definition.Intent || d.Target != old.Definition.Target || d.Environment != old.Definition.Environment || !equivalent(d.NonGoals, old.Definition.NonGoals) || !equivalent(d.Scope, old.Definition.Scope)
	beforeChecks, afterChecks := semantic(old.Definition), semantic(d)
	beforeChecks.Checks = nil
	afterChecks.Checks = nil
	checksOnly := equivalent(beforeChecks, afterChecks) && !hasWriter(old) && old.Candidate != "" && old.Step == "verify" && old.PolicyHash == policyHash
	for _, c := range old.Definition.Checks {
		if c.Required {
			found := false
			for _, newCheck := range d.Checks {
				if newCheck.ID == c.ID && newCheck.Required {
					found = true
				}
			}
			if !found {
				boundary = true
			}
		}
	}
	plan.Approved = !boundary && old.Plan.Approved
	var t Task
	e = s.Store.Write(func(tx *store.Tx) error {
		if err := checkPlanActive(tx, planParent, planKey); err != nil {
			return err
		}
		cur, e := load(tx, d.ID)
		if e != nil {
			return e
		}
		if cur.DefinitionHash != old.DefinitionHash {
			return Error("REVISION_CONFLICT", d.ID)
		}
		if expectedState != "" && taskStateHash(cur) != expectedState {
			return Error("PLAN_TASK_CHANGED", d.ID)
		}
		if cur.Status != "open" {
			return Error("TERMINAL_TASK", "task completed while its definition was being revised")
		}
		if refuseWriter && hasWriter(cur) {
			return Error("PLAN_TASK_BUSY", d.ID)
		}
		if expectedPolicy != "" {
			current, err := s.policyHash()
			if err != nil || current != expectedPolicy {
				return Error("PLAN_STALE_POLICY", "project policy changed during application")
			}
		}
		if e = dependencies(tx, d.ID, d.Dependencies); e != nil {
			return e
		}
		t = cur
		t.PolicyHash = policyHash
		t.Definition = d
		t.TimeLimitSeconds = nil
		t.Plan = plan
		t.EvidenceHistory = append(t.EvidenceHistory, t.Evidence...)
		t.Evidence = nil
		t.Candidate = ""
		t.Step = "define"
		for i := range t.Attempts {
			if t.Attempts[i].Status == "running" {
				t.Attempts[i].Status = "interrupted"
				t.Attempts[i].Reason = "definition changed; resume requires stopped-process acknowledgement"
			}
		}
		t.Paused = hasWriter(old)
		t.Failure = "definition revised; relevant inputs and acceptance require revalidation"
		if cycle, ok := cur.FailureCycles[cur.ActiveFailure]; ok && cycle.State != "resolved" && cur.ActiveFailure != "" {
			// A presentation or unrelated definition edit cannot release a
			// repeated-failure stop or erase the retained review record.
			t.Paused, t.Failure = true, cur.Failure
		}
		t.KnowledgeOutcome = nil
		t.ContextAttempt = ""
		t.Route = ""
		if boundary {
			t.Confirmation = &Confirmation{"revision", "acceptance, authorization or policy boundary changed; review exact revision and definition diff", d.Revision, plan.Revision, ""}
		} else {
			t.Confirmation = nil
			t.Step = "execute"
			if slices.Contains(plan.Steps, "analysis") {
				t.Step = "analysis"
			}
		}
		if checksOnly && !boundary {
			workspace := old.CandidateWorkspace
			if workspace == "" {
				workspace = s.Root
			}
			fp, err := Fingerprint(workspace)
			if err != nil {
				return err
			}
			if fp == old.Candidate {
				t.Candidate = old.Candidate
				t.CandidateWorkspace = workspace
				t.Step = "verify"
				for _, c := range d.Checks {
					for _, prior := range old.Definition.Checks {
						if equivalent(c, prior) {
							if ev := CurrentEvidence(&old, prior); ev != nil {
								reused := *ev
								reused.Revision = d.Revision
								reused.PlanRevision = plan.Revision
								reused.ReusedFrom = fmt.Sprintf("definition:%d/plan:%d", ev.Revision, ev.PlanRevision)
								t.Evidence = append(t.Evidence, reused)
							}
						}
					}
				}
				t.KnowledgeOutcome = old.KnowledgeOutcome
			}
		}
		if hasUnresolvedFailures(&cur) && !boundary && cur.Step != "verify" && cur.Step != "delivery" && slices.Contains(t.Plan.Steps, cur.Step) {
			// An unrelated revision must not move a paused failure to a later unit.
			t.Step = cur.Step
		}
		b := definitionBytes(d)
		t.DefinitionHash = store.Hash(b)
		p, e := s.taskPath(d.ID)
		if e != nil {
			return e
		}
		if expectedState != "" {
			e = tx.FileExpected(p, b, old.DefinitionHash)
		} else {
			e = tx.File(p, b)
		}
		if e != nil {
			return e
		}
		if planKey != "" {
			e = s.savePlan(tx, &t)
		} else {
			e = s.save(tx, &t)
		}
		if e != nil {
			return e
		}
		return tx.Event(d.ID, "definition_revised", map[string]any{"revision": d.Revision, "source_hash": store.Hash(raw), "previous": old.Definition, "proposed": d, "checks_only": checksOnly, "approval_required": boundary, "impact": "changed input/coverage revalidated; unchanged checks reused only for the identical candidate"})
	})
	if e == nil {
		e = s.Store.Flush()
	}
	return t, e
}
func dependencies(tx *store.Tx, id string, deps []string) error {
	seen := map[string]bool{}
	var walk func(string) error
	walk = func(cur string) error {
		if cur == id {
			return Error("DEPENDENCY_CYCLE", id)
		}
		if seen[cur] {
			return nil
		}
		seen[cur] = true
		t, e := load(tx, cur)
		if e != nil {
			return Error("DEPENDENCY_MISSING", cur)
		}
		for _, d := range t.Definition.Dependencies {
			if e = walk(d); e != nil {
				return e
			}
		}
		return nil
	}
	for _, d := range deps {
		if e := walk(d); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) Edit(id string, d Definition, reason string) (Next, error) {
	if reason == "" {
		return Next{}, Error("REASON_REQUIRED", "edit")
	}
	old, e := s.Get(id)
	if e != nil {
		return Next{}, e
	}
	if old.Status == "completed" || old.Status == "cancelled" {
		return Next{}, Error("TERMINAL_TASK", "create linked feedback")
	}
	d.ID = id
	if _, e = s.syncDefinition(old, d, definitionBytes(d)); e != nil {
		return Next{}, e
	}
	return s.Next(id)
}
func (s *Service) List() ([]Next, error) {
	rows, e := s.Store.DB.Query("SELECT id FROM tasks ORDER BY CASE WHEN completed='' THEN 0 ELSE 1 END, completed DESC,updated DESC")
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	all := []Next{}
	for _, id := range ids {
		n, e := s.Next(id)
		if e != nil {
			return nil, e
		}
		all = append(all, n)
	}
	return all, nil
}
func (s *Service) Next(id string) (Next, error) {
	return s.next(id, true)
}

// next can omit artifact reads for list projections which only need task
// eligibility. Detail views still receive the original evidence availability.
func (s *Service) next(id string, includeArtifacts bool) (Next, error) {
	return s.nextWithConstraints(id, includeArtifacts, s.ResolveBasis, s.ConstraintCheck)
}

func (s *Service) nextWithConstraints(id string, includeArtifacts bool, resolveBasis func(Definition) ([]BasisReference, error), constraintCheck func(Definition) []string) (Next, error) {
	t, e := s.Get(id)
	if e != nil {
		return Next{}, e
	}
	// Remote evidence lives in the central artifact store, never in Root.
	// Its authorized artifact endpoint reports availability.
	if includeArtifacts && !s.RemoteAuthority {
		for i := range t.Evidence {
			ev := &t.Evidence[i]
			ev.ArtifactStatus = map[string]string{}
			workspace := t.CandidateWorkspace
			if workspace == "" {
				workspace = s.Root
			}
			for _, ref := range ev.Artifacts {
				if _, err := s.artifactAt(workspace, ref); err != nil {
					ev.ArtifactStatus[ref] = "摘要保留 / 原件已清理或缺失"
				} else {
					ev.ArtifactStatus[ref] = "原件可读取"
				}
			}
		}
	}
	n := t.BaseNext()
	if resolveBasis != nil && t.Status == "open" && (t.Step == "verify" || t.Step == "delivery") {
		refs, err := resolveBasis(t.Definition)
		if err == nil && !sameBasis(t.Basis, refs) {
			n.Action = "wait_environment"
			n.Reasons = append(n.Reasons, "applicable knowledge changed after execution; return to execute, load current basis and revalidate affected candidate")
		}
	}
	if constraintCheck != nil && t.Status == "open" {
		for _, reason := range constraintCheck(t.Definition) {
			n.Action = "wait_environment"
			if strings.Contains(reason, "unresolved knowledge conflict") {
				n.Action = "wait_confirmation"
			}
			n.Reasons = append(n.Reasons, reason)
		}
	}
	if !s.Active && t.Status == "open" {
		n.Action = "wait_environment"
		n.Reasons = append(n.Reasons, "project shadow mode")
	}
	if t.Status == "open" {
		for _, dep := range t.Definition.Dependencies {
			var b string
			if e = s.Store.DB.QueryRow("SELECT status FROM tasks WHERE id=?", dep).Scan(&b); e != nil || b != "completed" {
				n.Action = "wait_dependency"
				n.Reasons = append(n.Reasons, "unsatisfied dependency: "+dep)
			}
		}
	}
	if t.Definition.Type == "epic" && t.Status == "open" {
		if e = s.summarizeEpicNext(&n, &t); e != nil {
			return Next{}, e
		}
	}
	if n.Action == "continue" || n.Action == "repair" {
		count := 0
		for _, a := range t.Attempts {
			if a.Unit == t.Step {
				count++
			}
		}
		if timeBudgetExhausted(&t) {
			n.Action = "wait_environment"
			n.Reasons = append(n.Reasons, "budget exhausted; reviewed definition revision required")
		}
	}
	n.Task = &t
	health, e := s.workHealth(t, time.Now().UTC())
	if e != nil {
		return Next{}, e
	}
	n.Health = &health
	return n, nil
}
func (s *Service) mutate(id, kind string, fn func(*store.Tx, *Task) error) (Next, error) {
	if _, e := s.Get(id); e != nil {
		return Next{}, e
	}
	e := s.Store.Write(func(tx *store.Tx) error {
		t, e := load(tx, id)
		if e != nil {
			return e
		}
		if e = fn(tx, &t); e != nil {
			return e
		}
		if e = s.save(tx, &t); e != nil {
			return e
		}
		return tx.Event(id, kind, map[string]any{"step": t.Step, "revision": t.Definition.Revision, "failure": t.Failure})
	})
	if e != nil {
		return Next{}, e
	}
	if e = s.Store.Flush(); e != nil {
		return Next{}, e
	}
	return s.Next(id)
}
func (s *Service) Claim(id, unit, executor, workspace, session string) (Next, error) {
	if os.Getenv("AEL_MANAGED_CHILD") == "1" {
		return Next{}, Error("MANAGED_CHILD_FORBIDDEN", "managed child must return its current result to the parent runner")
	}
	return s.claim(id, unit, executor, workspace, session, nil)
}
func (s *Service) claim(id, unit, executor, workspace, session string, expected *ReviewExpectation) (Next, error) {
	return s.claimGuarded(id, unit, executor, workspace, session, expected, nil)
}

func (s *Service) claimGuarded(id, unit, executor, workspace, session string, expected *ReviewExpectation, guard func(*store.Tx, *Task) error) (Next, error) {
	if e := s.active(); e != nil {
		return Next{}, e
	}
	var generation string
	if err := s.Store.DB.QueryRow("SELECT body FROM metadata WHERE key='tool-generation'").Scan(&generation); err != nil && err != sql.ErrNoRows {
		return Next{}, err
	}
	n, e := s.Next(id)
	if e != nil {
		return n, e
	}
	if n.Action != "continue" && n.Action != "repair" && !(n.Step == "verify" && n.Action == "wait_executor") {
		return n, Error("NOT_RUNNABLE", strings.Join(n.Reasons, ";"))
	}
	if executor == "" {
		return Next{}, Error("EXECUTOR_REQUIRED", "")
	}
	if workspace == "" {
		workspace = s.Root
		if (n.Step == "verify" || n.Step == "delivery") && n.Task.CandidateWorkspace != "" {
			workspace = n.Task.CandidateWorkspace
		}
	}
	workspace, e = filepath.EvalSymlinks(workspace)
	if e != nil {
		return Next{}, e
	}
	workspace, e = filepath.Abs(workspace)
	if e != nil {
		return Next{}, e
	}
	if workspace != s.Root {
		var binding string
		if e = s.Store.DB.QueryRow("SELECT body FROM metadata WHERE key=?", "workspace:"+workspace).Scan(&binding); e != nil {
			return Next{}, Error("WORKSPACE_NOT_BOUND", "run workspace bind --path WORKTREE")
		}
	}
	inputSnapshot, e := captureCandidate(workspace)
	if e != nil {
		return Next{}, e
	}
	candidate := inputSnapshot.Fingerprint
	inputRepository := ""
	inputRepositoryUnknown := false
	if n.Step == "execute" {
		inputRepository, e = localGitRepository(workspace, inputSnapshot.Head)
		if e != nil {
			inputRepositoryUnknown = true
		}
	}
	leaseDuration, e := s.leaseDuration()
	if e != nil {
		return Next{}, e
	}
	var a Attempt
	n, e = s.mutate(id, "claimed", func(tx *store.Tx, t *Task) error {
		if guard != nil {
			if e := guard(tx, t); e != nil {
				return e
			}
		} else {
			if e := protectAssignedWork(tx, t, executor); e != nil {
				return e
			}
			if e := assignLegacyWork(tx, t, executor, workspace, s.Root, unit); e != nil {
				return e
			}
		}
		var current string
		err := tx.Row("SELECT body FROM metadata WHERE key='tool-generation'").Scan(&current)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		var maintenance int
		if e := tx.Row("SELECT count(*) FROM metadata WHERE key='tool-update'").Scan(&maintenance); e != nil {
			return e
		}
		if maintenance > 0 {
			return Error("PROJECT_UPDATING", "tool maintenance active")
		}
		if current != generation {
			return Error("PROJECT_CHANGED", "toolchain changed while preparing this claim; reload and retry")
		}

		if t.Step == "execute" && t.Plan.Preset == "strict" && !t.Plan.Approved {
			return Error("PLAN_APPROVAL_REQUIRED", "strict plan must be approved before execution")
		}
		if t.Status != "open" || t.Paused || t.Confirmation != nil {
			return Error("NOT_RUNNABLE", id)
		}
		for _, dep := range t.Definition.Dependencies {
			x, e := load(tx, dep)
			if e != nil || x.Status != "completed" {
				return Error("DEPENDENCY_BLOCKED", dep)
			}
		}
		if unit == "" {
			unit = t.Step
		}
		if t.Step == "verify" {
			found := false
			for _, c := range t.Definition.Checks {
				if c.ID == unit {
					found = true
					if expected != nil {
						if expected.Candidate != t.Candidate || expected.DefinitionRevision != t.Definition.Revision || expected.PlanRevision != t.Plan.Revision {
							return Error("REVIEW_VERSION_MISMATCH", "review the current candidate and revisions")
						}
						if access := ReviewAccess(Next{Task: t, Action: "continue"}, c); !access.CanReview {
							return Error("REVIEW_NOT_ALLOWED", access.Reason)
						}
					}
				}
			}
			if !found {
				return Error("CHECK_REQUIRED", "claim a specific check id")
			}
			if t.Definition.Type == "epic" {
				preparedWorkspace, err := filepath.EvalSymlinks(t.CandidateWorkspace)
				if err != nil || filepath.Clean(preparedWorkspace) != filepath.Clean(workspace) {
					return Error("EPIC_CHECK_WORKSPACE_MISMATCH", "run the parent check in its prepared candidate workspace")
				}
			}
			if candidate != t.Candidate {
				return Error("CANDIDATE_CHANGED", "return to execute with resume --return-to execute")
			}
		} else if unit != t.Step {
			return Error("WRONG_UNIT", unit)
		}
		if t.Definition.Intent == "inspect" {
			if t.InspectionCandidate == "" {
				t.InspectionCandidate = candidate
			}
			if candidate != t.InspectionCandidate {
				return Error("INSPECTION_WRITE_DETECTED", "restore the original inspected candidate; retry cannot authorize a changed source baseline")
			}
		}
		claimUnit := unit
		if t.Step == "verify" {
			claimUnit = "check:" + unit
		}
		for _, cycle := range t.FailureCycles {
			if cycle.Unit == claimUnit && (cycle.State == "review_required" || cycle.State == "blocked") {
				return Error("ROOT_CAUSE_REVIEW_REQUIRED", "the requested unit still has an unreviewed failure")
			}
		}
		attempts := 0
		for _, old := range t.Attempts {
			if old.Unit == claimUnit {
				attempts++
			}
			if old.Status == "running" && (old.Unit == claimUnit || t.Step != "verify") {
				return Error("CLAIM_CONFLICT", "attempt active or expired; resume explicitly")
			}
		}
		if timeBudgetExhausted(t) {
			return Error("BUDGET_EXHAUSTED", "increase budget through a reviewed definition revision")
		}
		// Distinct tasks may use the same workspace concurrently. The attempt
		// guard above is task-scoped; candidate checks still invalidate evidence
		// when any process changes the workspace under verification.
		now := time.Now().UTC()
		a = Attempt{DefinitionRevision: t.Definition.Revision, PlanRevision: t.Plan.Revision, ID: ID("AT-"), Unit: claimUnit, Executor: executor, Session: session, Workspace: workspace, Input: candidate, Started: now, LastContact: &now, Lease: now.Add(leaseDuration), Token: ID("lease-"), Fence: len(t.Attempts) + 1, Status: "running"}
		if t.Definition.Type == "epic" && t.Step == "verify" {
			if a.WorkspaceConflict, e = epicWorkspaceWait(tx, t); e != nil {
				return e
			}
			for i := range t.Attempts {
				other := &t.Attempts[i]
				if other.Status == "running" && strings.HasPrefix(other.Unit, "check:") && overlappingWorkspaces(other.Workspace, workspace) {
					other.WorkspaceConflict = fmt.Sprintf("parent check %s overlapped this check in the same workspace", a.ID)
					a.WorkspaceConflict = fmt.Sprintf("parent check %s overlapped this check in the same workspace", other.ID)
				}
			}
		}
		if e = s.markOverlappingEpicChecks(tx, t.Definition.ID, a.ID, workspace); e != nil {
			return e
		}
		if t.Step == "execute" && t.ExecutionBase == nil {
			unknown := false
			for _, old := range t.Attempts {
				unknown = unknown || old.Unit == "execute"
			}
			if t.History != nil {
				for _, old := range t.History.Attempts {
					unknown = unknown || old.Unit == "execute"
				}
			}
			t.ExecutionBase = &CandidateBaseline{Fingerprint: candidate, Head: inputSnapshot.Head, LocalRepository: inputRepository, Files: inputSnapshot.Files, Exclusions: inputSnapshot.Exclusions, PolicyExclusions: inputSnapshot.PolicyExclusions, Unknown: unknown || inputRepositoryUnknown}
		}
		t.Attempts = append(t.Attempts, a)
		return nil
	})
	if e == nil {
		n.Attempt = &a
	}
	return n, e
}
func findAttempt(t *Task, id, token string) (*Attempt, error) {
	for i := range t.Attempts {
		a := &t.Attempts[i]
		if a.ID == id {
			if a.Token != token || a.Status != "running" || !a.Lease.After(time.Now()) {
				return nil, Error("STALE_TOKEN", id)
			}
			return a, nil
		}
	}
	return nil, Error("ATTEMPT_MISSING", id)
}
func (s *Service) Heartbeat(id, attempt, token string) (Next, error) {
	duration, e := s.leaseDuration()
	if e != nil {
		return Next{}, e
	}
	e = s.Store.Write(func(tx *store.Tx) error {
		t, e := load(tx, id)
		if e != nil {
			return e
		}
		a, e := findAttempt(&t, attempt, token)
		if e != nil {
			return e
		}
		now := time.Now().UTC()
		a.LastContact = &now
		a.Lease = now.Add(duration)
		return save(tx, &t)
	})
	if e != nil {
		return Next{}, e
	}
	return s.Next(id)
}

func (s *Service) Submit(sub Submission) (Next, error) {
	if os.Getenv("AEL_MANAGED_CHILD") == "1" {
		return Next{}, Error("MANAGED_CHILD_FORBIDDEN", "managed child must return its current result to the parent runner")
	}
	return s.submit(sub, nil)
}
func (s *Service) submit(sub Submission, evidence *Evidence) (Next, error) {
	return s.submitWithHash(sub, evidence, "")
}
func (s *Service) submitWithHash(sub Submission, evidence *Evidence, requestHash string) (Next, error) {
	return s.submitWithEnvironment(sub, evidence, requestHash, nil)
}

func (s *Service) submitWithEnvironment(sub Submission, evidence *Evidence, requestHash string, remote *RemoteSubmitEnvironment) (Next, error) {
	if sub.Key == "" {
		return Next{}, Error("IDEMPOTENCY_KEY_REQUIRED", "")
	}
	b, _ := json.Marshal(sub)
	hash := store.Hash(b)
	if requestHash != "" {
		hash = requestHash
	}
	var response Next
	if _, e := s.Get(sub.Task); e != nil {
		return Next{}, e
	}
	e := s.Store.Write(func(tx *store.Tx) error {
		var h, r string
		err := tx.Row("SELECT hash,response FROM submissions WHERE key=?", sub.Key).Scan(&h, &r)
		if err == nil {
			if h != hash {
				return Error("IDEMPOTENCY_CONFLICT", sub.Key)
			}
			return json.Unmarshal([]byte(r), &response)
		}
		if err != sql.ErrNoRows {
			return err
		}
		t, e := load(tx, sub.Task)
		if e != nil {
			return e
		}
		a, e := findAttempt(&t, sub.Attempt, sub.Token)
		if e != nil {
			return e
		}
		readArtifact := func(ref string) ([]byte, error) { return s.artifactAt(a.Workspace, ref) }
		if remote != nil {
			if a.Fence != remote.Fence || !strings.HasPrefix(a.Workspace, "remote:") {
				return Error("STALE_TOKEN", "remote fence or workspace changed")
			}
			readArtifact = func(ref string) ([]byte, error) { return remote.ReadArtifact(tx, ref) }
			evidence, e = remote.Prepare(tx, &t, a)
			if e != nil {
				return e
			}
		}
		if t.Status != "open" {
			return Error("TERMINAL_TASK", sub.Task)
		}
		if (t.Step == "verify" && !strings.HasPrefix(a.Unit, "check:")) || (t.Step != "verify" && a.Unit != t.Step) || (a.DefinitionRevision > 0 && a.DefinitionRevision != t.Definition.Revision) || (a.PlanRevision > 0 && a.PlanRevision != t.Plan.Revision) {
			return Error("STALE_EXECUTION_UNIT", "attempt is not for the current responsibility and revisions")
		}

		if sub.Result.Summary == "" {
			return Error("RESULT_REQUIRED", "summary")
		}
		if !slices.Contains([]string{"pass", "fail", "blocked"}, sub.Result.Status) {
			return Error("INVALID_RESULT", sub.Result.Status)
		}
		if len(sub.Result.FailureClass) > 128 || strings.ContainsAny(sub.Result.FailureClass, "\r\n") {
			return Error("INVALID_FAILURE_CLASS", "failure_class must be a short single-line identifier")
		}
		if t.Step == "verify" && evidence == nil {
			return Error("TRUSTED_CHECK_REQUIRED", "use verify for commands; confirm --kind review for manual checks")
		}
		now := time.Now().UTC()
		a.Status = sub.Result.Status
		a.Ended = &now
		a.Reason = sub.Result.Summary
		a.Outputs = sub.Result.Artifacts
		var outputSnapshot candidateSnapshot
		if remote != nil {
			outputSnapshot = candidateSnapshot{Fingerprint: remote.CandidateID, Head: remote.Head, Files: remote.Files}
		} else {
			outputSnapshot, e = captureCandidate(a.Workspace)
			if e != nil {
				return e
			}
		}
		candidate := outputSnapshot.Fingerprint
		if a.Unit == "execute" && t.ExecutionBase != nil {
			t.ExecutionBase.Exclusions = uniqueSorted(t.ExecutionBase.Exclusions, outputSnapshot.Exclusions)
			for _, ref := range sub.Result.Artifacts {
				if strings.HasPrefix(ref, "runtime:") {
					continue
				}
				path := filepath.ToSlash(filepath.Clean(ref))
				if _, ok := outputSnapshot.Files[path]; !ok {
					t.ExecutionBase.UnboundArtifacts = uniqueSorted(t.ExecutionBase.UnboundArtifacts, []string{path})
				}
			}
		}
		if t.Step == "verify" && evidence != nil && strings.HasPrefix(evidence.Source, "human:") {
			if t.Definition.Type == "epic" && a.WorkspaceConflict != "" {
				return Error("REVIEW_VERSION_MISMATCH", "workspace activity overlapped the review; review a stable candidate")
			}
			var check Check
			for _, c := range t.Definition.Checks {
				if c.ID == evidence.Check {
					check = c
				}
			}
			config, _ := json.Marshal(check)
			if check.ID == "" || EffectiveCheckMethod(check) != "human" || len(check.Command) != 0 || a.Unit != "check:"+check.ID {
				return Error("CHECK_METHOD_MISMATCH", evidence.Check)
			}
			if evidence.Candidate != t.Candidate || evidence.Revision != t.Definition.Revision || evidence.PlanRevision != t.Plan.Revision || evidence.Config != store.Hash(config) {
				return Error("REVIEW_VERSION_MISMATCH", "candidate, revisions or check configuration changed")
			}
			if candidate != t.Candidate || a.Input != t.Candidate {
				return Error("CANDIDATE_CHANGED", "human review candidate changed")
			}
			for ref, hash := range evidence.ArtifactHashes {
				data, err := readArtifact(ref)
				if err != nil || store.Hash(data) != hash {
					return Error("REVIEW_EVIDENCE_CHANGED", ref)
				}
			}
			if evidence.ReviewNoteID != "" {
				var note *HumanReviewNote
				for i := range t.HumanReviewNotes {
					if t.HumanReviewNotes[i].ID == evidence.ReviewNoteID {
						note = &t.HumanReviewNotes[i]
						break
					}
				}
				if note == nil {
					return Error("REVIEW_NOTE_MISSING", evidence.ReviewNoteID)
				}
				if note.Status != "pending" {
					return Error("REVIEW_NOTE_ALREADY_CONFIRMED", evidence.ReviewNoteID)
				}
				if note.Check != "" && note.Check != evidence.Check {
					return Error("REVIEW_NOTE_CHECK_MISMATCH", evidence.Check)
				}
				if note.DefinitionRevision != t.Definition.Revision || note.PlanRevision != t.Plan.Revision {
					return Error("REVIEW_NOTE_VERSION_MISMATCH", "record a new opinion for the current definition and plan")
				}
				for ref, hash := range note.ArtifactHashes {
					data, err := readArtifact(ref)
					if err != nil || store.Hash(data) != hash {
						return Error("REVIEW_EVIDENCE_CHANGED", ref)
					}
				}
				note.Status, note.ConfirmedCandidate, note.ConfirmedAt, note.ConfirmedBy = "confirmed", t.Candidate, &now, evidence.Executor
			}
		}
		a.Candidate = candidate
		priorEvidence := append(append([]Evidence{}, t.EvidenceHistory...), t.Evidence...)
		progressEvidence := evidence
		var failureHashes map[string]bool
		if sub.Result.Status == "fail" {
			var diagnostic map[string]bool
			if remote != nil {
				diagnostic = remote.DiagnosticArtifacts
			}
			failureHashes = failureEvidenceWith(sub.Result, evidence, readArtifact, diagnostic)
		}
		if sub.Result.Status == "blocked" && a.PID > 0 {
			a.Status = "interrupted"
		} else {
			// Release a legacy lock only for this attempt. New claims no longer
			// create workspace-wide locks, but upgraded projects can contain them.
			if _, e = tx.Exec("DELETE FROM workspace_locks WHERE attempt=?", a.ID); e != nil {
				return e
			}
		}
		staleEpicCheck := t.Definition.Type == "epic" && t.Step == "verify" && evidence != nil && !strings.HasPrefix(evidence.Source, "human:") && (candidate != t.Candidate || a.Input != t.Candidate || evidence.Candidate != t.Candidate || a.WorkspaceConflict != "")
		if t.Definition.Intent == "inspect" && !staleEpicCheck && (candidate != a.Input || (t.InspectionCandidate != "" && candidate != t.InspectionCandidate)) {
			return Error("INSPECTION_WRITE_DETECTED", "inspection may write reports only into runtime; source candidate must remain unchanged")
		}
		if sub.Result.Status == "pass" && t.Step == "execute" {
			if remote != nil {
				e = validateContextWith(&t, a, sub.Result, remote.Basis, readArtifact)
			} else {
				e = s.validateContext(&t, a, sub.Result)
			}
			if e != nil {
				return e
			}
		}
		if staleEpicCheck {
			if evidence.Candidate != a.Input {
				return Error("CANDIDATE_CHANGED", "check evidence does not match the claimed input")
			}
			// Files read after a change cannot be presented as hashes of the
			// claimed candidate. Keep the conclusion and runtime report as
			// historical facts, explicitly marking workspace references.
			if evidence.ArtifactStatus == nil {
				evidence.ArtifactStatus = map[string]string{}
			}
			if a.WorkspaceConflict != "" {
				evidence.Recovery = a.WorkspaceConflict + "; recheck after the workspace is stable"
			}
			for _, ref := range append(slices.Clone(evidence.Inputs), evidence.Artifacts...) {
				if !strings.HasPrefix(ref, "runtime:") {
					if evidence.ArtifactStatus[ref] == "" {
						evidence.ArtifactStatus[ref] = "unverified_after_candidate_change"
					}
					delete(evidence.ArtifactHashes, ref)
				}
			}
			t.EvidenceHistory = append(t.EvidenceHistory, t.Evidence...)
			t.EvidenceHistory = append(t.EvidenceHistory, *evidence)
			t.Evidence = nil
			if t.Integration != nil {
				t.Integration.StaleReason = "parent candidate changed or another workspace attempt overlapped the check; prepare integration again"
				t.Integration.WaitReason = ""
			}
			t.Failure = "父级集成候选在检查期间变化；保留历史结果并重新准备"
			if sub.Result.Status == "blocked" {
				t.Paused = true
				t.Blocker = sub.Result.Blocker
				if t.Blocker == nil {
					t.Blocker = &Blocker{Category: "environment", Reason: sub.Result.Summary, Scope: t.Definition.Scope, Tried: sub.Result.Summary, Recovery: "restore the missing capability and prepare a stable candidate again", ResumeAt: t.Step, Unit: a.Unit, Input: a.Input, Next: "解除阻塞后重新准备父级候选并复验"}
				}
			}
		} else if sub.Result.Status == "blocked" {
			t.Blocker = sub.Result.Blocker
			if t.Blocker == nil {
				t.Blocker = &Blocker{Category: "environment", Reason: sub.Result.Summary, Scope: t.Definition.Scope, Tried: sub.Result.Summary, Recovery: "restore the missing capability or dependency; verify any old writer stopped then resume", ResumeAt: t.Step}
			}
			if t.Blocker.Reason == "" {
				t.Blocker.Reason = sub.Result.Summary
			}
			if t.Blocker.Tried == "" {
				t.Blocker.Tried = "未记录，请补充已尝试事项"
			}
			if t.Blocker.Recovery == "" {
				t.Blocker.Recovery = "核实阻塞原因和旧进程状态后恢复"
			}
			if t.Blocker.ResumeAt == "" {
				t.Blocker.ResumeAt = t.Step
			}
			t.Blocker.Unit = a.Unit
			t.Blocker.Input = a.Input
			if t.Blocker.Next == "" {
				t.Blocker.Next = "resolve the retained blocker and resume the required unit"
			}
			t.Paused = true
			t.Failure = sub.Result.Summary
		} else if sub.Result.Status == "fail" {
			t.Failure = sub.Result.Summary
			if t.Definition.Type == "epic" && t.Step == "verify" {
				if evidence.Candidate != t.Candidate || candidate != t.Candidate {
					return Error("CANDIDATE_CHANGED", "integration check input changed")
				}
				t.Evidence = append(t.Evidence, *evidence)
				evidence = nil
				if (t.Definition.Goal == "report" || (t.Definition.Goal == "analysis" && t.Definition.Intent == "inspect")) && ChecksComplete(&t) && !failureWouldStop(&t, a, sub.Result, failureHashes) {
					t.Route = ""
					if e = s.advance(tx, &t); e != nil {
						return e
					}
				} else {
					t.Route = "repair"
				}
			} else if (t.Definition.Goal == "report" || (t.Definition.Goal == "analysis" && t.Definition.Intent == "inspect")) && t.Step == "verify" {
				if evidence.Candidate != t.Candidate || candidate != t.Candidate {
					return Error("CANDIDATE_CHANGED", "report check input changed")
				}
				t.Evidence = append(t.Evidence, *evidence)
				evidence = nil
				if ChecksComplete(&t) && !failureWouldStop(&t, a, sub.Result, failureHashes) {
					if e = s.advance(tx, &t); e != nil {
						return e
					}
				}
			} else if sub.Result.ReturnTo == "define" || sub.Result.ReturnTo == "analysis" {
				t.Step = sub.Result.ReturnTo
				t.Plan.Revision++
				t.Plan.Approved = false
				t.Candidate = ""
				t.EvidenceHistory = append(t.EvidenceHistory, t.Evidence...)
				t.Evidence = nil
				if !slices.Contains(t.Plan.Steps, t.Step) {
					t.Step = "define"
				}
				t.Failure = "premise conflict: " + sub.Result.Summary
				for i := range t.Attempts {
					if t.Attempts[i].Status == "running" {
						t.Attempts[i].Status = "interrupted"
						t.Attempts[i].Reason = "peer changed responsibility or plan version"
						t.Paused = true
						t.Blocker = &Blocker{Category: "recovery", Reason: t.Attempts[i].Reason, Scope: t.Definition.Scope, Tried: sub.Result.Summary, Recovery: "verify all prior check processes stopped, then resume the new analysis/definition responsibility", ResumeAt: t.Step, Unit: t.Attempts[i].Unit, Input: t.Attempts[i].Input, Next: "核实旧检查停止后继续新的责任节点"}
					}
				}
			} else if t.Step == "verify" || sub.Result.ReturnTo == "execute" {
				t.Route = "repair"
				t.Step = "execute"
				t.EvidenceHistory = append(t.EvidenceHistory, t.Evidence...)
				t.Evidence = nil
				t.Candidate = ""
				for i := range t.Attempts {
					if t.Attempts[i].Status == "running" {
						t.Attempts[i].Status = "interrupted"
						t.Attempts[i].Reason = "peer check failed"
						t.Paused = true
						t.Failure = "peer check failed; acknowledge all old processes stopped before repair"
					}
				}
			}
		} else {
			switch t.Step {
			case "verify":
				if evidence.Candidate != t.Candidate || candidate != t.Candidate {
					return Error("CANDIDATE_CHANGED", "check result rejected")
				}
				t.Evidence = append(t.Evidence, *evidence)
				if gates(&t) || ((t.Definition.Goal == "report" || (t.Definition.Goal == "analysis" && t.Definition.Intent == "inspect")) && ChecksComplete(&t)) {
					t.Route = ""
					if e = s.advance(tx, &t); e != nil {
						return e
					}
				}
			case "execute":
				if len(sub.Result.Artifacts) == 0 {
					return Error("ARTIFACT_REQUIRED", "execute result must reference actual deliverable")
				}
				for _, p := range sub.Result.Artifacts {
					if _, e = readArtifact(p); e != nil {
						return e
					}
				}
				t.Route = ""
				t.Blocker = nil
				t.Failure = ""
				t.Candidate = candidate
				t.CandidateWorkspace = a.Workspace
				t.CandidateProof = nil
				outputRepository, repoErr := "", error(nil)
				if remote == nil {
					outputRepository, repoErr = localGitRepository(a.Workspace, outputSnapshot.Head)
				}
				if base := t.ExecutionBase; remote == nil && base != nil && !base.Unknown && repoErr == nil && base.LocalRepository == outputRepository && slices.Equal(base.PolicyExclusions, outputSnapshot.PolicyExclusions) {
					t.CandidateProof = &CandidateProof{Input: base.Fingerprint, InputHead: base.Head, Candidate: candidate, Head: outputSnapshot.Head, LocalRepository: outputRepository,
						Changes: candidateChanges(base.Files, outputSnapshot.Files), Files: outputSnapshot.Files,
						Exclusions: uniqueSorted(base.Exclusions, outputSnapshot.Exclusions), UnboundArtifacts: slices.Clone(base.UnboundArtifacts)}
				}
				t.EvidenceHistory = append(t.EvidenceHistory, t.Evidence...)
				t.Evidence = nil
				t.Step = "verify"
			case "analysis":
				if t.Plan.Preset == "strict" && !t.Plan.Approved {
					t.Confirmation = &Confirmation{"plan", "approve strict plan before execution", t.Definition.Revision, t.Plan.Revision, candidate}
				}
				if e = s.advance(tx, &t); e != nil {
					return e
				}
			case "delivery":
				d := sub.Result.Delivery
				if d == nil || d.RunID == "" || d.Key == "" || d.Artifact == "" || d.Status != "success" || d.Candidate != t.Candidate || candidate != t.Candidate {
					return Error("DELIVERY_RECEIPT_REQUIRED", "exact candidate, run id, idempotency key and artifact")
				}
				if t.Definition.Goal == "released" && d.Environment != t.Definition.Environment {
					return Error("WRONG_ENVIRONMENT", d.Environment)
				}
				if t.Definition.Goal == "merged" && (d.Target == "" || d.Target != t.Definition.Target) {
					return Error("TARGET_REQUIRED", "merge target")
				}
				raw, e := readArtifact(d.Artifact)
				if e != nil {
					return e
				}
				var receipt Delivery
				if e = json.Unmarshal(raw, &receipt); e != nil {
					return Error("DELIVERY_RECEIPT_INVALID", d.Artifact)
				}
				if receipt.RunID != d.RunID || receipt.Candidate != d.Candidate || receipt.Environment != d.Environment || receipt.Status != "success" || receipt.Key != d.Key || receipt.Target != d.Target || receipt.Artifact != d.Artifact {
					return Error("DELIVERY_RECEIPT_MISMATCH", d.RunID)
				}
				t.Delivery = d
				if e = s.advance(tx, &t); e != nil {
					return e
				}
			default:
				if e = s.advance(tx, &t); e != nil {
					return e
				}
			}
		}
		if sub.Result.Status == "fail" && !staleEpicCheck {
			noteFailure(&t, a, sub.Result, failureHashes)
		}
		if evidence != nil && sub.Result.Status != "pass" && !staleEpicCheck {
			t.Evidence = append(t.Evidence, *evidence)
		}
		var diagnostic map[string]bool
		if remote != nil {
			diagnostic = remote.DiagnosticArtifacts
		}
		progressChanged := recordSubmittedProgressWith(&t, a, sub.Result, progressEvidence, priorEvidence, now, readArtifact, diagnostic)
		if sub.Result.Status == "pass" && !staleEpicCheck {
			noteSuccess(&t, a)
		}
		if remote != nil && !a.Lease.After(time.Now()) {
			return Error("STALE_TOKEN", "lease expired before result commit")
		}
		if e = s.save(tx, &t); e != nil {
			return e
		}
		if e = tx.Event(sub.Task, "submitted", map[string]any{"attempt": a.ID, "result": sub.Result, "evidence": evidence, "evidence_changed": progressChanged}); e != nil {
			return e
		}
		response = t.BaseNext()
		if t.Definition.Type == "epic" && t.Integration != nil && t.Integration.StaleReason != "" && t.Status == "open" {
			response.Action = "wait_integration"
			response.Reasons = append(response.Reasons, t.Integration.StaleReason)
		} else if t.Definition.Type == "epic" && t.Integration != nil && t.Integration.WaitReason != "" && t.Status == "open" {
			response.Action = "wait_integration"
			response.Reasons = append(response.Reasons, t.Integration.WaitReason)
		}
		if sub.Result.Status == "fail" && t.Step == "execute" && !t.Paused {
			response.Action = "repair"
		}
		response.Task = &t
		b, _ := json.Marshal(response)
		_, e = tx.Exec("INSERT INTO submissions(key,hash,response) VALUES(?,?,?)", sub.Key, hash, string(b))
		return e
	})
	if e != nil {
		return Next{}, e
	}
	if e = s.Store.Flush(); e != nil {
		return Next{}, e
	}
	return response, nil
}
func gates(t *Task) bool {
	if len(t.Definition.Checks) == 0 {
		return false
	}
	coverage := map[string]bool{}
	for _, c := range t.Definition.Checks {
		if !c.Required {
			continue
		}
		if ev := CurrentEvidence(t, c); ev == nil || ev.Result != "pass" {
			return false
		}
		for _, a := range c.Covers {
			coverage[a] = true
		}
	}
	for _, a := range t.Definition.Acceptance {
		if !coverage[a] {
			return false
		}
	}
	return true
}
func (s *Service) advance(tx *store.Tx, t *Task) error {
	if pendingFailureKey(t) != "" {
		return nil
	}
	if t.Definition.Type == "epic" {
		if t.Paused {
			return nil
		}
		if t.Step != "verify" && t.Step != "delivery" {
			return Error("EPIC_INTEGRATION_REQUIRED", "prepare and verify the parent candidate")
		}
		if err := validateEpicIntegration(tx, t); err != nil {
			if !strings.HasPrefix(err.Error(), "EPIC_") && !strings.HasPrefix(err.Error(), "PLAN_APPLY_INCOMPLETE:") {
				return err
			}
			if t.Integration != nil {
				t.Integration.StaleReason = err.Error()
			}
			t.EvidenceHistory = append(t.EvidenceHistory, t.Evidence...)
			t.Evidence = nil
			t.Failure = "父级集成候选需重新核对：" + err.Error()
			if t.Step == "delivery" && t.Delivery != nil {
				t.Confirmation = &Confirmation{"delivery", "scope changed after delivery; reconcile the existing receipt before retry", t.Definition.Revision, t.Plan.Revision, t.Candidate}
			}
			return nil
		}
		// Never use an old passing result to finish while another check on
		// this parent is still running or awaiting stopped-process recovery.
		if hasWriter(*t) {
			return nil
		}
		wait, err := epicWorkspaceWait(tx, t)
		if err != nil {
			return err
		}
		if wait != "" {
			t.Integration.WaitReason = wait
			return nil
		}
		t.Integration.WaitReason = ""
		current, err := Fingerprint(t.CandidateWorkspace)
		if err != nil || current != t.Candidate {
			t.Integration.StaleReason = "parent workspace changed before completion; prepare integration again"
			t.EvidenceHistory = append(t.EvidenceHistory, t.Evidence...)
			t.Evidence = nil
			t.Failure = "父级集成候选已变化；保留历史结果并重新准备"
			if t.Step == "delivery" && t.Delivery != nil {
				t.Confirmation = &Confirmation{"delivery", "candidate changed after delivery; reconcile the existing receipt before retry", t.Definition.Revision, t.Plan.Revision, t.Candidate}
			}
			return nil
		}
	}
	for i, step := range t.Plan.Steps {
		if step == t.Step {
			if i+1 < len(t.Plan.Steps) {
				t.Step = t.Plan.Steps[i+1]
				if t.Step == "delivery" {
					t.Confirmation = &Confirmation{"delivery", "authorize exact candidate delivery", t.Definition.Revision, t.Plan.Revision, t.Candidate}
				}
				return nil
			}
			now := time.Now().UTC()
			t.Status = "completed"
			if t.ExecutionBase != nil {
				t.UnboundArtifacts = uniqueSorted(t.UnboundArtifacts, t.ExecutionBase.UnboundArtifacts)
			}
			t.ExecutionBase = nil
			t.Completed = &now
			t.Result = "verified"
			if t.Definition.Goal == "report" {
				t.Result = "report_delivered"
				if !gates(t) {
					t.Result = "report_delivered_with_findings"
				}
			}
			if t.Definition.Goal == "analysis" {
				t.Result = "analysis_delivered"
			}
			if t.Delivery != nil {
				t.Result = t.Definition.Goal
			}
			b := OutcomeDocument(*t)
			p, e := store.SafePath(s.Root, ".ael/outcomes/"+t.Definition.ID+".json")
			if e != nil {
				return e
			}
			return tx.File(p, b)
		}
	}
	return Error("INVALID_STEP", t.Step)
}
func (s *Service) artifact(ref string) ([]byte, error) { return s.artifactAt(s.Root, ref) }
func (s *Service) artifactAt(workspace, ref string) ([]byte, error) {
	root := workspace
	rel := ref
	if strings.HasPrefix(ref, "runtime:") {
		root = s.Store.Dir
		rel = strings.TrimPrefix(ref, "runtime:")
	}
	p, e := store.SafePath(root, rel)
	if e != nil {
		return nil, e
	}
	st, e := os.Stat(p)
	if e != nil || !st.Mode().IsRegular() {
		return nil, Error("ARTIFACT_MISSING", ref)
	}
	return os.ReadFile(p)
}
func (s *Service) Confirm(id, kind, decision, actor, reason, candidate string, revision, planRevision int) (Next, error) {
	return s.mutate(id, "confirmation", func(tx *store.Tx, t *Task) error {
		c := t.Confirmation
		if c == nil || c.Kind != kind || c.Revision != revision || c.PlanRevision != planRevision || c.Candidate != candidate {
			return Error("CONFIRMATION_VERSION_MISMATCH", id)
		}
		if actor == "" || reason == "" {
			return Error("CONFIRMATION_DETAIL_REQUIRED", "")
		}
		if !slices.Contains([]string{"approve", "adjust", "reject"}, decision) {
			return Error("INVALID_DECISION", decision)
		}
		// Rejecting or adjusting a stale epic delivery reconciliation records
		// what happened to the old run; it does not approve the new candidate.
		reconcileOldDelivery := t.Definition.Type == "epic" && c.Kind == "delivery" && decision != "approve" && t.Delivery != nil && t.Integration != nil && t.Integration.StaleReason != "" && c.Candidate == t.Delivery.Candidate
		if c.Candidate != "" && !reconcileOldDelivery {
			workspace := t.CandidateWorkspace
			if workspace == "" {
				workspace = s.Root
			}
			now, e := Fingerprint(workspace)
			if e != nil {
				return e
			}
			if now != c.Candidate {
				return Error("CANDIDATE_CHANGED", "revise before approval")
			}
		}
		t.Approvals = append(t.Approvals, Approval{kind, actor, decision, reason, revision, planRevision, candidate, time.Now().UTC()})
		t.Confirmation = nil
		if decision == "approve" {
			t.Plan.Approved = true
		} else {
			t.Plan.Approved = false
			t.Plan.Revision++
			t.EvidenceHistory = append(t.EvidenceHistory, t.Evidence...)
			t.Evidence = nil
			t.Step = "analysis"
			if !slices.Contains(t.Plan.Steps, "analysis") {
				t.Step = "define"
			}
			t.Failure = reason
		}
		return nil
	})
}
func (s *Service) Resume(id, returnTo string, ack bool) (Next, error) {
	// A previously completed set of checks may have been held solely for
	// failure review. Snapshot the same basis/constraint rules used by Next;
	// after old writers are verified stopped, the transaction rechecks the
	// candidate before advancing without unnecessary repeated checks.
	snapshot, err := s.Get(id)
	if err != nil {
		return Next{}, err
	}
	resumeEpicDelivery := snapshot.Definition.Type == "epic" && snapshot.Step == "delivery" && snapshot.Delivery != nil && snapshot.Integration != nil && snapshot.Integration.WaitReason != ""
	canAdvance := returnTo == "" && (snapshot.Step == "verify" || resumeEpicDelivery) && snapshot.Candidate != "" && s.Active
	if canAdvance {
		if policy, e := s.policyHash(); e != nil || policy != snapshot.PolicyHash {
			canAdvance = false
		}
		if s.ResolveBasis != nil {
			refs, e := s.ResolveBasis(snapshot.Definition)
			if e != nil || !sameBasis(snapshot.Basis, refs) {
				canAdvance = false
			}
		}
		if s.ConstraintCheck != nil && len(s.ConstraintCheck(snapshot.Definition)) > 0 {
			canAdvance = false
		}
	}
	return s.mutate(id, "resumed", func(tx *store.Tx, t *Task) error {
		terminalCleanup := t.Status == "completed" && returnTo == "" && hasWriter(*t)
		if t.Status != "open" && !terminalCleanup {
			return Error("TERMINAL_TASK", id)
		}
		if !terminalCleanup && pendingFailureKey(t) != "" {
			return Error("ROOT_CAUSE_REVIEW_REQUIRED", "record a bounded review before resuming the retained failed unit")
		}
		for i := range t.Attempts {
			a := &t.Attempts[i]
			if a.Status == "running" || a.Status == "interrupted" {
				if a.OwnerPID > 0 {
					identity, e := process.Identity(a.OwnerPID)
					if e == nil && identity == a.OwnerIdentity {
						return Error("OLD_OWNER_ALIVE", "stop the attached owner before recovering its workspace")
					}
					if e != nil && process.Alive(a.OwnerPID) {
						return Error("OWNER_IDENTITY_UNAVAILABLE", "cannot prove that the old owner stopped")
					}
				}
				if a.PID > 0 && (process.Alive(a.PID) || process.GroupAlive(a.PID)) {
					return Error("OLD_PROCESS_ALIVE", "stop PID before recovering workspace")
				}
				if !ack {
					return Error("PROCESS_CONFIRMATION_REQUIRED", "verify old process has stopped; use --ack-stopped")
				}
				a.Status = "abandoned"
				now := time.Now().UTC()
				a.Ended = &now
			}
		}
		if _, e := tx.Exec("DELETE FROM workspace_locks WHERE task=?", id); e != nil {
			return e
		}
		if terminalCleanup {
			// Older versions could close a task while a parallel check was
			// still active. End only the acknowledged stopped attempts; retain
			// its original outcome and completed time.
			return nil
		}
		if returnTo != "" {
			if slices.Index(t.Plan.Steps, returnTo) > slices.Index(t.Plan.Steps, t.Step) {
				return Error("FORWARD_RESUME_FORBIDDEN", "resume can only retain or return to earlier responsibility")
			}
			if !slices.Contains(t.Plan.Steps, returnTo) {
				return Error("INVALID_RETURN", returnTo)
			}
			t.Step = returnTo
			t.EvidenceHistory = append(t.EvidenceHistory, t.Evidence...)
			t.Evidence = nil
			t.Candidate = ""
		} else if t.Step == "verify" && t.Candidate != "" {
			workspace := t.CandidateWorkspace
			if workspace == "" {
				workspace = s.Root
			}
			if s.RemoteAuthority && strings.HasPrefix(workspace, "remote:") {
				return Error("REMOTE_CANDIDATE_REQUIRED", "remote workspace cannot be checked on the server; explicitly return to execute after verifying the old writer stopped")
			}
			current, e := Fingerprint(workspace)
			if e != nil {
				return Error("CANDIDATE_UNAVAILABLE", "cannot verify the retained input before recovery")
			}
			if current != t.Candidate {
				t.EvidenceHistory = append(t.EvidenceHistory, t.Evidence...)
				t.Evidence = nil
				t.Candidate = ""
				t.Route = "repair"
				t.Failure = "candidate changed during recovery; re-execute and revalidate"
				if slices.Contains(t.Plan.Steps, "execute") {
					t.Step = "execute"
				} else {
					t.Step = "define"
				}
			}
		}
		if t.Step == "delivery" && t.Delivery == nil {
			t.Confirmation = &Confirmation{"delivery", "unknown delivery outcome: reconcile existing run before retry", t.Definition.Revision, t.Plan.Revision, t.Candidate}
		}
		t.Paused = false
		t.Blocker = nil
		t.ActiveFailure = ""
		if canAdvance && t.DefinitionHash == snapshot.DefinitionHash && t.Plan.Revision == snapshot.Plan.Revision && t.Candidate == snapshot.Candidate && t.Step == snapshot.Step && t.Confirmation == nil {
			if policy, e := s.policyHash(); e != nil || policy != t.PolicyHash {
				canAdvance = false
			}
			for _, dep := range t.Definition.Dependencies {
				dependency, e := load(tx, dep)
				if e != nil || dependency.Status != "completed" {
					canAdvance = false
					break
				}
			}
			ready := t.Step == "verify" && (gates(t) || ((t.Definition.Goal == "report" || (t.Definition.Goal == "analysis" && t.Definition.Intent == "inspect")) && ChecksComplete(t)))
			if resumeEpicDelivery && t.Step == "delivery" && t.Integration != nil && t.Integration.WaitReason != "" && equivalent(t.Delivery, snapshot.Delivery) {
				ready = true
			}
			if canAdvance && ready {
				if e := s.advance(tx, t); e != nil {
					return e
				}
			}
		}
		return nil
	})
}
func (s *Service) Pause(id, reason string) (Next, error) {
	return s.mutate(id, "paused", func(tx *store.Tx, t *Task) error {
		if t.Status != "open" || reason == "" {
			return Error("INVALID_PAUSE", id)
		}
		t.Paused = true
		t.Failure = reason
		input := t.Candidate
		unit := t.Step
		if len(t.Attempts) > 0 {
			last := t.Attempts[len(t.Attempts)-1]
			if input == "" {
				input = last.Input
			}
			if last.Unit != "" {
				unit = last.Unit
			}
		}
		if input == "" {
			input = "未记录"
		}
		t.Blocker = &Blocker{Category: "manual", Reason: reason, Scope: t.Definition.Scope, Unit: unit, Input: input,
			Tried: "未记录，请补充已尝试事项", Recovery: "核实暂停原因并确认旧执行已停止", ResumeAt: t.Step,
			Next: "处理暂停原因后从保留的责任节点恢复"}
		return nil
	})
}
func (s *Service) Cancel(id, reason string) (Next, error) {
	return s.mutate(id, "cancelled", func(tx *store.Tx, t *Task) error {
		if t.Status != "open" || reason == "" {
			return Error("INVALID_CANCEL", id)
		}
		for i := range t.Attempts {
			a := &t.Attempts[i]
			if a.Status == "running" || a.Status == "interrupted" {
				if !s.RemoteAuthority || !strings.HasPrefix(a.Workspace, "remote:") {
					return Error("RUNNING_ATTEMPT", "stop and resume --ack-stopped first")
				}
				// Preserve the unknown host writer while revoking API acceptance.
				a.Status = "interrupted"
				a.Reason = "task cancelled; host execution state unknown"
			}
		}
		t.Status = "cancelled"
		t.Result = reason
		t.Confirmation = nil
		now := time.Now().UTC()
		t.Completed = &now
		b := OutcomeDocument(*t)
		p, e := store.SafePath(s.Root, ".ael/outcomes/"+id+".json")
		if e != nil {
			return e
		}
		return tx.File(p, b)
	})
}
func (s *Service) Events(id string) ([]map[string]any, error) {
	rows, e := s.Store.DB.Query("SELECT id,kind,body,created FROM events WHERE task_id=? ORDER BY id", id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	all := []map[string]any{}
	for rows.Next() {
		var seq int
		var kind, body, at string
		if e = rows.Scan(&seq, &kind, &body, &at); e != nil {
			return nil, e
		}
		var v any
		_ = json.Unmarshal([]byte(body), &v)
		all = append(all, map[string]any{"id": seq, "kind": kind, "body": v, "at": at})
	}
	return all, rows.Err()
}

var _ = fmt.Sprint

func (s *Service) RecordProcess(id, attempt, token string, pid int) error {
	_, e := s.mutate(id, "process_started", func(tx *store.Tx, t *Task) error {
		a, e := findAttempt(t, attempt, token)
		if e != nil {
			return e
		}
		a.PID = pid
		return nil
	})
	return e
}
func (s *Service) RecordSession(id, attempt, session string) error {
	_, e := s.mutate(id, "session_bound", func(tx *store.Tx, t *Task) error {
		for i := range t.Attempts {
			if t.Attempts[i].ID == attempt {
				t.Attempts[i].Session = session
				return nil
			}
		}
		return Error("ATTEMPT_MISSING", attempt)
	})
	return e
}

func runtimeUsed(t *Task) time.Duration {
	type interval struct{ start, end time.Time }
	var spans []interval
	for _, a := range t.Attempts {
		end := time.Now()
		if a.Ended != nil {
			end = *a.Ended
		}
		if !a.Lease.IsZero() && end.After(a.Lease) {
			end = a.Lease
		}
		if end.After(a.Started) {
			spans = append(spans, interval{a.Started, end})
		}
	}
	slices.SortFunc(spans, func(a, b interval) int { return a.start.Compare(b.start) })
	var sum time.Duration
	var last time.Time
	for _, span := range spans {
		start := span.start
		if start.Before(last) {
			start = last
		}
		if span.end.After(start) {
			sum += span.end.Sub(start)
		}
		if span.end.After(last) {
			last = span.end
		}
	}
	return sum
}
func timeBudgetExhausted(t *Task) bool {
	seconds := t.Definition.MaxSeconds
	if t.TimeLimitSeconds != nil {
		seconds = *t.TimeLimitSeconds
	}
	return seconds > 0 && runtimeUsed(t) > time.Duration(seconds)*time.Second
}

// A scheduling budget change does not alter acceptance criteria or approve gates.
func (s *Service) SetTimeLimit(id string, seconds int, reason string) (Next, error) {
	if e := s.active(); e != nil {
		return Next{}, e
	}
	if seconds < 0 || strings.TrimSpace(reason) == "" {
		return Next{}, Error("INVALID_BUDGET", "nonnegative max-seconds and a reason required; 0 means unlimited")
	}
	return s.mutate(id, "time_budget_changed", func(tx *store.Tx, t *Task) error {
		if t.Status != "open" {
			return Error("TERMINAL_TASK", id)
		}
		t.TimeLimitSeconds = &seconds
		return tx.Event(id, "time_budget_decision", map[string]any{"max_seconds": seconds, "reason": reason})
	})
}
func (s *Service) BindWorkspace(path string) (any, error) {
	p, e := filepath.Abs(path)
	if e != nil {
		return nil, e
	}
	p, e = filepath.EvalSymlinks(p)
	if e != nil {
		return nil, e
	}
	var primary, other struct {
		ID string `json:"project_id"`
	}
	b, e := os.ReadFile(filepath.Join(s.Root, ".ael/project.yaml"))
	if e != nil {
		return nil, e
	}
	if e = json.Unmarshal(b, &primary); e != nil {
		return nil, e
	}
	b, e = os.ReadFile(filepath.Join(p, ".ael/project.yaml"))
	if e != nil {
		return nil, e
	}
	if e = json.Unmarshal(b, &other); e != nil {
		return nil, e
	}
	if primary.ID == "" || other.ID != primary.ID {
		return nil, Error("WORKSPACE_PROJECT_MISMATCH", p)
	}
	_, e = s.Store.DB.Exec("INSERT INTO metadata(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", "workspace:"+p, primary.ID)
	return map[string]string{"workspace": p, "runtime_instance": s.Store.Dir}, e
}

func (s *Service) TaskArtifact(id, ref string) ([]byte, error) {
	t, e := s.Get(id)
	if e != nil {
		return nil, e
	}
	workspace := t.CandidateWorkspace
	if workspace == "" {
		workspace = s.Root
	}
	if workspace != s.Root {
		var registered string
		if e = s.Store.DB.QueryRow("SELECT body FROM metadata WHERE key=?", "workspace:"+workspace).Scan(&registered); e != nil {
			return nil, Error("WORKSPACE_NOT_BOUND", workspace)
		}
	}
	return s.artifactAt(workspace, ref)
}

func (s *Service) Timeline(limit, offset int) ([]map[string]any, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, e := s.Store.DB.Query("SELECT id,task_id,kind,body,created FROM events ORDER BY id DESC LIMIT ? OFFSET ?", limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var seq int
		var task, kind, body, at string
		if e = rows.Scan(&seq, &task, &kind, &body, &at); e != nil {
			return nil, e
		}
		var v any
		if e = json.Unmarshal([]byte(body), &v); e != nil {
			return nil, e
		}
		out = append(out, map[string]any{"id": seq, "task": task, "kind": kind, "body": v, "at": at})
	}
	return out, rows.Err()
}
