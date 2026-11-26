package core

import (
	"ael/internal/store"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path"
	"slices"
	"sort"
	"strings"
	"time"
)

// WorkRequest describes one local Runner; capability and resource names are
// explicit declarations, not inferred from titles or file extensions.
type WorkRequest struct {
	Parent       string   `json:"parent"`
	Runner       string   `json:"runner"`
	Capabilities []string `json:"capabilities"`
	Resources    []string `json:"resources"`
	Parallel     bool     `json:"parallel"`
	Workspace    string   `json:"workspace"`
	Session      string   `json:"session"`
}

type WorkCheckpoint struct {
	Parent       string    `json:"parent"`
	Runner       string    `json:"runner"`
	Task         string    `json:"task,omitempty"`
	Step         string    `json:"step,omitempty"`
	Unit         string    `json:"unit,omitempty"`
	Revision     int       `json:"definition_revision,omitempty"`
	PlanRevision int       `json:"plan_revision,omitempty"`
	At           time.Time `json:"at"`
}

type WorkResult struct {
	Action      string         `json:"action"`
	StopReason  string         `json:"stop_reason,omitempty"`
	Detail      string         `json:"detail,omitempty"`
	Checkpoint  WorkCheckpoint `json:"checkpoint"`
	Next        *Next          `json:"next,omitempty"`
	Blocker     *Blocker       `json:"blocker,omitempty"`
	stateHash   string
	handoffFrom string
}

type workAssignment struct {
	Parent    string   `json:"parent"`
	Runner    string   `json:"runner"`
	Task      string   `json:"task"`
	Resources []string `json:"resources"`
	Parallel  bool     `json:"parallel"`
	Workspace string   `json:"workspace,omitempty"`
}

func workAssignmentKey(runner string) string {
	return "runner-assignment:" + store.Hash([]byte(runner))
}
func workCheckpointKey(parent, runner string) string {
	return "runner-checkpoint:" + store.Hash([]byte(parent+"\n"+runner))
}

type orderedWork struct {
	task         Task
	order        int
	capabilities []string
}

func workStopped(req WorkRequest, action, reason string, t *Task, unit string) WorkResult {
	cp := WorkCheckpoint{Parent: req.Parent, Runner: req.Runner, Unit: unit, At: time.Now().UTC()}
	if t != nil {
		cp.Task, cp.Step = t.Definition.ID, t.Step
		cp.Revision, cp.PlanRevision = t.Definition.Revision, t.Plan.Revision
		if cp.Unit == "" {
			cp.Unit = t.Step
			if t.Blocker != nil && t.Blocker.Unit != "" {
				cp.Unit = t.Blocker.Unit
			}
		}
	}
	result := WorkResult{Action: action, StopReason: action, Detail: reason, Checkpoint: cp}
	if t != nil && t.Status == "open" && t.Blocker != nil {
		result.Blocker = t.Blocker
	}
	return result
}

func workResourcesConflict(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for _, left := range a {
		if strings.TrimSpace(left) == "" {
			return true
		}
		for _, right := range b {
			if strings.TrimSpace(right) == "" || left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/") {
				return true
			}
		}
	}
	return false
}
func workScopesConflict(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for _, left := range a {
		if path.IsAbs(left) || strings.ContainsAny(left, "*?[]\\") {
			return true
		}
		left = path.Clean(left)
		if left == "." || left == ".." || strings.HasPrefix(left, "../") {
			return true
		}
		for _, right := range b {
			if path.IsAbs(right) || strings.ContainsAny(right, "*?[]\\") {
				return true
			}
			right = path.Clean(right)
			if right == "." || right == ".." || strings.HasPrefix(right, "../") || left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/") {
				return true
			}
		}
	}
	return false
}

func workHasUnrecoveredAttempt(t Task) bool {
	for _, attempt := range t.Attempts {
		if attempt.Status == "interrupted" || (attempt.Status == "running" && !attempt.Lease.After(time.Now())) {
			return true
		}
	}
	return false
}

