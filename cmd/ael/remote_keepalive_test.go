package main

import (
	"ael/internal/process"
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func startFixtureKeeper(t *testing.T, script string, timeout time.Duration, keep bool) (int, error) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	watcher := process.Command(context.Background(), "sh", "-c", script)
	err = launchLeaseWatcher(watcher, reader, writer, timeout, keep)
	pid := 0
	if watcher.Process != nil {
		pid = watcher.Process.Pid
		t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL); _, _ = watcher.Process.Wait() })
	}
	return pid, err
}

func TestRemoteKeeperSlowFirstRenewalKeepsRunning(t *testing.T) {
	// A keeper whose first heartbeat is slow must not be killed: it is the
	// only thing renewing the already accepted claim.
	pid, err := startFixtureKeeper(t, `sleep 1; printf '\001' >&3; sleep 30`, 100*time.Millisecond, true)
	if !errors.Is(err, errKeeperUnconfirmed) {
		t.Fatalf("slow keeper result: %v", err)
	}
	if syscall.Kill(pid, 0) != nil {
		t.Fatal("slow remote keeper was stopped")
	}
	if pid, err = startFixtureKeeper(t, `printf '\001' >&3; sleep 30`, 5*time.Second, true); err != nil || pid == 0 {
		t.Fatalf("ready keeper failed: %v", err)
	}
	// Local attached mode keeps its strict start contract.
	pid, err = startFixtureKeeper(t, `sleep 30`, 100*time.Millisecond, false)
	if err == nil || !strings.Contains(err.Error(), "KEEPER_START_TIMEOUT") {
		t.Fatalf("strict keeper timeout: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		t.Fatal("strict keeper survived its start timeout")
	}
}

func TestKeeperWarningKeepsClaimResult(t *testing.T) {
	result := remoteOutput{"lease_file": "/private/claim.json"}
	keeperWarning(result, "/private/claim.json", "123", errKeeperUnconfirmed)
	keeperWarning(result, "/private/claim.json", "123", errors.New("EOF"))
	warnings, _ := result["warnings"].([]any)
	if len(warnings) != 2 || !strings.HasPrefix(warnings[0].(string), "KEEPER_UNCONFIRMED") || !strings.HasPrefix(warnings[1].(string), "KEEPER_FAILED") || !strings.Contains(warnings[1].(string), "runner keepalive --lease-file /private/claim.json --owner-pid 123") {
		t.Fatalf("unexpected keeper warnings: %v", warnings)
	}
}
