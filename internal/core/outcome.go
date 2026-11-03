package core

import (
	"encoding/json"
	"time"
)

// Outcome is deliberately a portable summary, never a serialized runtime Task.
type Outcome struct {
	ID                 string           `json:"id"`
	DefinitionRevision int              `json:"definition_revision"`
	PlanRevision       int              `json:"plan_revision"`
	Status             string           `json:"status"`
	Result             string           `json:"result"`
	Candidate          string           `json:"candidate"`
	Completed          *time.Time       `json:"completed"`
	Historical         bool             `json:"historical"`
	Checks             []OutcomeCheck   `json:"checks"`
	Delivery           *OutcomeDelivery `json:"delivery,omitempty"`
}
type OutcomeCheck struct {
	ID             string            `json:"id"`
	Result         string            `json:"result"`
	Source         string            `json:"source"`
	Config         string            `json:"config_hash"`
	At             time.Time         `json:"at"`
	ArtifactHashes map[string]string `json:"artifact_hashes"`
}
type OutcomeDelivery struct {
	RunID       string `json:"run_id"`
	Environment string `json:"environment"`
	Target      string `json:"target"`
	Status      string `json:"status"`
}

func OutcomeDocument(t Task) []byte {
	o := Outcome{ID: t.Definition.ID, DefinitionRevision: t.Definition.Revision, PlanRevision: t.Plan.Revision, Status: t.Status, Result: t.Result, Candidate: t.Candidate, Completed: t.Completed, Historical: t.Historical, Checks: []OutcomeCheck{}}
	for _, e := range t.Evidence {
		o.Checks = append(o.Checks, OutcomeCheck{e.Check, e.Result, e.Source, e.Config, e.At, e.ArtifactHashes})
	}
	if t.Delivery != nil {
		d := t.Delivery
		o.Delivery = &OutcomeDelivery{d.RunID, d.Environment, d.Target, d.Status}
	}
	b, _ := json.MarshalIndent(o, "", "  ")
	return b
}
