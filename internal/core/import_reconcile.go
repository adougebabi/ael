package core

import (
	"ael/internal/store"
	"encoding/json"
	"slices"
	"strings"
)

// ReconcileImport removes only a proven import recovery hold. It is an Agent
// observation, never a human approval or acceptance of historical evidence.
func (s *Service) ReconcileImport(id, contextID, summary, start string) (Next, error) {
	if !s.RemoteAuthority || s.remoteOperation == nil {
		return Next{}, Error("REMOTE_OPERATION_REQUIRED", "use the authorized remote task action")
	}
	if strings.TrimSpace(summary) == "" || strings.TrimSpace(start) == "" || len(summary) > 8000 || len(start) > 4000 {
		return Next{}, Error("IMPORT_CONTEXT_REQUIRED", "provide the reconciliation findings and concrete continuation point")
	}
	return s.mutate(id, "import_reconciled", func(tx *store.Tx, t *Task) error {
		if t.Status != "open" {
			return Error("TERMINAL_TASK", id)
		}
		var source, state, body string
		if err := tx.Row("SELECT i.id,i.state,i.body FROM authority_imports i JOIN authority_import_tasks m ON m.import_id=i.id WHERE m.task_id=?", id).Scan(&source, &state, &body); err != nil || t.Legacy["authority_import"] != source {
			return Error("IMPORT_SOURCE_UNKNOWN", "missing authoritative import ledger; review the original recovery scope in the task page")
		}
		if state != "active" {
			return Error("IMPORT_NOT_ACTIVE", "activate the verified import before reconciliation")
		}
		if t.ActiveFailure != "" || t.Blocker != nil {
			return Error("IMPORT_PAUSE_PROTECTED", "resolve the task's recorded blocker or failure review first")
		}
		for _, a := range t.Attempts {
			if a.Status == "running" || a.Status == "interrupted" {
				return Error("IMPORT_PAUSE_PROTECTED", "another execution responsibility needs reconciliation; use runner recover")
			}
		}
		var paused int
		if err := tx.Row("SELECT count(*) FROM events WHERE task_id=? AND kind='paused' AND id>(SELECT COALESCE(MAX(id),0) FROM events WHERE task_id=? AND kind IN ('authority_history_imported','resumed'))", id, id).Scan(&paused); err != nil {
			return err
		}
		if paused > 0 {
			return Error("IMPORT_PAUSE_PROTECTED", "a later explicit pause exists; resume that pause under its current authorization first")
		}
		c := t.Confirmation
		if !t.Paused && (c == nil || c.Kind != "restore") {
			return Error("IMPORT_PAUSE_PROTECTED", "the import recovery hold has already been resolved; consume the current next action")
		}
		if c != nil && (c.Kind != "restore" || c.Revision != t.Definition.Revision || c.PlanRevision != t.Plan.Revision) {
			return Error("IMPORT_PAUSE_PROTECTED", "a current business or version decision requires its own confirmation")
		}
		var archive AuthorityArchive
		if json.Unmarshal([]byte(body), &archive) != nil {
			return Error("IMPORT_SOURCE_UNKNOWN", "original archive cannot be read")
		}
		known := false
		for _, item := range archive.Tasks {
			if item.MappedID == id && item.Recovery != nil {
				known = !item.Recovery.Paused && !item.Recovery.PendingDecision
			}
		}
		// Existing explicit, authenticated recovery approval is current authority;
		// an Agent's own summary cannot supply it.
		for _, approval := range t.Approvals {
			if approval.Kind == "restore" && approval.Decision == "approve" && approval.Revision == t.Definition.Revision && approval.PlanRevision == t.Plan.Revision {
				known = true
			}
		}
		if !known {
			return Error("IMPORT_SOURCE_UNKNOWN", "archive lacks unambiguous pause/decision provenance; reviewer must confirm the current restore scope on the task page (a reviewed batch may use the same exact-version confirm protocol)")
		}
		var contextBody string
		if contextID == "" || tx.Row("SELECT body FROM portable_contexts WHERE id=? AND task_id=?", contextID, id).Scan(&contextBody) != nil {
			return Error("IMPORT_CONTEXT_REQUIRED", "capture the current engineering candidate and publish/read its context first")
		}
		var ctx struct {
			Revision   string `json:"task_revision"`
			Actor      string `json:"actor"`
			Known      bool   `json:"inputs_known"`
			Unverified bool   `json:"inventory_unverified"`
		}
		if json.Unmarshal([]byte(contextBody), &ctx) != nil || ctx.Revision != RemoteRevision(*t) || ctx.Actor != s.remoteOperation.Actor || !ctx.Known || ctx.Unverified {
			return Error("IMPORT_CONTEXT_REQUIRED", "context must belong to this actor and exact task revision with verified required inputs")
		}
		t.Paused = false
		t.Confirmation = nil
		t.Plan.Approved = false
		if t.Plan.Preset == "strict" && slices.Index(t.Plan.Steps, t.Step) > slices.Index(t.Plan.Steps, "analysis") {
			t.Step = "analysis"
		}
		return tx.Event(id, "agent_import_reconciliation", map[string]string{"actor": s.remoteOperation.Actor, "context_id": contextID, "summary": summary, "start": start, "source": source, "gate_effect": "import_hold_only"})
	})
}
