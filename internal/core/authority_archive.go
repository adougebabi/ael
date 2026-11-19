package core

import (
	"ael/internal/store"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"
)

// History manifests can contain many tasks and artifact references.
const AuthorityArchiveRequestLimit = 8 << 20

var archiveSourceID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,95}$`)

func ValidArchiveSourceID(id string) bool { return archiveSourceID.MatchString(id) }

func ValidateArchiveTransferLimits(archive AuthorityArchive) error {
	if archive.Project == "" || !ValidArchiveSourceID(archive.Source) {
		return Error("TRANSFER_SOURCE_INVALID", "source project and instance identity are required before export")
	}
	for _, task := range archive.Tasks {
		if !slices.Contains([]string{"open", "completed", "cancelled"}, task.Snapshot.Status) {
			return Error("TRANSFER_SOURCE_INVALID", "source contains an unsupported task state")
		}
	}
	for _, file := range archive.Files {
		if file.Status == "available" && file.Size > RemoteArtifactFileLimit {
			return Error("TRANSFER_FILE_LIMIT", "an artifact exceeds the current remote file limit; source remains unfrozen")
		}
	}
	request := struct {
		Archive   AuthorityArchive `json:"archive"`
		Digest    string           `json:"digest"`
		Operation string           `json:"operation_id"`
		Key       string           `json:"idempotency_key"`
	}{archive, strings.Repeat("a", 64), strings.Repeat("a", 64), strings.Repeat("a", 64)}
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if len(body) > AuthorityArchiveRequestLimit {
		return Error("TRANSFER_ARCHIVE_LIMIT", "archive exceeds the current remote request limit; source remains unfrozen")
	}
	return nil
}

// AuthorityArchive carries historical facts. It never grants a live execution
// responsibility, approval, or current-candidate acceptance.
type AuthorityArchive struct {
	Schema  int            `json:"schema"`
	Project string         `json:"project"`
	Source  string         `json:"source"`
	Version string         `json:"version"`
	Tasks   []ArchivedTask `json:"tasks"`
	Files   []ArchivedFile `json:"files"`
}

type ArchiveRecovery struct {
	Paused          bool `json:"paused"`
	PendingDecision bool `json:"pending_decision"`
}

type ArchivedTask struct {
	Recovery   *ArchiveRecovery `json:"recovery,omitempty"`
	OriginalID string           `json:"original_id"`
	MappedID   string           `json:"mapped_id"`
	Snapshot   Task             `json:"snapshot"`
	Record     TaskRecord       `json:"record"`
}

type ArchivedFile struct {
	ExpectedHashes []string `json:"historical_hashes,omitempty"`
	HashMatch      string   `json:"historical_hash_match"`
	TaskID         string   `json:"task_id"`
	Ref            string   `json:"ref"`
	SHA256         string   `json:"sha256,omitempty"`
	Size           int64    `json:"size"`
	Status         string   `json:"status"`
	Kind           string   `json:"kind"`
}

// ArchiveTask preserves evidence and review provenance as historical data.
// TaskRecord remains the existing portable summary used by task history views.
func ArchiveTask(task Task, mapped string) ArchivedTask {
	snapshot := RemoteTaskView(task)
	snapshot.Plan.Approved = false
	snapshot.ContextAttempt = ""
	snapshot.Confirmation = nil
	snapshot.Approvals = nil
	for i := range snapshot.Attempts {
		snapshot.Attempts[i].Lease = time.Time{}
		snapshot.Attempts[i].Fence = 0
	}
	return ArchivedTask{Recovery: &ArchiveRecovery{Paused: task.Paused, PendingDecision: task.Confirmation != nil}, OriginalID: task.Definition.ID, MappedID: mapped, Snapshot: snapshot, Record: recordOf(snapshot)}
}

// ImportArchivedTaskTx uses the normal creation policy and relationship checks,
// then attaches portable history. Full source snapshots live in the import
// ledger and never enter the current evidence or responsibility collections.
func (s *Service) ImportArchivedTaskTx(tx *store.Tx, archived ArchivedTask, definition Definition, source string) (Task, error) {
	var membership *EpicMembership
	if archived.Snapshot.EpicMembership != nil && definition.Parent != "" {
		copy := *archived.Snapshot.EpicMembership
		copy.Parent = definition.Parent
		membership = &copy
	}
	creation := definition
	if definition.Parent != "" && membership == nil {
		// Older Local tasks used Parent for attribution to ordinary tasks.
		// Preserve that relationship without inventing Epic membership.
		parent, err := load(tx, definition.Parent)
		if err != nil || parent.Legacy["authority_import"] != source {
			return Task{}, Error("IMPORT_REFERENCE_MISSING", definition.Parent)
		}
		creation.Parent = ""
	}
	task, err := s.createTaskTx(tx, creation, membership, "", "")
	if err != nil {
		return task, err
	}
	if creation.Parent != definition.Parent {
		task.Definition.Parent = definition.Parent
		body := definitionBytes(task.Definition)
		task.DefinitionHash = store.Hash(body)
		path, err := s.taskPath(task.Definition.ID)
		if err != nil {
			return task, err
		}
		if err := tx.File(path, body); err != nil {
			return task, err
		}
	}
	record := archived.Record
	task.Plan.Approved = false
	if record.Plan.Revision > 0 {
		task.Plan.Revision = record.Plan.Revision
	}
	if record.Status == "open" && slices.Contains(task.Plan.Steps, record.Step) {
		task.Step = record.Step
		if task.Step == "verify" || task.Step == "delivery" {
			task.Step = "execute"
			if definition.Type == "epic" {
				task.Step = "integrate"
			}
		}
	}
	task.Created = record.Created
	task.Completed = record.Completed
	task.History = &record.History
	task.HumanReviewNotes = record.HumanReviewNotes
	for i := range task.HumanReviewNotes {
		task.HumanReviewNotes[i].Status = "historical"
		task.HumanReviewNotes[i].ConfirmedAt = nil
		task.HumanReviewNotes[i].ConfirmedBy = ""
		task.HumanReviewNotes[i].ConfirmedCandidate = ""
	}
	task.TimeLimitSeconds = record.TimeLimitSeconds
	task.FailureCycles, task.ActiveFailure = record.FailureCycles, record.ActiveFailure
	if task.ActiveFailure != "" {
		task.Blocker = record.FailureBlocker
	}
	task.Result, task.Failure = record.Result, record.Failure
	task.Legacy = map[string]any{"authority_import": source, "original_id": archived.OriginalID, "original_status": record.Status, "original_step": record.Step, "original_updated": record.Updated}
	// Keep parents open until all normal relationship checks have completed.
	task.Paused = true
	task.Completed = nil
	task.Confirmation = &Confirmation{Kind: "restore", Reason: "historical import requires current context and fresh execution", Revision: task.Definition.Revision, PlanRevision: task.Plan.Revision}
	if err = s.save(tx, &task); err != nil {
		return task, err
	}
	return task, tx.Event(task.Definition.ID, "authority_history_imported", map[string]string{"source": source, "original_id": archived.OriginalID, "gate_effect": "none"})
}

func (s *Service) FinalizeArchivedTaskTx(tx *store.Tx, archived ArchivedTask, source string) error {
	if archived.Record.Status == "open" {
		return nil
	}
	task, err := load(tx, archived.MappedID)
	if err != nil {
		return err
	}
	if task.Legacy["authority_import"] != source {
		return Error("IMPORT_SOURCE_CONFLICT", "task belongs to another import")
	}
	task.Status, task.Step, task.Historical = archived.Record.Status, "done", true
	task.Paused, task.Confirmation = false, nil
	task.Completed = archived.Record.Completed
	return s.save(tx, &task)
}

// This is read-only next-action guidance. Resume retains all write-time checks;
// neither a legacy flag nor an Agent's assertion supplies restoration approval.
func (t *Task) approvedImportResumeReady() bool {
	source, _ := t.Legacy["authority_import"].(string)
	if source == "" || t.Status != "open" || !t.Paused || t.Confirmation != nil || t.Blocker != nil || t.ActiveFailure != "" || pendingFailureKey(t) != "" || t.Candidate != "" || len(t.Attempts) != 0 {
		return false
	}
	for _, a := range t.Approvals {
		if a.Kind == "restore" && a.Decision == "approve" && a.Actor != "" && !a.At.IsZero() && a.Revision == t.Definition.Revision && a.PlanRevision == t.Plan.Revision && a.Candidate == "" {
			return true
		}
	}
	return false
}