// A legacy direct claim cannot escape a Runner's active parent assignment.
func protectAssignedWork(tx *store.Tx, task *Task, executor string) error {
	rows, err := tx.Conn.QueryContext(context.Background(), "SELECT body FROM metadata WHERE key LIKE 'runner-assignment:%'")
	if err != nil {
		return err
	}
	assignments := []workAssignment{}
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			rows.Close()
			return err
		}
		var a workAssignment
		if err = json.Unmarshal([]byte(body), &a); err != nil {
			rows.Close()
			return err
		}
		assignments = append(assignments, a)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, a := range assignments {
		if a.Task == task.Definition.ID {
			continue
		}
		if a.Runner != executor && (task.EpicMembership == nil || a.Parent != task.EpicMembership.Parent) {
			continue
		}
		assigned, err := load(tx, a.Task)
		if err == sql.ErrNoRows || (err == nil && assigned.Status != "open") {
			continue
		}
		if err != nil {
			return err
		}
		return Error("RUNNER_WIP", "unfinished parent-scope assignment task "+a.Task+" (runner "+a.Runner+", parent "+a.Parent+") must be completed or released first")
	}
	if task.EpicMembership == nil {
		return nil
	}
	rows, err = tx.Conn.QueryContext(context.Background(), "SELECT body FROM tasks WHERE id!=?", task.Definition.ID)
	if err != nil {
		return err
	}
	others := []Task{}
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			rows.Close()
			return err
		}
		var other Task
		if err = json.Unmarshal([]byte(body), &other); err != nil {
			rows.Close()
			return err
		}
		others = append(others, other)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, other := range others {
		for _, attempt := range other.Attempts {
			if attempt.Status != "running" && attempt.Status != "interrupted" {
				continue
			}
			if attempt.Executor == executor || (other.EpicMembership != nil && other.EpicMembership.Parent == task.EpicMembership.Parent) {
				return Error("RUNNER_WIP", "unfinished attempt "+attempt.ID+" on task "+other.Definition.ID+" unit "+attempt.Unit+" ("+attempt.Status+") requires explicit parent-scope coordination")
			}
		}
	}
	return nil
}

// A legacy claim of an Epic leaf retains the same WIP ownership after its
// attempt is submitted, so a later direct claim cannot skip unfinished work.
func assignLegacyWork(tx *store.Tx, task *Task, executor, workspace, root, unit string) error {
	if task.EpicMembership == nil {
		return nil
	}
	rows, err := tx.Conn.QueryContext(context.Background(), "SELECT body FROM metadata WHERE key LIKE 'runner-assignment:%'")
	if err != nil {
		return err
	}
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			rows.Close()
			return err
		}
		var existing workAssignment
		if err = json.Unmarshal([]byte(body), &existing); err != nil {
			rows.Close()
			return err
		}
		if existing.Parent == task.EpicMembership.Parent && existing.Task == task.Definition.ID {
			rows.Close()
			return nil
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if workspace == root {
		workspace = ""
	}
	a := workAssignment{Parent: task.EpicMembership.Parent, Runner: executor, Task: task.Definition.ID, Workspace: workspace}
	body, err := json.Marshal(a)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", workAssignmentKey(executor), string(body)); err != nil {
		return err
	}
	if unit == "" {
		unit = task.Step
	}
	cp := WorkResult{Action: "claimed", StopReason: "in_progress", Checkpoint: WorkCheckpoint{Parent: a.Parent, Runner: executor, Task: a.Task, Step: task.Step, Unit: unit, Revision: task.Definition.Revision, PlanRevision: task.Plan.Revision, At: time.Now().UTC()}}
	checkpoint, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", workCheckpointKey(a.Parent, executor), string(checkpoint)); err != nil {
		return err
	}
	return tx.Event(a.Parent, "runner_assigned", a)
}

