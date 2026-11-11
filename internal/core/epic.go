package core

import (
	"ael/internal/store"
	"encoding/json"
	"strings"
)

// EpicTree is a read-only projection of actual leaf states. Completion of the
// children does not itself complete the parent or claim integration acceptance.
type EpicTree struct {
	Root              Next             `json:"root"`
	Children          []EpicTreeMember `json:"children"`
	Required          int              `json:"required"`
	RequiredCompleted int              `json:"required_completed"`
}

type EpicTreeMember struct {
	Required  bool   `json:"required"`
	Milestone string `json:"milestone,omitempty"`
	Task      Next   `json:"task"`
}

func validateEpicParent(tx *store.Tx, parent, child string) error {
	if parent == child {
		return Error("PARENT_CYCLE", child)
	}
	t, err := load(tx, parent)
	if err != nil {
		return Error("EPIC_MISSING", parent)
	}
	if t.Definition.Type != "epic" || t.Status != "open" {
		return Error("EPIC_NOT_OPEN", parent)
	}
	return nil
}

// LinkEpic adds one existing leaf without rewriting its historical definition,
// result, candidate or completion time. The record is saved with the task.
func (s *Service) LinkEpic(parent, child string, required bool, milestone string) (Next, error) {
	return s.linkEpic(parent, child, required, milestone, "", "", "", "")
}

func (s *Service) linkEpic(parent, child string, required bool, milestone, expectedPolicy, expectedHash, planKey, expectedState string) (Next, error) {
	if err := s.active(); err != nil {
		return Next{}, err
	}
	read := s.Get
	if expectedPolicy != "" {
		read = s.planningTask
	}
	if _, err := read(parent); err != nil {
		return Next{}, Error("EPIC_MISSING", parent)
	}
	if _, err := read(child); err != nil {
		return Next{}, Error("TASK_MISSING", child)
	}
	err := s.Store.Write(func(tx *store.Tx) error {
		if err := checkPlanActive(tx, parent, planKey); err != nil {
			return err
		}
		if expectedPolicy != "" {
			policy, err := s.policyHash()
			if err != nil || policy != expectedPolicy {
				return Error("PLAN_STALE_POLICY", "project policy changed during application")
			}
		}
		if err := validateEpicParent(tx, parent, child); err != nil {
			return err
		}
		t, err := load(tx, child)
		if err != nil {
			return Error("TASK_MISSING", child)
		}
		if t.Definition.Type == "epic" {
			return Error("EPIC_CHILD_INVALID", "only leaf tasks may be associated")
		}
		if expectedHash != "" && t.DefinitionHash != expectedHash {
			return Error("PLAN_TASK_CHANGED", child)
		}
		if expectedState != "" && taskStateHash(t) != expectedState {
			return Error("PLAN_TASK_CHANGED", child)
		}
		if expectedState != "" {
			if err := s.checkTaskFile(t); err != nil {
				return err
			}
		}
		if t.EpicMembership != nil {
			return Error("TASK_ALREADY_LINKED", t.EpicMembership.Parent)
		}
		if hasWriter(t) {
			return Error("RUNNING_ATTEMPT", "stop or finish the leaf before associating it")
		}
		t.EpicMembership = &EpicMembership{Parent: parent, Required: required, Milestone: strings.TrimSpace(milestone)}
		if planKey != "" {
			err = s.savePlan(tx, &t)
		} else {
			err = s.save(tx, &t)
		}
		if err != nil {
			return err
		}
		return tx.Event(child, "epic_linked", t.EpicMembership)
	})
	if err != nil {
		return Next{}, err
	}
	if err = s.Store.Flush(); err != nil {
		return Next{}, err
	}
	return s.Next(child)
}

func (s *Service) epicMembers(parent string) ([]Task, error) {
	rows, err := s.Store.DB.Query("SELECT body FROM tasks WHERE json_extract(body,'$.epic_membership.parent')=? ORDER BY id", parent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []Task{}
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		var t Task
		if err = json.Unmarshal([]byte(body), &t); err != nil {
			return nil, err
		}
		members = append(members, t)
	}
	return members, rows.Err()
}

func (s *Service) summarizeEpicNext(n *Next, id string) error {
	members, err := s.epicMembers(id)
	if err != nil {
		return err
	}
	required, completed := 0, 0
	for _, member := range members {
		if !member.EpicMembership.Required {
			continue
		}
		required++
		if member.Status == "completed" && !member.Historical {
			completed++
		} else if member.Historical {
			n.Reasons = append(n.Reasons, "required child "+member.Definition.ID+" is historical and has not been revalidated here")
		} else {
			n.Reasons = append(n.Reasons, "required child "+member.Definition.ID+" is "+member.Status)
		}
	}
	if n.Action == "continue" || n.Action == "repair" {
		if required == 0 || completed != required {
			n.Action = "wait_children"
			if required == 0 {
				n.Reasons = append(n.Reasons, "add required leaf tasks before integration")
			}
		} else {
			n.Action = "wait_integration"
			n.Reasons = append(n.Reasons, "required leaves completed; parent integration remains to be accepted")
		}
	}
	return nil
}

func (s *Service) Tree(id string) (EpicTree, error) {
	result := EpicTree{Children: []EpicTreeMember{}}
	root, err := s.Next(id)
	if err != nil {
		return result, err
	}
	if root.Task == nil || root.Task.Definition.Type != "epic" {
		return result, Error("EPIC_REQUIRED", id)
	}
	result.Root = root
	members, err := s.epicMembers(id)
	if err != nil {
		return result, err
	}
	for _, member := range members {
		next, err := s.Next(member.Definition.ID)
		if err != nil {
			return result, err
		}
		m := member.EpicMembership
		result.Children = append(result.Children, EpicTreeMember{Required: m.Required, Milestone: m.Milestone, Task: next})
		if m.Required {
			result.Required++
			if next.Task.Status == "completed" && !next.Task.Historical {
				result.RequiredCompleted++
			}
		}
	}
	return result, nil
}
