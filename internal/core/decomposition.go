package core

import (
	"ael/internal/store"
	"database/sql"
	"encoding/json"
	"os"
	"slices"
	"sort"
	"strings"
)

// A proposal is an Agent/user-authored statement. Validation checks its
// structure and references; it does not claim to understand its semantics.
type PlanInputItem struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	ExistingID  string `json:"existing_task_id"`
}

type PlanInput struct {
	Request string          `json:"request"`
	Items   []PlanInputItem `json:"items"`
}

type PlanLeaf struct {
	Key              string     `json:"key"`
	SourceIDs        []string   `json:"source_ids"`
	ExistingID       string     `json:"existing_task_id,omitempty"`
	ExpectedRevision int        `json:"expected_revision,omitempty"`
	Definition       Definition `json:"definition"`
	Deliverables     []string   `json:"deliverables"`
	Basis            []string   `json:"basis"`
	Capabilities     []string   `json:"capabilities"`
	InspectionOnly   bool       `json:"inspection_only,omitempty"`
	DependsOn        []string   `json:"depends_on"`
	Order            int        `json:"order"`
	Required         bool       `json:"required"`
	Milestone        string     `json:"milestone"`
}

type PlanCoverage struct {
	Acceptance  string   `json:"acceptance"`
	Leaves      []string `json:"leaves"`
	Integration string   `json:"integration"`
}

type PlanProposal struct {
	Version        int             `json:"version"`
	ID             string          `json:"id"`
	Parent         string          `json:"parent"`
	ParentRevision int             `json:"parent_revision"`
	InputHash      string          `json:"input_hash"`
	Request        string          `json:"request"`
	UserItems      []PlanInputItem `json:"user_items"`
	Leaves         []PlanLeaf      `json:"leaves"`
	Coverage       []PlanCoverage  `json:"coverage"`
}

type EpicPlanSnapshot struct {
	ProposalHash string            `json:"proposal_hash"`
	Digest       string            `json:"digest"`
	Proposal     PlanProposal      `json:"proposal"`
	TaskIDs      map[string]string `json:"task_ids"`
	Changes      []PlanChange      `json:"changes"`
}

func findEpicPlan(task Task, id string) *EpicPlanSnapshot {
	if task.EpicPlan != nil && task.EpicPlan.Proposal.ID == id {
		return task.EpicPlan
	}
	for i := range task.EpicPlanHistory {
		if task.EpicPlanHistory[i].Proposal.ID == id {
			return &task.EpicPlanHistory[i]
		}
	}
	return nil
}

func (s *Service) verifyPlanSnapshot(parent string, snapshot *EpicPlanSnapshot) error {
	for key, id := range snapshot.TaskIDs {
		task, err := s.planningTask(id)
		if err != nil || task.EpicMembership == nil || task.EpicMembership.Parent != parent {
			return Error("PLAN_SNAPSHOT_INCOMPLETE", "missing or unassociated leaf "+key+" ("+id+")")
		}
	}
	return nil
}

type PlanChange struct {
	Key                  string          `json:"key"`
	TaskID               string          `json:"task_id"`
	Action               string          `json:"action"`
	ExistingStatus       string          `json:"existing_status,omitempty"`
	ExistingHistorical   bool            `json:"existing_historical,omitempty"`
	Title                string          `json:"title"`
	Goal                 string          `json:"goal"`
	Scope                []string        `json:"scope"`
	Acceptance           []string        `json:"acceptance"`
	Deliverables         []string        `json:"deliverables"`
	Dependencies         []string        `json:"dependencies"`
	Order                int             `json:"order"`
	Required             bool            `json:"required"`
	Fields               []string        `json:"fields,omitempty"`
	Diff                 []PlanFieldDiff `json:"diff,omitempty"`
	RequiresConfirmation bool            `json:"requires_confirmation,omitempty"`
}

