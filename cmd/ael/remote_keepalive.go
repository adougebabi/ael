package main

import (
	"ael/internal/core"
	"ael/internal/process"
	"ael/internal/remote"
	"context"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

// The host runs this companion alongside a long tool call. It only renews
// the original fenced attempt while the same owner process is alive.
func remoteKeepalive(a args, config, project string, binding remote.Binding) (any, error) {
	pid, err := strconv.Atoi(a.get("owner-pid", ""))
	if err != nil {
		return nil, core.Error("OWNER_IDENTITY_REQUIRED", "provide the persistent host or long-running tool PID")
	}
	owner, err := process.Identity(pid)
	if err != nil {
		return nil, err
	}
	expectedOwner := os.Getenv("AEL_REMOTE_KEEPER_OWNER")
	if expectedOwner != "" && owner != expectedOwner {
		return nil, core.Error("OWNER_CHANGED", "owner process changed before keeper startup")
	}
	path := a.get("lease-file", "")
	lease, receipt, err := readAgentLease(path)
	if err != nil {
		return nil, err
	}
	if runner := a.get("runner", lease.RunnerID); runner != lease.RunnerID {
		return nil, core.Error("AGENT_LEASE_INVALID", "runner differs from claim")
	}
	// The operation location is scoped to the server, checkout and identity,
	// so replaying a claim into another receipt cannot create two keepers.
	location, _, err := agentOperationLocation(config, project, binding, "keepalive", lease.RunnerID, lease.AttemptID)
	if err != nil {
		return nil, err
	}
	unlock, err := lockAgentOperation(location)
	if err != nil {
		return nil, err
	}
	defer unlock()
	endpoint := "/runners/" + url.PathEscape(lease.RunnerID) + "/heartbeat"
	var deadline time.Time
	for {
		if current, err := process.Identity(pid); err != nil || current != owner {
			return map[string]string{"status": "owner_stopped"}, nil
		}
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return nil, core.Error("LEASE_LOST", "last confirmed renewal interval elapsed")
		}
		op, err := runnerOperation("heartbeat")
		if err != nil {
			return nil, err
		}
		body := map[string]any{"operation_id": op, "idempotency_key": op}
		if err = applyAgentLease(body, lease, "heartbeat", lease.RunnerID); err != nil {
			return nil, err
		}
		started := time.Now()
		response, err := agentCommunicationCall(config, project, binding, http.MethodPost, endpoint, body)
		if err != nil {
			if agentTransient(err) && time.Until(deadline) > time.Second {
				time.Sleep(time.Second)
				continue
			}
			return nil, err
		}
		deadline, err = heartbeatDeadline(started, response, lease.AttemptID, lease.Fence)
		if err != nil {
			return nil, err
		}
		if err = updateAgentLease(path, receipt, response); err != nil {
			return nil, err
		}
		if expectedOwner != "" {
			ready := os.NewFile(3, "keeper-ready")
			if ready == nil {
				return nil, core.Error("KEEPER_HANDSHAKE_FAILED", "missing readiness pipe")
			}
			_, err := ready.Write([]byte{1})
			ready.Close()
			if err != nil {
				return nil, err
			}
			expectedOwner = ""
		}
		time.Sleep(min(time.Until(deadline)/3, 20*time.Second))
	}
}

func launchRemoteKeeper(a args, config, project, identity, pid string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return 0, err
	}
	defer reader.Close()
	defer writer.Close()
	command := []string{"runner", "keepalive", "--owner-pid", pid, "--lease-file", a.get("out", ""), "--config-dir", config, "--project", project}
	if session := a.get("session", ""); session != "" {
		command = append(command, "--session", session)
	}
	watcher := process.Command(context.Background(), exe, command...)
	watcher.Env = append(os.Environ(), "AEL_REMOTE_KEEPER_OWNER="+identity)
	if err = launchLeaseWatcher(watcher, reader, writer); err != nil {
		return 0, err
	}
	keeperPID := watcher.Process.Pid
	_ = watcher.Process.Release()
	return keeperPID, nil
}
