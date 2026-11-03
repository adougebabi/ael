package core_test

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/process"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNoImplicitOneHourBudget(t *testing.T) {
	s := fixture(t)
	id := create(t, s, bug())
	n, _ := s.Next(id)
	if n.Task.Definition.MaxSeconds != 0 {
		t.Fatalf("implicit hard limit: %d", n.Task.Definition.MaxSeconds)
	}
	step(t, s, id, "one")
	task, _ := s.Get(id)
	end := time.Now().Add(-time.Minute)
	task.Attempts[0].Started = end.Add(-2 * time.Hour)
	task.Attempts[0].Ended = &end
	b, _ := json.Marshal(task)
	s.Store.DB.Exec("UPDATE tasks SET body=? WHERE id=?", string(b), id)
	n, e := s.Claim(id, "", "two", "", "")
	if e != nil {
		t.Fatalf("task blocked after an hour: %v", e)
	}
	if n.Attempt.Lease.Sub(time.Now()) < 25*time.Minute {
		t.Fatal("attached claim still expires during a normal model queue")
	}
}

func TestExplicitTimeBudgetStillStopsAndUnlimitedOverrideWorks(t *testing.T) {
	for _, seconds := range []int{1, -1} {
		t.Run(fmt.Sprint(seconds), func(t *testing.T) {
			s := fixture(t)
			d := bug()
			d.MaxSeconds = seconds
			id := create(t, s, d)
			step(t, s, id, "first")
			task, _ := s.Get(id)
			end := time.Now().Add(-time.Minute)
			task.Attempts[0].Started = end.Add(-2 * time.Hour)
			task.Attempts[0].Ended = &end
			b, _ := json.Marshal(task)
			s.Store.DB.Exec("UPDATE tasks SET body=? WHERE id=?", string(b), id)
			_, e := s.Claim(id, "", "second", "", "")
			if (e != nil) != (seconds > 0) {
				t.Fatalf("budget %d: %v", seconds, e)
			}
		})
	}
}

func TestBudgetAdjustmentPreservesAcceptanceAndCurrentStep(t *testing.T) {
	s := fixture(t)
	d := bug()
	d.MaxSeconds = 1
	id := create(t, s, d)
	step(t, s, id, "one")
	before, _ := s.Get(id)
	end := time.Now().Add(-time.Minute)
	before.Attempts[0].Started = end.Add(-time.Hour)
	before.Attempts[0].Ended = &end
	b, _ := json.Marshal(before)
	s.Store.DB.Exec("UPDATE tasks SET body=? WHERE id=?", string(b), id)
	n, _ := s.Next(id)
	if n.Action != "wait_environment" {
		t.Fatal("explicit limit did not stop task")
	}
	n, e := s.SetTimeLimit(id, 0, "remove old default one-hour ceiling")
	if e != nil || n.Action != "continue" || n.Revision != before.Definition.Revision || n.Task.Step != before.Step || n.Task.Confirmation != nil {
		t.Fatal(n, e)
	}
	clone := copyCheckout(t, s.Root)
	other, _, e := bootstrap.Open(clone)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Store.Close()
	restored, _ := other.Get(id)
	if restored.TimeLimitSeconds == nil || *restored.TimeLimitSeconds != 0 {
		t.Fatal("budget adjustment did not travel")
	}
}

func TestPortableCancelledResultKeepsReasonAndCompletionTime(t *testing.T) {
	s := fixture(t)
	id := create(t, s, bug())
	before, e := s.Cancel(id, "mistaken probe-x")
	if e != nil {
		t.Fatal(e)
	}
	clone := copyCheckout(t, s.Root)
	other, _, e := bootstrap.Open(clone)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Store.Close()
	after, e := other.Get(id)
	if e != nil || after.Result != "mistaken probe-x" || !after.Completed.Equal(*before.Task.Completed) || !after.Historical {
		t.Fatal(after, e)
	}
}