// chooseWorkTx is shared by querying and the claim guard. It reads all
// scheduling state from the same SQLite transaction as the eventual claim.
func (s *Service) chooseWorkTx(tx *store.Tx, req WorkRequest) (WorkResult, error) {
	root, err := load(tx, req.Parent)
	if err != nil {
		return WorkResult{}, Error("EPIC_MISSING", req.Parent)
	}
	if root.Definition.Type != "epic" {
		return WorkResult{}, Error("EPIC_REQUIRED", req.Parent)
	}
	if s.RemoteAuthority && root.Status == "completed" && !root.Historical {
		return workStopped(req, "completed", "parent acceptance is complete", &root, ""), nil
	}
	if req.Parallel && len(req.Resources) == 0 {
		return workStopped(req, "wait_wip", "parallel work requires explicit resource declarations", nil, ""), nil
	}
	if root.Status != "open" || root.Confirmation != nil || !s.Active {
		return workStopped(req, "wait_human", "parent scope requires review or is not active", nil, ""), nil
	}
	if root.Paused {
		return workStopped(req, "wait_environment", root.Failure, nil, ""), nil
	}
	if err = s.checkTaskFile(root); err != nil {
		return workStopped(req, "wait_human", err.Error(), nil, ""), nil
	}
	policy, err := s.policyHash()
	if err != nil || policy != root.PolicyHash {
		return workStopped(req, "wait_human", "parent policy needs reconciliation", nil, ""), nil
	}
	var activePlan string
	if err = tx.Row("SELECT body FROM metadata WHERE key=?", planActiveKey(req.Parent)).Scan(&activePlan); err != nil && err != sql.ErrNoRows {
		return WorkResult{}, err
	}
	if activePlan != "" {
		return workStopped(req, "wait_human", "decomposition plan is not fully applied", nil, ""), nil
	}
	if root.EpicPlan != nil && root.EpicPlan.Proposal.ParentRevision != root.Definition.Revision {
		return workStopped(req, "wait_human", "parent definition changed since the plan was applied", nil, ""), nil
	}
	order := map[string]int{}
	caps := map[string][]string{}
	if root.EpicPlan != nil {
		for _, leaf := range root.EpicPlan.Proposal.Leaves {
			id := root.EpicPlan.TaskIDs[leaf.Key]
			order[id], caps[id] = leaf.Order, leaf.Capabilities
		}
	}
	rows, err := tx.Conn.QueryContext(context.Background(), "SELECT body FROM tasks WHERE json_extract(body,'$.epic_membership.parent')=?", req.Parent)
	if err != nil {
		return WorkResult{}, err
	}
	members := []orderedWork{}
	byID := map[string]Task{}
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			rows.Close()
			return WorkResult{}, err
		}
		var task Task
		if err = json.Unmarshal([]byte(body), &task); err != nil {
			rows.Close()
			return WorkResult{}, err
		}
		index, ok := order[task.Definition.ID]
		if !ok {
			index = int(^uint(0) >> 1)
		}
		members = append(members, orderedWork{task: task, order: index, capabilities: caps[task.Definition.ID]})
		byID[task.Definition.ID] = task
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return WorkResult{}, err
	}
	rows.Close()
	if root.EpicPlan != nil {
		for _, leaf := range root.EpicPlan.Proposal.Leaves {
			id := root.EpicPlan.TaskIDs[leaf.Key]
			if id == "" {
				return workStopped(req, "wait_human", "applied plan has an unmapped leaf: "+leaf.Key, nil, ""), nil
			}
			if _, ok := byID[id]; !ok {
				return workStopped(req, "wait_human", "applied plan leaf is missing or unassociated: "+id, nil, ""), nil
			}
		}
	}
	sort.Slice(members, func(i, j int) bool {
		if members[i].order != members[j].order {
			return members[i].order < members[j].order
		}
		return members[i].task.Definition.ID < members[j].task.Definition.ID
	})
	// Manual links have no explicit plan order. Respect dependencies among
	// members while retaining a deterministic order for independent leaves.
	ordered := make([]orderedWork, 0, len(members))
	byMember := map[string]orderedWork{}
	for _, member := range members {
		byMember[member.task.Definition.ID] = member
	}
	visited := map[string]int{}
	var visit func(string) error
	visit = func(id string) error {
		if visited[id] == 1 {
			return Error("DEPENDENCY_CYCLE", id)
		}
		if visited[id] == 2 {
			return nil
		}
		visited[id] = 1
		member := byMember[id]
		for _, dep := range member.task.Definition.Dependencies {
			if _, ok := byMember[dep]; ok {
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		visited[id] = 2
		ordered = append(ordered, member)
		return nil
	}
	for _, member := range members {
		if err := visit(member.task.Definition.ID); err != nil {
			return workStopped(req, "wait_dependency", err.Error(), nil, ""), nil
		}
	}
	members = ordered
	assignments := []workAssignment{}
	rows, err = tx.Conn.QueryContext(context.Background(), "SELECT body FROM metadata WHERE key LIKE 'runner-assignment:%'")
	if err != nil {
		return WorkResult{}, err
	}
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			rows.Close()
			return WorkResult{}, err
		}
		var a workAssignment
		if err = json.Unmarshal([]byte(body), &a); err != nil {
			rows.Close()
			return WorkResult{}, err
		}
		assignments = append(assignments, a)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return WorkResult{}, err
	}
	rows.Close()
	var own *workAssignment
	others := map[string]workAssignment{}
	for i := range assignments {
		a := assignments[i]
		task, err := load(tx, a.Task)
		if err == sql.ErrNoRows || (err == nil && task.Status != "open") {
			continue
		}
		if err != nil {
			return WorkResult{}, err
		}
		if a.Runner == req.Runner && req.Runner != "" {
			copy := a
			own = &copy
			if a.Parent != req.Parent {
				return workStopped(req, "wait_wip", "runner already owns an unfinished leaf in another parent", &task, ""), nil
			}
		} else if a.Parent == req.Parent {
			others[a.Task] = a
		}
	}
	var selected *orderedWork
	if own != nil {
		left, right := slices.Clone(own.Resources), slices.Clone(req.Resources)
		slices.Sort(left)
		slices.Sort(right)
		if own.Parallel != req.Parallel || own.Workspace != req.Workspace || !slices.Equal(left, right) {
			return workStopped(req, "wait_wip", "runner declarations changed; release the existing assignment first", nil, ""), nil
		}
		for i := range members {
			if members[i].task.Definition.ID == own.Task {
				selected = &members[i]
				break
			}
		}
		if selected == nil {
			return workStopped(req, "wait_human", "runner assignment no longer belongs to this parent", nil, ""), nil
		}
	} else {
		for i := range members {
			t := &members[i].task
			if t.Status == "completed" && !t.Historical {
				continue
			}
			if t.Status == "cancelled" && !t.EpicMembership.Required {
				continue
			}
			if a, claimed := others[t.Definition.ID]; claimed && req.Parallel && a.Parallel && t.Route != "repair" && t.Failure == "" && !workHasUnrecoveredAttempt(*t) && !t.Paused && t.Confirmation == nil {
				continue
			}
			selected = &members[i]
			break
		}
	}
	if selected == nil {
		if len(others) > 0 {
			return workStopped(req, "wait_wip", "other runners still hold unfinished leaves", nil, ""), nil
		}
		required := 0
		for _, member := range members {
			if member.task.EpicMembership.Required {
				required++
			}
		}
		if required == 0 {
			return workStopped(req, "wait_children", "parent has no required leaf tasks", nil, ""), nil
		}
		if s.RemoteAuthority && root.Integration != nil && root.Step == "verify" {
			return s.remoteParentWorkTx(tx, req, &root)
		}
		return workStopped(req, "wait_integration", "no remaining executable leaf; parent integration is still required", nil, ""), nil
	}
	t := &selected.task
	if t.Historical {
		return workStopped(req, "wait_human", "historical leaf must be revalidated in this workspace", t, ""), nil
	}
	if t.Status == "cancelled" {
		return workStopped(req, "wait_human", "required leaf was cancelled and still belongs to the parent scope", t, ""), nil
	}
	for _, attempt := range t.Attempts {
		if attempt.Status == "interrupted" || (attempt.Status == "running" && !attempt.Lease.After(time.Now())) {
			return workStopped(req, "runner_offline", "previous executor must be verified stopped before recovery", t, ""), nil
		}
	}
	handoffFrom := ""
	if own == nil && strings.TrimSpace(req.Runner) != "" {
		if holder, ok := others[t.Definition.ID]; ok && !hasWriter(*t) {
			handoffFrom = holder.Runner
			delete(others, t.Definition.ID)
		}
	}
	if own == nil && len(others) > 0 {
		if !req.Parallel {
			return workStopped(req, "wait_wip", "another runner holds an unfinished leaf", t, ""), nil
		}
		for id, assignment := range others {
			other := byID[id]
			if !assignment.Parallel || workResourcesConflict(req.Resources, assignment.Resources) || workScopesConflict(t.Definition.Scope, other.Definition.Scope) {
				return workStopped(req, "wait_wip", "parallel work requires explicitly disjoint resources and scopes", t, ""), nil
			}
		}
	}
	for _, member := range members {
		for _, attempt := range member.task.Attempts {
			if attempt.Status != "running" {
				continue
			}
			if member.task.Definition.ID != t.Definition.ID {
				assignment, known := others[member.task.Definition.ID]
				if !req.Parallel || !known || !assignment.Parallel {
					return workStopped(req, "wait_wip", "an active attempt prevents safe parallel selection", &member.task, ""), nil
				}
			}
		}
	}
	if t.Confirmation != nil {
		return workStopped(req, "wait_human", t.Confirmation.Reason, t, ""), nil
	}
	for _, attempt := range t.Attempts {
		if attempt.Status == "running" {
			return workStopped(req, "wait_executor", "leaf already has an active attempt", t, ""), nil
		}
	}
	if t.Paused {
		return workStopped(req, "wait_environment", t.Failure, t, ""), nil
	}
	if timeBudgetExhausted(t) {
		return workStopped(req, "budget_stopped", "leaf execution budget is exhausted", t, ""), nil
	}
	for _, dep := range t.Definition.Dependencies {
		dependency, err := load(tx, dep)
		if err != nil || dependency.Status != "completed" || dependency.Historical {
			return workStopped(req, "wait_dependency", "unsatisfied or historical dependency: "+dep, t, ""), nil
		}
	}
	if err = s.checkTaskFile(*t); err != nil {
		return workStopped(req, "wait_human", err.Error(), t, ""), nil
	}
	if t.PolicyHash != policy {
		return workStopped(req, "wait_human", "leaf policy needs reconciliation", t, ""), nil
	}
	if s.ConstraintCheck != nil {
		if reasons := s.ConstraintCheck(t.Definition); len(reasons) > 0 {
			return workStopped(req, "wait_human", strings.Join(reasons, "; "), t, ""), nil
		}
	}
	if s.ResolveBasis != nil && (t.Step == "verify" || t.Step == "delivery") {
		refs, err := s.ResolveBasis(t.Definition)
		if err != nil || !sameBasis(t.Basis, refs) {
			return workStopped(req, "wait_environment", "applicable knowledge changed or is unavailable", t, ""), nil
		}
	}
	if t.Step == "delivery" {
		return workStopped(req, "wait_human", "delivery requires explicit authorization and receipt", t, ""), nil
	}
	if t.Step == "execute" && t.Plan.Preset == "strict" && !t.Plan.Approved {
		return workStopped(req, "wait_human", "strict plan needs approval", t, ""), nil
	}
	if strings.TrimSpace(req.Runner) == "" {
		return workStopped(req, "wait_runner", "no runner supplied for the next leaf", t, t.Step), nil
	}
	for _, capability := range selected.capabilities {
		if !slices.Contains(req.Capabilities, capability) {
			return workStopped(req, "wait_capability", "runner lacks capability: "+capability, t, ""), nil
		}
	}
	unit := t.Step
	if t.Step == "verify" {
		unit = ""
		for _, check := range t.Definition.Checks {
			if !check.Required {
				continue
			}
			if ev := CurrentEvidence(t, check); ev != nil && (ev.Result == "pass" || (t.Definition.Goal == "report" && ev.Result == "fail")) {
				continue
			}
			unit = check.ID
			if len(check.Command) == 0 && check.Method != "model" && check.Method != "scenario" {
				return workStopped(req, "wait_human", "check requires a human decision: "+check.ID, t, unit), nil
			}
			if len(check.Command) == 0 && !slices.Contains(req.Capabilities, check.Method) {
				return workStopped(req, "wait_capability", "runner lacks check capability: "+check.Method, t, unit), nil
			}
			break
		}
		if unit == "" {
			return workStopped(req, "wait_human", "no pending required check can advance this leaf", t, ""), nil
		}
	}
	result := workStopped(req, "ready", "", t, unit)
	result.StopReason = ""
	result.stateHash = taskStateHash(*t)
	result.handoffFrom = handoffFrom
	return result, nil
}