type PlanFieldDiff struct {
	Field  string `json:"field"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

type PlanPreview struct {
	ProposalHash   string         `json:"proposal_hash"`
	Digest         string         `json:"digest"`
	AlreadyApplied bool           `json:"already_applied,omitempty"`
	Changes        []PlanChange   `json:"changes"`
	Coverage       []PlanCoverage `json:"coverage"`
	Warnings       []string       `json:"warnings"`
	parent         Task
	existing       map[string]Task
	ids            map[string]string
}

type PlanPreparation struct {
	Version        int                `json:"version"`
	InputHash      string             `json:"input_hash"`
	Parent         Definition         `json:"parent"`
	Input          PlanInput          `json:"input"`
	ExistingLeaves []PlanExistingLeaf `json:"existing_leaves"`
	Instruction    string             `json:"instruction"`
}

type preparedPlanInput struct {
	Parent     string    `json:"parent"`
	ParentHash string    `json:"parent_hash"`
	Input      PlanInput `json:"input"`
}

func normalizedPlanInput(input PlanInput) PlanInput {
	if input.Items == nil {
		input.Items = []PlanInputItem{}
	}
	return input
}

func planInputKey(hash string) string { return "decomposition-input:" + hash }

type PlanExistingLeaf struct {
	Definition map[string]any  `json:"definition"`
	Status     string          `json:"status"`
	Result     string          `json:"result"`
	Membership *EpicMembership `json:"epic_membership,omitempty"`
}

func planDefinitionView(d Definition) map[string]any {
	b, _ := json.Marshal(d)
	var result map[string]any
	_ = json.Unmarshal(b, &result)
	for _, key := range []string{"intent", "original_request"} {
		if _, ok := result[key]; !ok {
			result[key] = ""
		}
	}
	// Legacy payloads may contain unrelated historical metadata. Preserve them
	// during revision without supplying them to a planning model by default.
	result["legacy"] = nil
	for _, key := range []string{"scope", "non_goals", "acceptance", "dependencies", "risk", "checks", "knowledge"} {
		if result[key] == nil {
			result[key] = []any{}
		}
	}
	if checks, ok := result["checks"].([]any); ok {
		for _, item := range checks {
			check, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if _, ok := check["method"]; !ok {
				check["method"] = ""
			}
			for _, key := range []string{"command", "covers", "artifacts"} {
				if check[key] == nil {
					check[key] = []any{}
				}
			}
		}
	}
	return result
}

// planningTask reads the current on-disk definition without reconciling it.
// Preview must not change tasks, and it must refuse stale inputs.
func (s *Service) planningTask(id string) (Task, error) {
	var t Task
	var body string
	if err := s.Store.DB.QueryRow("SELECT body FROM tasks WHERE id=?", id).Scan(&body); err != nil {
		if err == sql.ErrNoRows {
			return t, Error("TASK_MISSING", id)
		}
		return t, err
	}
	if err := json.Unmarshal([]byte(body), &t); err != nil {
		return t, err
	}
	path, err := s.taskPath(id)
	if err != nil {
		return t, err
	}
	data, err := os.ReadFile(path)
	if err != nil || store.Hash(data) != t.DefinitionHash {
		return t, Error("PLAN_STALE_TASK", "reconcile the task definition first: "+id)
	}
	policy, err := s.policyHash()
	if err != nil {
		return t, err
	}
	if t.Status == "open" && t.PolicyHash != policy {
		return t, Error("PLAN_STALE_POLICY", "reconcile current project policy first: "+id)
	}
	return t, nil
}

func (s *Service) PreparePlan(parent string, input PlanInput) (PlanPreparation, error) {
	input = normalizedPlanInput(input)
	result := PlanPreparation{Version: 1, Input: input, ExistingLeaves: []PlanExistingLeaf{}, Instruction: "Produce a PlanProposal from the supplied requirements. Reuse user item IDs and existing tasks; semantic decomposition is the Agent/user's responsibility. Validate with plan preview before applying."}
	if err := s.active(); err != nil {
		return result, err
	}
	if strings.TrimSpace(input.Request) == "" {
		return result, Error("PLAN_REQUEST_REQUIRED", "provide the original request")
	}
	root, err := s.planningTask(parent)
	if err != nil {
		return result, err
	}
	if root.Definition.Type != "epic" || root.Status != "open" {
		return result, Error("EPIC_NOT_OPEN", parent)
	}
	itemIDs := map[string]bool{}
	for _, item := range input.Items {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Description) == "" {
			return result, Error("INVALID_USER_ITEM", item.ID)
		}
		if itemIDs[item.ID] {
			return result, Error("DUPLICATE_USER_ITEM", item.ID)
		}
		itemIDs[item.ID] = true
	}
	result.Parent = root.Definition
	members, err := s.epicMembers(parent)
	if err != nil {
		return result, err
	}
	seen := map[string]bool{}
	for _, member := range members {
		current, err := s.planningTask(member.Definition.ID)
		if err != nil {
			return result, err
		}
		result.ExistingLeaves = append(result.ExistingLeaves, PlanExistingLeaf{Definition: planDefinitionView(current.Definition), Status: current.Status, Result: current.Result, Membership: current.EpicMembership})
		seen[current.Definition.ID] = true
	}
	for _, item := range input.Items {
		if item.ExistingID == "" || seen[item.ExistingID] {
			continue
		}
		current, err := s.planningTask(item.ExistingID)
		if err != nil {
			return result, err
		}
		result.ExistingLeaves = append(result.ExistingLeaves, PlanExistingLeaf{Definition: planDefinitionView(current.Definition), Status: current.Status, Result: current.Result, Membership: current.EpicMembership})
		seen[item.ExistingID] = true
	}
	bound := preparedPlanInput{Parent: parent, ParentHash: root.DefinitionHash, Input: input}
	raw, err := json.Marshal(bound)
	if err != nil {
		return result, err
	}
	result.InputHash = store.Hash(raw)
	err = s.Store.Write(func(tx *store.Tx) error {
		current, err := load(tx, parent)
		if err != nil || !equivalent(current, root) {
			return Error("PLAN_PARENT_CHANGED", parent)
		}
		_, err = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?) ON CONFLICT(key) DO NOTHING", planInputKey(result.InputHash), string(raw))
		return err
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

func planTaskID(parent, plan, key string) string {
	return "PLAN-" + store.Hash([]byte(parent + "\n" + plan + "\n" + key))[:20]
}

func nonempty(items []string) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if strings.TrimSpace(item) == "" {
			return false
		}
	}
	return true
}

func (s *Service) PreviewPlan(p PlanProposal) (PlanPreview, error) {
	preview := PlanPreview{Changes: []PlanChange{}, Coverage: p.Coverage, Warnings: []string{"Structural validation cannot judge whether the decomposition captures the user's intent."}}
	if p.Version != 1 || strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.Parent) == "" || strings.TrimSpace(p.Request) == "" {
		return preview, Error("INVALID_PLAN", "version, id, parent and original request are required")
	}
	root, err := s.planningTask(p.Parent)
	if err != nil {
		return preview, err
	}
	if prior := findEpicPlan(root, p.ID); prior != nil {
		if prior.ProposalHash != proposalHash(p) {
			return preview, Error("IDEMPOTENCY_CONFLICT", p.ID)
		}
		if err := s.verifyPlanSnapshot(p.Parent, prior); err != nil {
			return preview, err
		}
		return PlanPreview{ProposalHash: prior.ProposalHash, Digest: prior.Digest, AlreadyApplied: true,
			Changes: prior.Changes, Coverage: prior.Proposal.Coverage,
			Warnings: []string{"This plan was already applied; its receipt does not imply that current tasks are complete."}}, nil
	}
	if root.Definition.Type != "epic" || root.Status != "open" {
		return preview, Error("EPIC_NOT_OPEN", p.Parent)
	}
	if p.ParentRevision != root.Definition.Revision {
		return preview, Error("PLAN_PARENT_CHANGED", p.Parent)
	}
	var inputBody string
	if p.InputHash == "" || s.Store.DB.QueryRow("SELECT body FROM metadata WHERE key=?", planInputKey(p.InputHash)).Scan(&inputBody) != nil {
		return preview, Error("PLAN_INPUT_REQUIRED", "run plan prepare with the original request and user items")
	}
	var bound preparedPlanInput
	if err := json.Unmarshal([]byte(inputBody), &bound); err != nil || store.Hash([]byte(inputBody)) != p.InputHash || bound.Parent != p.Parent || bound.ParentHash != root.DefinitionHash || !equivalent(normalizedPlanInput(bound.Input), normalizedPlanInput(PlanInput{Request: p.Request, Items: p.UserItems})) {
		return preview, Error("PLAN_INPUT_MISMATCH", "proposal must preserve the exact prepared request and user items")
	}
	if len(p.Leaves) == 0 {
		return preview, Error("PLAN_LEAVES_REQUIRED", "provide at least one independently verifiable leaf")
	}
	items := map[string]PlanInputItem{}
	for _, item := range p.UserItems {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Description) == "" {
			return preview, Error("INVALID_USER_ITEM", item.ID)
		}
		if _, ok := items[item.ID]; ok {
			return preview, Error("DUPLICATE_USER_ITEM", item.ID)
		}
		items[item.ID] = item
	}
	seenSources := map[string]bool{}
	priorSources := map[string]string{}
	priorPlans := slices.Clone(root.EpicPlanHistory)
	if root.EpicPlan != nil {
		priorPlans = append(priorPlans, *root.EpicPlan)
	}
	for _, prior := range priorPlans {
		for _, previous := range prior.Proposal.Leaves {
			for _, source := range previous.SourceIDs {
				priorSources[source] = prior.TaskIDs[previous.Key]
			}
		}
	}
	keys := map[string]PlanLeaf{}
	ids := map[string]string{}
	usedIDs := map[string]bool{}
	titles := map[string]string{}
	responsibilities := map[string]string{}
	existing := map[string]Task{}
	for _, leaf := range p.Leaves {
		if strings.TrimSpace(leaf.Key) == "" || leaf.Order <= 0 || !nonempty(leaf.Deliverables) || !nonempty(leaf.Basis) || !nonempty(leaf.Capabilities) {
			return preview, Error("INVALID_PLAN_LEAF", leaf.Key+": key, order, deliverables, basis and capabilities are required")
		}
		if _, ok := keys[leaf.Key]; ok {
			return preview, Error("DUPLICATE_PLAN_LEAF", leaf.Key)
		}
		keys[leaf.Key] = leaf
		for _, source := range leaf.SourceIDs {
			item, ok := items[source]
			if !ok {
				return preview, Error("UNKNOWN_USER_ITEM", source)
			}
			if item.ExistingID != "" && item.ExistingID != leaf.ExistingID {
				return preview, Error("USER_TASK_MISMATCH", source)
			}
			if previous := priorSources[source]; previous != "" && previous != leaf.ExistingID {
				return preview, Error("USER_TASK_MISMATCH", source+": reuse previously mapped task "+previous)
			}
			if seenSources[source] {
				return preview, Error("DUPLICATE_USER_ITEM_MAPPING", source)
			}
			seenSources[source] = true
		}
		id := leaf.ExistingID
		definition := leaf.Definition
		if id != "" {
			if _, duplicate := existing[id]; duplicate {
				return preview, Error("DUPLICATE_EXISTING_TASK", id)
			}
			current, err := s.planningTask(id)
			if err != nil {
				return preview, err
			}
			if current.Definition.Type == "epic" {
				return preview, Error("EPIC_CHILD_INVALID", id)
			}
			if leaf.Required && current.Status == "cancelled" {
				return preview, Error("PLAN_REQUIRED_TASK_CANCELLED", id)
			}
			if current.EpicMembership != nil && (current.EpicMembership.Parent != p.Parent || current.EpicMembership.Required != leaf.Required || current.EpicMembership.Milestone != strings.TrimSpace(leaf.Milestone)) {
				return preview, Error("PLAN_MEMBERSHIP_CONFLICT", id)
			}
			if current.EpicMembership == nil && hasWriter(current) {
				return preview, Error("PLAN_TASK_BUSY", id)
			}
			existing[id] = current
			if leaf.ExpectedRevision == 0 {
				if !matchesSuppliedDefinition(leaf.Definition, current.Definition) {
					return preview, Error("PLAN_REVISION_REQUIRED", id+": supply expected_revision for a proposed definition change")
				}
				definition = current.Definition
			} else if leaf.ExpectedRevision != current.Definition.Revision {
				return preview, Error("PLAN_TASK_CHANGED", id)
			}
		} else {
			id = leaf.Definition.ID
			if id == "" {
				id = planTaskID(p.Parent, p.ID, leaf.Key)
			}
			var count int
			if err := s.Store.DB.QueryRow("SELECT count(*) FROM tasks WHERE id=?", id).Scan(&count); err != nil {
				return preview, err
			}
			if count > 0 {
				return preview, Error("TASK_EXISTS", id+": map an existing task explicitly")
			}
		}
		if usedIDs[id] {
			return preview, Error("DUPLICATE_PLAN_TASK_ID", id)
		}
		usedIDs[id] = true
		ids[leaf.Key] = id
		if strings.TrimSpace(definition.Title) == "" || !nonempty(definition.Acceptance) {
			return preview, Error("PLAN_UNVERIFIABLE_LEAF", leaf.Key+": title and acceptance are required")
		}
		if leaf.InspectionOnly && leaf.ExistingID != "" && leaf.ExpectedRevision == 0 && definition.Intent != "inspect" {
			return preview, Error("INSPECTION_AUTHORIZATION_CONFLICT", leaf.Key+": existing task has change permission")
		}
		if leaf.ExistingID != "" && leaf.ExpectedRevision == 0 && (definition.Type == "qa" || definition.Type == "verify") && definition.Intent != "inspect" {
			return preview, Error("INSPECTION_AUTHORIZATION_CONFLICT", leaf.Key+": existing QA task has change permission")
		}
		for _, criterion := range definition.Acceptance {
			covered := false
			for _, check := range definition.Checks {
				if check.Required && slices.Contains(check.Covers, criterion) {
					covered = true
				}
			}
			if !covered {
				return preview, Error("LEAF_COVERAGE_MISSING", leaf.Key+": "+criterion)
			}
		}
		canonicalTitle := strings.ToLower(strings.TrimSpace(definition.Title))
		if previous := titles[canonicalTitle]; previous != "" {
			return preview, Error("PLAN_DUPLICATE_RESPONSIBILITY", previous+" / "+leaf.Key)
		}
		titles[canonicalTitle] = leaf.Key
		parts := struct {
			Scope        []string
			Acceptance   []string
			Deliverables []string
		}{slices.Clone(definition.Scope), slices.Clone(definition.Acceptance), slices.Clone(leaf.Deliverables)}
		slices.Sort(parts.Scope)
		slices.Sort(parts.Acceptance)
		slices.Sort(parts.Deliverables)
		encoded, _ := json.Marshal(parts)
		signature := store.Hash(encoded)
		if previous := responsibilities[signature]; previous != "" {
			return preview, Error("PLAN_DUPLICATE_RESPONSIBILITY", previous+" / "+leaf.Key)
		}
		responsibilities[signature] = leaf.Key
		if leaf.ExistingID == "" || leaf.ExpectedRevision > 0 {
			if !nonempty(definition.NonGoals) || len(definition.Checks) == 0 {
				return preview, Error("PLAN_UNVERIFIABLE_LEAF", leaf.Key+": non-goals and explicit checks are required")
			}
			if (leaf.InspectionOnly || definition.Type == "qa" || definition.Type == "verify" || definition.Intent == "inspect") && (definition.Intent == "change" || (definition.Goal != "" && definition.Goal != "analysis" && definition.Goal != "report")) {
				return preview, Error("INSPECTION_GOAL_REQUIRED", leaf.Key+": inspection tasks cannot authorize business changes")
			}
			for _, check := range definition.Checks {
				if (check.Kind == "ui" && !definition.UI) || (check.Kind == "ux" && !definition.UX) {
					return preview, Error("PLAN_UNDECLARED_UI_CHECK", leaf.Key+": "+check.Kind)
				}
			}
			if definition.Type == "epic" {
				return preview, Error("EPIC_CHILD_INVALID", leaf.Key)
			}
			definition.ID = id
			if err := Validate(definition); err != nil {
				return preview, err
			}
		}
	}
	for _, item := range p.UserItems {
		if !seenSources[item.ID] {
			return preview, Error("USER_ITEM_UNMAPPED", item.ID)
		}
	}
	members, err := s.epicMembers(p.Parent)
	if err != nil {
		return preview, err
	}
	for _, member := range members {
		if _, ok := existing[member.Definition.ID]; !ok {
			return preview, Error("PLAN_EXISTING_LEAF_UNMAPPED", member.Definition.ID)
		}
	}
	coverage := map[string]bool{}
	for _, mapping := range p.Coverage {
		if !slices.Contains(root.Definition.Acceptance, mapping.Acceptance) || coverage[mapping.Acceptance] {
			return preview, Error("INVALID_PARENT_COVERAGE", mapping.Acceptance)
		}
		coverage[mapping.Acceptance] = true
		if len(mapping.Leaves) == 0 && strings.TrimSpace(mapping.Integration) == "" {
			return preview, Error("PARENT_COVERAGE_MISSING", mapping.Acceptance)
		}
		for _, key := range mapping.Leaves {
			if _, ok := keys[key]; !ok {
				return preview, Error("UNKNOWN_COVERAGE_LEAF", key)
			}
		}
		if strings.TrimSpace(mapping.Integration) == "" {
			required := false
			for _, key := range mapping.Leaves {
				required = required || keys[key].Required
			}
			if !required {
				return preview, Error("PARENT_COVERAGE_OPTIONAL", mapping.Acceptance)
			}
		}
	}
	for _, criterion := range root.Definition.Acceptance {
		if !coverage[criterion] {
			return preview, Error("PARENT_COVERAGE_MISSING", criterion)
		}
	}
	state := map[string]int{}
	var visit func(string) error
	visit = func(key string) error {
		if state[key] == 1 {
			return Error("DEPENDENCY_CYCLE", key)
		}
		if state[key] == 2 {
			return nil
		}
		state[key] = 1
		for _, dep := range keys[key].DependsOn {
			if _, ok := keys[dep]; !ok {
				return Error("PLAN_DEPENDENCY_MISSING", dep)
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[key] = 2
		return nil
	}
	for _, leaf := range p.Leaves {
		if err := visit(leaf.Key); err != nil {
			return preview, err
		}
	}
	// Validate both proposal edges and existing project dependencies as one
	// graph, including references that are not themselves proposal leaves.
	graph := map[string][]string{}
	byID := map[string]string{}
	for key, id := range ids {
		byID[id] = key
	}
	for _, leaf := range p.Leaves {
		definition := leaf.Definition
		if leaf.ExistingID != "" && leaf.ExpectedRevision == 0 {
			definition = existing[leaf.ExistingID].Definition
		}
		deps := slices.Clone(definition.Dependencies)
		for _, dep := range deps {
			if key, ok := byID[dep]; ok && keys[key].Order >= leaf.Order {
				return preview, Error("PLAN_ORDER_CONFLICT", leaf.Key+" depends on "+key)
			}
		}
		for _, key := range leaf.DependsOn {
			if keys[key].Order >= leaf.Order {
				return preview, Error("PLAN_ORDER_CONFLICT", leaf.Key+" depends on "+key)
			}
			depID := ids[key]
			if leaf.ExistingID != "" && leaf.ExpectedRevision == 0 && !slices.Contains(deps, depID) {
				return preview, Error("PLAN_EXISTING_DEPENDENCY_MISMATCH", leaf.Key+" / "+key)
			}
			if !slices.Contains(deps, depID) {
				deps = append(deps, depID)
			}
		}
		graph[ids[leaf.Key]] = deps
	}
	visited := map[string]int{}
	var checkTask func(string) error
	checkTask = func(id string) error {
		if visited[id] == 1 {
			return Error("DEPENDENCY_CYCLE", id)
		}
		if visited[id] == 2 {
			return nil
		}
		visited[id] = 1
		deps, planned := graph[id]
		if !planned {
			t, err := s.planningTask(id)
			if err != nil {
				return Error("DEPENDENCY_MISSING", id)
			}
			if t.Status == "cancelled" {
				return Error("PLAN_DEPENDENCY_CANCELLED", id)
			}
			deps = t.Definition.Dependencies
		}
		for _, dep := range deps {
			if dep == p.Parent {
				return Error("PLAN_PARENT_DEPENDENCY", id)
			}
			if task, ok := existing[dep]; ok && task.Status == "cancelled" {
				return Error("PLAN_DEPENDENCY_CANCELLED", dep)
			}
			if err := checkTask(dep); err != nil {
				return err
			}
		}
		visited[id] = 2
		return nil
	}
	for _, leaf := range p.Leaves {
		if err := checkTask(ids[leaf.Key]); err != nil {
			return preview, err
		}
	}
	for _, leaf := range p.Leaves {
		action := "create"
		status := ""
		historical := false
		fields := []string{}
		var diff []PlanFieldDiff
		confirmation := false
		var target Definition
		if leaf.ExistingID == "" {
			var err error
			target, err = s.planDefinition(p, leaf, ids, nil)
			if err != nil {
				return preview, err
			}
		}
		if leaf.ExistingID != "" {
			current := existing[leaf.ExistingID]
			target = current.Definition
			status = current.Status
			historical = current.Historical
			action = "reuse"
			if leaf.ExpectedRevision > 0 {
				desired, err := s.planDefinition(p, leaf, ids, &current)
				if err != nil {
					return preview, err
				}
				target = desired
				diff = definitionDiff(current.Definition, desired)
				for _, field := range diff {
					fields = append(fields, field.Field)
				}
				confirmation = planNeedsConfirmation(current.Definition, desired)
				if len(fields) > 0 && current.Status != "open" {
					return preview, Error("TERMINAL_TASK", "completed or cancelled leaf cannot be revised: "+leaf.ExistingID)
				}
				if len(fields) > 0 {
					action = "revise"
				}
			}
			if current.EpicMembership == nil {
				action += "+link"
			}
		}
		preview.Changes = append(preview.Changes, PlanChange{Key: leaf.Key, TaskID: ids[leaf.Key], Action: action, ExistingStatus: status, ExistingHistorical: historical,
			Title: target.Title, Goal: target.Goal, Scope: slices.Clone(target.Scope), Acceptance: slices.Clone(target.Acceptance),
			Deliverables: slices.Clone(leaf.Deliverables), Dependencies: slices.Clone(graph[ids[leaf.Key]]), Order: leaf.Order,
			Required: leaf.Required, Fields: fields, Diff: diff, RequiresConfirmation: confirmation})
	}
	sort.Slice(preview.Changes, func(i, j int) bool { return preview.Changes[i].Key < preview.Changes[j].Key })
	proposalJSON, _ := json.Marshal(p)
	preview.ProposalHash = store.Hash(proposalJSON)
	// Bind approval to the exact parent and existing task state seen in preview.
	stateJSON, _ := json.Marshal(struct {
		Proposal string
		Parent   Task
		Existing map[string]Task
	}{preview.ProposalHash, root, existing})
	preview.Digest = store.Hash(stateJSON)
	preview.parent = root
	preview.existing = existing
	preview.ids = ids
	return preview, nil
}
