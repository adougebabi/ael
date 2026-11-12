package server

import (
	"ael/internal/core"
	"ael/internal/store"
	"testing"
	"time"
)

// This tests the heartbeat response contract directly. It does not bypass or
// claim to validate the HTTP isolation gate for actual remote execution.
func TestRemoteRunnerHeartbeatReturnsServerLeaseInterval(t *testing.T) {
	f := newArtifactFixture(t)
	service, err := f.s.projectService("shared")
	if err != nil {
		t.Fatal(err)
	}
	portable, err := f.s.portable("shared", service)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := f.s.token(f.uploader)
	if err != nil {
		t.Fatal(err)
	}
	var attempt core.Attempt
	err = service.Store.Write(func(tx *store.Tx) error {
		_, claimed, err := service.RemoteClaimTx(tx, core.RemoteClaimInput{TaskID: "task", ExpectedRevision: f.revision, Runner: "runner-a", Workspace: "workspace-a", Candidate: "candidate", Token: "fixture-lease-token"})
		if err != nil {
			return err
		}
		attempt = claimed
		_, err = tx.Exec("INSERT INTO remote_runner_attempts(attempt_id,task_id,runner_id,actor,context_id,candidate_id,claim_revision,fence,token_hash) VALUES(?,?,?,?,?,?,?,?,?)", attempt.ID, "task", "runner-a", principal.UserID, "fixture-context", "candidate", f.revision, attempt.Fence, store.Hash([]byte(attempt.Token)))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := portable.heartbeatRunner(principal, "runner-a", remoteRunnerHeartbeatInput{TaskID: "task", AttemptID: attempt.ID, Token: attempt.Token, Fence: attempt.Fence, OperationID: "heartbeat", IdempotencyKey: "heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	issuedText, issuedOK := response["lease_issued_at"].(string)
	untilText, untilOK := response["lease_until"].(string)
	issued, issuedErr := time.Parse(time.RFC3339Nano, issuedText)
	until, untilErr := time.Parse(time.RFC3339Nano, untilText)
	if !issuedOK || !untilOK || issuedErr != nil || untilErr != nil {
		t.Fatalf("missing server lease start: %+v", response)
	}
	if !until.After(issued) || response["attempt_id"] != attempt.ID || response["fence"] != float64(attempt.Fence) {
		t.Fatalf("invalid heartbeat interval: %+v", response)
	}
}