func (s *Service) NextWork(req WorkRequest) (WorkResult, error) {
	var result WorkResult
	err := s.Store.Write(func(tx *store.Tx) error {
		var err error
		result, err = s.chooseWorkTx(tx, req)
		return err
	})
	return result, err
}

func (s *Service) ClaimNextWork(req WorkRequest) (WorkResult, error) {
	if os.Getenv("AEL_MANAGED_CHILD") == "1" {
		return WorkResult{}, Error("MANAGED_CHILD_FORBIDDEN", "managed child must return its current result to the parent runner")
	}
	var choice WorkResult
	for retry := 0; retry < 3; retry++ {
		var err error
		choice, err = s.NextWork(req)
		if err != nil || choice.Action != "ready" {
			return choice, err
		}
		guard := func(tx *store.Tx, task *Task) error {
			current, err := s.chooseWorkTx(tx, req)
			if err != nil {
				return err
			}
			if current.Action != "ready" || current.Checkpoint.Task != task.Definition.ID || current.Checkpoint.Unit != choice.Checkpoint.Unit || current.stateHash != choice.stateHash || current.handoffFrom != choice.handoffFrom {
				return Error("NEXT_WORK_CHANGED", "refresh parent work selection")
			}
			if current.handoffFrom != "" {
				if _, err = tx.Exec("DELETE FROM metadata WHERE key=?", workAssignmentKey(current.handoffFrom)); err != nil {
					return err
				}
				previous := WorkResult{Action: "released", StopReason: "handoff", Detail: "successor claimed the same unfinished leaf", Checkpoint: WorkCheckpoint{Parent: req.Parent, Runner: current.handoffFrom, Task: task.Definition.ID, Step: task.Step, Revision: task.Definition.Revision, PlanRevision: task.Plan.Revision, At: time.Now().UTC()}}
				previousBody, err := json.Marshal(previous)
				if err != nil {
					return err
				}
				if _, err = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", workCheckpointKey(req.Parent, current.handoffFrom), string(previousBody)); err != nil {
					return err
				}
				if err = tx.Event(req.Parent, "runner_handoff", map[string]string{"from": current.handoffFrom, "to": req.Runner, "task": task.Definition.ID}); err != nil {
					return err
				}
			}
			assignment := workAssignment{Parent: req.Parent, Runner: req.Runner, Task: task.Definition.ID, Resources: slices.Clone(req.Resources), Parallel: req.Parallel, Workspace: req.Workspace}
			body, err := json.Marshal(assignment)
			if err != nil {
				return err
			}
			if _, err = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", workAssignmentKey(req.Runner), string(body)); err != nil {
				return err
			}
			checkpoint := WorkResult{Action: "claimed", StopReason: "in_progress", Checkpoint: current.Checkpoint}
			checkpointBody, err := json.Marshal(checkpoint)
			if err != nil {
				return err
			}
			if _, err = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", workCheckpointKey(req.Parent, req.Runner), string(checkpointBody)); err != nil {
				return err
			}
			return tx.Event(req.Parent, "runner_assigned", assignment)
		}
		n, err := s.claimGuarded(choice.Checkpoint.Task, choice.Checkpoint.Unit, req.Runner, req.Workspace, req.Session, nil, guard)
		if err != nil {
			if strings.Contains(err.Error(), "NEXT_WORK_CHANGED") || strings.Contains(err.Error(), "CLAIM_CONFLICT") || strings.Contains(err.Error(), "NOT_RUNNABLE") {
				continue
			}
			return choice, err
		}
		choice.Action, choice.Next = "claimed", &n
		choice.Checkpoint.Step, choice.Checkpoint.Revision, choice.Checkpoint.PlanRevision = n.Step, n.Revision, n.PlanRevision
		return choice, nil
	}
	choice.Action, choice.StopReason, choice.Detail = "wait_retry", "selection_changed", "work changed repeatedly during claim; inspect and retry"
	return choice, nil
}

