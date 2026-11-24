package main

import (
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"fmt"
	"time"
)

// A claim receipt is also a lease file. Keep the original nested receipt so
// existing consumers can continue to read task and attempt.
func agentClaimLease(response map[string]any, runner, revision string) {
	attempt, ok := response["attempt"].(map[string]any)
	if !ok {
		return
	}
	response["runner_id"], response["claim_revision"] = runner, revision
	response["expected_revision"] = revision
	response["attempt_id"], response["token"], response["fence"] = attempt["id"], attempt["token"], attempt["fence"]
	response["lease_until"] = attempt["lease_until"]
	if task, ok := response["task"].(map[string]any); ok {
		if definition, ok := task["definition"].(map[string]any); ok {
			response["task_id"] = definition["id"]
		}
	}
}

func readAgentLease(path string) (agentUploadLease, map[string]any, error) {
	var lease agentUploadLease
	var receipt map[string]any
	if err := readAgentRequest(path, &receipt); err != nil {
		return lease, nil, err
	}
	if attempt, ok := receipt["attempt"].(map[string]any); ok {
		revision, _ := receipt["claim_revision"].(string)
		runner, _ := receipt["runner_id"].(string)
		// Older CLI receipts retained the original claim request privately.
		if revision == "" {
			operation, _ := receipt["operation_file"].(string)
			record, err := readAgentOperation(operation)
			if err != nil || record.Action != "claim" {
				return lease, nil, core.Error("AGENT_LEASE_INVALID", "legacy receipt needs its retained claim operation to recover claim_revision")
			}
			revision, _ = record.Payload["expected_revision"].(string)
			runner = record.Runner
			if record.Receipt != nil {
				original, _ := record.Receipt["attempt"].(map[string]any)
				if original["id"] != attempt["id"] {
					return lease, nil, core.Error("AGENT_LEASE_INVALID", "claim operation belongs to a different attempt")
				}
			}
		}
		agentClaimLease(receipt, runner, revision)
	}
	raw, _ := json.Marshal(receipt)
	if err := json.Unmarshal(raw, &lease); err != nil {
		return lease, nil, err
	}
	if lease.RunnerID == "" || lease.TaskID == "" || lease.AttemptID == "" || lease.Token == "" || lease.Fence < 1 || lease.ExpectedRevision == "" {
		return lease, nil, core.Error("AGENT_LEASE_INVALID", "use the private runner claim --out receipt or a complete upload lease")
	}
	return lease, receipt, nil
}

func applyAgentLease(body map[string]any, lease agentUploadLease, action, runner string) error {
	if runner != lease.RunnerID {
		return core.Error("AGENT_LEASE_INVALID", "runner differs from lease runner_id")
	}
	fields := map[string]any{"task_id": lease.TaskID, "attempt_id": lease.AttemptID, "token": lease.Token, "fence": lease.Fence}
	if action == "observe" || action == "submit" {
		fields["expected_revision"] = lease.ExpectedRevision
	}
	for field, value := range fields {
		if supplied, exists := body[field]; exists && fmt.Sprint(supplied) != fmt.Sprint(value) {
			return core.Error("AGENT_LEASE_INVALID", "request "+field+" differs from the claim receipt; omit it when using --lease-file")
		}
		body[field] = value
	}
	return nil
}

func updateAgentLease(path string, receipt, heartbeat map[string]any) error {
	if heartbeat["attempt_id"] != receipt["attempt_id"] || fmt.Sprint(heartbeat["fence"]) != fmt.Sprint(receipt["fence"]) || heartbeat["lease_until"] == nil {
		return core.Error("AGENT_LEASE_INVALID", "heartbeat receipt does not match lease")
	}
	unlock, err := lockAgentOperation(path)
	if err != nil {
		return err
	}
	defer unlock()
	current, latest, err := readAgentLease(path)
	if err != nil {
		return err
	}
	if current.AttemptID != receipt["attempt_id"] || current.Token != receipt["token"] || fmt.Sprint(current.Fence) != fmt.Sprint(receipt["fence"]) {
		return core.Error("AGENT_LEASE_INVALID", "claim receipt was replaced")
	}
	value, _ := heartbeat["lease_until"].(string)
	until, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return core.Error("AGENT_LEASE_INVALID", "invalid lease deadline")
	}
	if !until.After(current.LeaseUntil) {
		return nil
	}
	receipt = latest
	receipt["lease_until"] = heartbeat["lease_until"]
	if attempt, ok := receipt["attempt"].(map[string]any); ok {
		attempt["lease_until"] = heartbeat["lease_until"]
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	return store.Atomic(path, raw, 0600)
}
