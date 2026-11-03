package core

import (
	"ael/internal/store"
	"encoding/json"
	"gopkg.in/yaml.v3"
	"os"
	"slices"
	"time"
)

type Policy struct {
	CandidateExclude             []string `json:"candidate_exclude,omitempty" yaml:"candidate_exclude"`
	Schema                       int      `json:"schema" yaml:"schema"`
	MaxRetries                   int      `json:"max_retries" yaml:"max_retries"`
	MaxSeconds                   int      `json:"max_seconds" yaml:"max_seconds"`
	LeaseSeconds                 int      `json:"lease_seconds,omitempty" yaml:"lease_seconds"`
	Checks                       []Check  `json:"checks" yaml:"checks"`
	CriticalKnowledge            []string `json:"critical_knowledge" yaml:"critical_knowledge"`
	DeliveryRequiresConfirmation bool     `json:"delivery_requires_confirmation" yaml:"delivery_requires_confirmation"`
}

func (s *Service) Policy() (Policy, error) {
	var p Policy
	path, e := store.SafePath(s.Root, ".ael/policy.yaml")
	if e != nil {
		return p, e
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return p, e
	}
	e = yaml.Unmarshal(b, &p)
	if e != nil {
		return p, e
	}
	if p.Schema != 1 {
		return p, Error("POLICY_SCHEMA_UNSUPPORTED", "")
	}
	return p, nil
}
func (s *Service) normalize(d *Definition) error {
	p, e := s.Policy()
	if e != nil {
		return e
	}
	if d.MaxRetries <= 0 {
		d.MaxRetries = p.MaxRetries
	}
	if d.MaxSeconds == 0 {
		d.MaxSeconds = p.MaxSeconds
	}
	for _, required := range p.Checks {
		found := false
		for _, c := range d.Checks {
			if c.ID == required.ID {
				a, _ := json.Marshal(c)
				b, _ := json.Marshal(required)
				if store.Hash(a) != store.Hash(b) {
					return Error("POLICY_CHECK_CONFLICT", c.ID)
				}
				found = true
			}
		}
		if !found {
			d.Checks = append(d.Checks, required)
		}
	}
	for _, id := range p.CriticalKnowledge {
		if !slices.Contains(d.Knowledge, id) {
			d.Knowledge = append(d.Knowledge, id)
		}
	}
	return nil
}

func (s *Service) leaseDuration() (time.Duration, error) {
	p, e := s.Policy()
	if e != nil {
		return 0, e
	}
	if p.LeaseSeconds == 0 {
		return 30 * time.Minute, nil
	}
	if p.LeaseSeconds < 60 {
		return 0, Error("INVALID_LEASE_DURATION", "lease_seconds must be at least 60")
	}
	return time.Duration(p.LeaseSeconds) * time.Second, nil
}

func (s *Service) policyHash() (string, error) {
	p, e := store.SafePath(s.Root, ".ael/policy.yaml")
	if e != nil {
		return "", e
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return "", e
	}
	return store.Hash(b), nil
}
func (s *Service) syncPolicy(old Task) (Task, error) {
	d := old.Definition
	p, e := s.Policy()
	if e != nil {
		return old, e
	}
	for _, required := range p.Checks {
		found := false
		for i := range d.Checks {
			if d.Checks[i].ID == required.ID {
				d.Checks[i] = required
				found = true
			}
		}
		if !found {
			d.Checks = append(d.Checks, required)
		}
	}
	return s.syncDefinition(old, d, definitionBytes(d))
}