func (s *Service) ReleaseWork(parent, runner, reason string) (map[string]any, error) {
	if err := s.active(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(runner) == "" || strings.TrimSpace(reason) == "" {
		return nil, Error("REASON_REQUIRED", "runner and handoff reason required")
	}
	err := s.Store.Write(func(tx *store.Tx) error {
		var raw string
		if err := tx.Row("SELECT body FROM metadata WHERE key=?", workAssignmentKey(runner)).Scan(&raw); err != nil {
			return Error("RUNNER_ASSIGNMENT_MISSING", runner)
		}
		var a workAssignment
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			return err
		}
		if a.Parent != parent {
			return Error("RUNNER_ASSIGNMENT_MISMATCH", runner)
		}
		t, err := load(tx, a.Task)
		if err != nil {
			return err
		}
		if hasWriter(t) {
			return Error("RUNNING_ATTEMPT", "finish or safely recover the previous attempt first")
		}
		if _, err = tx.Exec("DELETE FROM metadata WHERE key=?", workAssignmentKey(runner)); err != nil {
			return err
		}
		checkpoint := WorkResult{Action: "released", StopReason: "handoff", Detail: reason, Checkpoint: WorkCheckpoint{Parent: parent, Runner: runner, Task: a.Task, Step: t.Step, Revision: t.Definition.Revision, PlanRevision: t.Plan.Revision, At: time.Now().UTC()}}
		body, err := json.Marshal(checkpoint)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", workCheckpointKey(parent, runner), string(body)); err != nil {
			return err
		}
		return tx.Event(parent, "runner_released", map[string]string{"runner": runner, "task": a.Task, "reason": reason})
	})
	return map[string]any{"released": runner, "parent": parent}, err
}

func (s *Service) SaveWorkCheckpoint(result WorkResult) error {
	result.Next = nil // Never persist attempt tokens or process details in a checkpoint.
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return s.Store.Write(func(tx *store.Tx) error {
		_, err := tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", workCheckpointKey(result.Checkpoint.Parent, result.Checkpoint.Runner), string(body))
		return err
	})
}

func (s *Service) WorkCheckpoint(parent, runner string) (WorkResult, error) {
	var result WorkResult
	var body string
	if err := s.Store.DB.QueryRow("SELECT body FROM metadata WHERE key=?", workCheckpointKey(parent, runner)).Scan(&body); err != nil {
		if err == sql.ErrNoRows {
			return result, Error("CHECKPOINT_MISSING", runner)
		}
		return result, err
	}
	err := json.Unmarshal([]byte(body), &result)
	return result, err
}