func TestExternalCheckpointDoesNotOverwriteLocalWriter(t *testing.T) {
	s := fixture(t)
	id := create(t, s, bug())
	if _, e := s.Claim(id, "", "writer", "", ""); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(s.Root, ".ael/records/"+id+".json")
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	var doc map[string]any
	if e = json.Unmarshal(b, &doc); e != nil {
		t.Fatal(e)
	}
	doc["failure"] = "incoming remote checkpoint"
	b, _ = json.Marshal(doc)
	os.WriteFile(p, b, 0644)
	if e = s.RecoverDefinitions(); e == nil || !strings.Contains(e.Error(), "TASK_RECORD_CONFLICT") {
		t.Fatal(e)
	}
	var body string
	s.Store.DB.QueryRow("SELECT body FROM tasks WHERE id=?", id).Scan(&body)
	if strings.Contains(body, "incoming remote checkpoint") {
		t.Fatal("live state overwritten")
	}
}

func TestDeleteRemovesTaskAndDoesNotResurrect(t *testing.T) {
	s := fixture(t)
	id := create(t, s, bug())
	oldClone := copyCheckout(t, s.Root)
	old, _, e := bootstrap.Open(oldClone)
	if e != nil {
		t.Fatal(e)
	}
	old.Store.Close()
	if _, e = s.Cancel(id, "probe entered by mistake"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Delete(id, "remove mistaken probe"); e != nil {
		t.Fatal(e)
	}
	for _, rel := range []string{"work-items/" + id + ".md", "records/" + id + ".json", "outcomes/" + id + ".json"} {
		if _, e = os.Stat(filepath.Join(s.Root, ".ael", rel)); !os.IsNotExist(e) {
			t.Fatalf("deleted task file remains: %s %v", rel, e)
		}
	}
	if _, e = s.Get(id); e == nil {
		t.Fatal("task still exists")
	}
	if e = s.RecoverDefinitions(); e != nil {
		t.Fatal(e)
	}
	if all, e := s.List(); e != nil || len(all) != 0 {
		t.Fatal(all, e)
	}
	// Simulate a Git update reaching a machine that already had the old record.
	for _, rel := range []string{"work-items/" + id + ".md", "records/" + id + ".json"} {
		os.Remove(filepath.Join(oldClone, ".ael", rel))
	}
	b, e := os.ReadFile(filepath.Join(s.Root, ".ael/deleted/"+id+".json"))
	if e != nil {
		t.Fatal(e)
	}
	os.MkdirAll(filepath.Join(oldClone, ".ael/deleted"), 0755)
	os.WriteFile(filepath.Join(oldClone, ".ael/deleted/"+id+".json"), b, 0644)
	old, _, e = bootstrap.Open(oldClone)
	if e != nil {
		t.Fatal(e)
	}
	defer old.Store.Close()
	if all, e := old.List(); e != nil || len(all) != 0 {
		t.Fatal("deletion did not travel", all, e)
	}
}

func TestDeleteRejectsLiveWritersAndReferences(t *testing.T) {
	s := fixture(t)
	id := create(t, s, bug())
	n, e := s.Claim(id, "", "agent", "", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Delete(id, "oops"); e == nil || !strings.Contains(e.Error(), "RUNNING_ATTEMPT") {
		t.Fatal(e)
	}
	if _, e = s.Submit(core.Submission{Task: id, Attempt: n.Attempt.ID, Token: n.Attempt.Token, Key: "define", Result: core.StepResult{Status: "pass", Summary: "defined"}}); e != nil {
		t.Fatal(e)
	}
	d := bug()
	d.Dependencies = []string{id}
	dep := create(t, s, d)
	if _, e = s.Delete(id, "oops"); e == nil || !strings.Contains(e.Error(), "TASK_REFERENCED") {
		t.Fatal(e)
	}
	if _, e = s.Delete(dep, "delete dependent first"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Delete(id, "delete stopped task"); e != nil {
		t.Fatal(e)
	}
}

func TestBatchDeletedReferencesConvergeRegardlessOfIDOrder(t *testing.T) {
	s := fixture(t)
	d := bug()
	d.ID = "A"
	create(t, s, d)
	d = bug()
	d.ID = "Z"
	d.Dependencies = []string{"A"}
	create(t, s, d)
	clone := copyCheckout(t, s.Root)
	other, _, e := bootstrap.Open(clone)
	if e != nil {
		t.Fatal(e)
	}
	other.Store.Close()
	for _, id := range []string{"Z", "A"} {
		if _, e = s.Delete(id, "remove temporary chain"); e != nil {
			t.Fatal(e)
		}
	}
	os.MkdirAll(filepath.Join(clone, ".ael/deleted"), 0755)
	for _, id := range []string{"A", "Z"} {
		for _, rel := range []string{"work-items/" + id + ".md", "records/" + id + ".json"} {
			os.Remove(filepath.Join(clone, ".ael", rel))
		}
		b, e := os.ReadFile(filepath.Join(s.Root, ".ael/deleted/"+id+".json"))
		if e != nil {
			t.Fatal(e)
		}
		os.WriteFile(filepath.Join(clone, ".ael/deleted/"+id+".json"), b, 0644)
	}
	other, _, e = bootstrap.Open(clone)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Store.Close()
	if all, e := other.List(); e != nil || len(all) != 0 {
		t.Fatal(all, e)
	}
}

func TestDeletedStableIDCannotBeAccidentallyReused(t *testing.T) {
	s := fixture(t)
	d := bug()
	d.ID = "probe"
	create(t, s, d)
	if _, e := s.Delete("probe", "mistake"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Create(d); e == nil || !strings.Contains(e.Error(), "TASK_ID_DELETED") {
		t.Fatal(e)
	}
	if e := s.RecoverDefinitions(); e != nil {
		t.Fatal(e)
	}
}

func TestFullBackupCanFollowProjectRecordRecovery(t *testing.T) {
	s := fixture(t)
	id := atVerify(t, s, bug())
	backup := filepath.Join(t.TempDir(), "snapshot")
	if _, e := s.Backup(backup); e != nil {
		t.Fatal(e)
	}
	clone := copyCheckout(t, s.Root)
	other, _, e := bootstrap.Open(clone)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Store.Close()
	if _, e = other.Restore(backup); e != nil {
		t.Fatal("fresh clone with records could not restore full backup", e)
	}
	task, e := other.Get(id)
	if e != nil || len(task.Attempts) != 2 {
		t.Fatal(task, e)
	}
	for _, a := range task.Attempts {
		if a.Token != "" || a.PID != 0 || a.OwnerPID != 0 {
			t.Fatal("restored live credential", a)
		}
	}
}

func TestAttachedKeeperOutlivesModelWaitAndStopsWithOwner(t *testing.T) {
	s := fixture(t)
	id := create(t, s, bug())
	n, e := s.Claim(id, "", "attached", "", "")
	if e != nil {
		t.Fatal(e)
	}
	owner := exec.Command("sleep", "60")
	if e = owner.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { owner.Process.Kill(); owner.Wait() }()
	identity, e := process.Identity(owner.Process.Pid)
	if e != nil {
		t.Fatal(e)
	}
	n, e = s.BindLeaseOwner(id, n.Attempt.ID, n.Attempt.Token, owner.Process.Pid, identity)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Resume(id, "", true); e == nil || !strings.Contains(e.Error(), "OLD_OWNER_ALIVE") {
		t.Fatal("live owner fence bypassed", e)
	}
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		done <- s.WatchLease(ctx, id, n.Attempt.ID, n.Attempt.Token, owner.Process.Pid, identity, 10*time.Millisecond)
	}()
	// No model/tool-driven heartbeat: the separate keeper must renew on its own.
	task, _ := s.Get(id)
	task.Attempts[0].Lease = time.Now().Add(time.Second)
	b, _ := json.Marshal(task)
	s.Store.DB.Exec("UPDATE tasks SET body=? WHERE id=?", string(b), id)
	deadline := time.After(2 * time.Second)
	for {
		task, e = s.Get(id)
		if e != nil {
			t.Fatal(e)
		}
		if time.Until(task.Attempts[0].Lease) > 25*time.Minute {
			break
		}
		select {
		case <-deadline:
			t.Fatal("independent keeper did not renew")
		case <-time.After(10 * time.Millisecond):
		}
	}
	owner.Process.Kill()
	owner.Wait()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("keeper survived owner exit")
	}
	task, _ = s.Get(id)
	if !task.Paused || task.Attempts[0].Status != "interrupted" {
		t.Fatal(task)
	}
	if _, e = s.Resume(id, "", false); e == nil {
		t.Fatal("unknown old tools were silently unlocked")
	}
}

func TestKeepLeaseCancelsOnFenceLossWithoutRevivingToken(t *testing.T) {
	s := fixture(t)
	id := create(t, s, bug())
	n, e := s.Claim(id, "", "managed", "", "")
	if e != nil {
		t.Fatal(e)
	}
	lost := make(chan struct{})
	k, e := s.KeepLease(context.Background(), id, n.Attempt, 10*time.Millisecond, func() { close(lost) })
	if e != nil {
		t.Fatal(e)
	}
	defer k.Stop()
	task, _ := s.Get(id)
	task.Attempts[0].Lease = time.Now().Add(-time.Second)
	b, _ := json.Marshal(task)
	s.Store.DB.Exec("UPDATE tasks SET body=? WHERE id=?", string(b), id)
	select {
	case <-lost:
	case <-time.After(2 * time.Second):
		t.Fatal("lost lease did not cancel worker")
	}
	if k.Err() == nil {
		t.Fatal("renewal error swallowed")
	}
	if _, e = s.Heartbeat(id, n.Attempt.ID, n.Attempt.Token); e == nil {
		t.Fatal("expired token revived")
	}
}

func TestTestingTaskTypesKeepTheirNames(t *testing.T) {
	for _, typ := range []string{"test", "qa", "verify"} {
		t.Run(typ, func(t *testing.T) {
			s := fixture(t)
			d := bug()
			d.Type = typ
			id := create(t, s, d)
			n, _ := s.Next(id)
			if n.Task.Definition.Type != typ || n.Task.Plan.Preset != "fast" {
				t.Fatal(n)
			}
			d.Risk = []string{"compatibility"}
			n, e := s.Edit(id, d, "risk found")
			if e != nil || n.Task.Definition.Type != typ || n.Task.Plan.Preset != "strict" {
				t.Fatal(n, e)
			}
		})
	}
}

func copyCheckout(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(src, path)
		if e != nil {
			return e
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0755)
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestTaskCheckpointTravelsWithCheckout(t *testing.T) {
	s := fixture(t)
	id := atVerify(t, s, bug())
	os.Remove(filepath.Join(s.Root, "result.txt"))
	s.Resume(id, "execute", true)
	os.WriteFile(filepath.Join(s.Root, "result.txt"), []byte("fixed"), 0644)
	step(t, s, id, "repairer", "result.txt")
	before, _ := s.Get(id)
	path := filepath.Join(s.Root, ".ael/records/"+id+".json")
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatalf("no portable checkpoint: %v", e)
	}
	for _, secret := range []string{"lease-", s.Root, "\"token\"", "\"pid\"", "\"session\""} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("runtime credential leaked: %s", secret)
		}
	}
	clone := copyCheckout(t, s.Root)
	other, _, e := bootstrap.Open(clone)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Store.Close()
	n, e := other.Next(id)
	if e != nil {
		t.Fatal(e)
	}
	if !n.Task.Created.Equal(before.Created) || n.Task.Step != "execute" || n.Action != "wait_confirmation" || n.Task.History == nil || len(n.Task.History.Attempts) != len(before.Attempts) {
		t.Fatalf("checkpoint lost: %+v", n)
	}
	if len(n.Task.Attempts) != 0 || len(n.Task.Approvals) != 0 || len(n.Task.Evidence) != 0 {
		t.Fatal("live authority restored from Git")
	}
	if _, e := other.Claim(id, "", "new", "", ""); e == nil {
		t.Fatal("unreviewed clone resumed execution")
	}
}
