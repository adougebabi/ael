package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type runnerRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f runnerRoundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestRunnerLeaseGuardChecksWallTimeAfterSleep(t *testing.T) {
	start := time.Now()
	guard := newRunnerLeaseGuard(context.Background(), start.Add(time.Minute))
	// Model a paused monotonic clock while wall time advances beyond the lease.
	if err := guard.checkAt(start.Add(time.Second), start.Add(2*time.Minute)); err == nil {
		t.Fatal("wall-clock expiry was ignored while the monotonic clock lagged")
	}
}

func TestRunnerLeaseTransportCancelsBlockedRequestAtDeadline(t *testing.T) {
	guard := newRunnerLeaseGuard(context.Background(), time.Now().Add(120*time.Millisecond))
	started := make(chan struct{})
	base := runnerRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	request, err := http.NewRequest(http.MethodPost, "http://fixture.invalid/write", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := (runnerLeaseTransport{guard: guard, base: base}).RoundTrip(request)
		done <- err
	}()
	<-started
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("blocked request completed without a deadline error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked request outlived the lease deadline")
	}
}

func TestRunnerLeaseTransportKeepsRequestAliveAfterRenewal(t *testing.T) {
	guard := newRunnerLeaseGuard(context.Background(), time.Now().Add(160*time.Millisecond))
	started, release := make(chan struct{}), make(chan struct{})
	base := runnerRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		close(started)
		select {
		case <-request.Context().Done():
			return nil, request.Context().Err()
		case <-release:
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("fixture"))}, nil
		}
	})
	request, err := http.NewRequest(http.MethodPost, "http://fixture.invalid/write", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		response, err := (runnerLeaseTransport{guard: guard, base: base}).RoundTrip(request)
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		done <- err
	}()
	<-started
	if err = guard.extend(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	select {
	case err = <-done:
		t.Fatalf("request ended at the old deadline after renewal: %v", err)
	default:
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRunnerLeaseGuardCancelsActualInFlightRequest(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/meta" {
			_, _ = fmt.Fprint(w, `{"mode":"server","protocol":1,"capabilities":["remote_runners"]}`)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		close(started)
		<-r.Context().Done()
		close(stopped)
	})
	_, config, project, binding := runnerRecoveryFixture(t, handler)
	guard := newRunnerLeaseGuard(context.Background(), time.Now().Add(250*time.Millisecond))
	done := make(chan error, 1)
	go func() {
		_, err := runnerGuardedCall(config, project, binding, guard, http.MethodPost, "/runners/runner/progress", map[string]string{"fixture": "yes"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("fixture request did not start")
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "LEASE_LOST") {
			t.Fatalf("in-flight request did not report expiry: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request was not cancelled")
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("fixture server did not observe cancellation")
	}
}

func TestRunnerLeaseGuardKeepsActualRequestAfterRenewal(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/meta" {
			_, _ = fmt.Fprint(w, `{"mode":"server","protocol":1,"capabilities":["remote_runners"]}`)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		close(started)
		select {
		case <-release:
			_, _ = fmt.Fprint(w, `{}`)
		case <-r.Context().Done():
		}
	})
	_, config, project, binding := runnerRecoveryFixture(t, handler)
	guard := newRunnerLeaseGuard(context.Background(), time.Now().Add(300*time.Millisecond))
	done := make(chan error, 1)
	go func() {
		_, err := runnerGuardedCall(config, project, binding, guard, http.MethodPost, "/runners/runner/progress", map[string]string{"fixture": "yes"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("fixture request did not start")
	}
	if err := guard.extend(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("renewed in-flight request failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("renewed in-flight request did not finish")
	}
}

func TestRunnerLeaseWatcherRenewsBeforeShortDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	guard := newRunnerLeaseGuard(ctx, time.Now().Add(300*time.Millisecond))
	called := make(chan struct{}, 1)
	done := guard.watch(time.Second, cancel, func() (time.Time, error) {
		called <- struct{}{}
		return time.Now().Add(time.Second), nil
	})
	select {
	case <-called:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("renewal waited for the fixed interval instead of the remaining lease")
	}
	cancel()
	<-done
}

func TestRunnerLeaseDeadlineStopsWhileHeartbeatIsStuck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	guard := newRunnerLeaseGuard(ctx, time.Now().Add(200*time.Millisecond))
	release := make(chan struct{})
	done := guard.watch(20*time.Millisecond, cancel, func() (time.Time, error) {
		<-release
		return time.Now().Add(time.Second), nil
	})
	defer close(release)
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("stuck heartbeat allowed the lease to outlive its deadline")
	}
	if err := <-done; err == nil || !strings.Contains(err.Error(), "LEASE_LOST") {
		t.Fatalf("deadline did not report loss: %v", err)
	}
	if err := guard.check(); err == nil {
		t.Fatal("an operation was allowed after the lease deadline")
	}
}

func TestRunnerLeaseGuardStopsMutationAfterSlowCapabilityCheck(t *testing.T) {
	var posts atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/meta" {
			time.Sleep(120 * time.Millisecond)
			_, _ = fmt.Fprint(w, `{"mode":"server","protocol":1,"capabilities":["remote_runners"]}`)
			return
		}
		posts.Add(1)
		_, _ = fmt.Fprint(w, `{}`)
	})
	cfg, config, project, binding := runnerRecoveryFixture(t, handler)
	guard := newRunnerLeaseGuard(context.Background(), time.Now().Add(60*time.Millisecond))
	lease := remoteRunnerLease{TaskID: "task", AttemptID: "AT-fixture", Token: "fixture-lease-token", Fence: 4, RunnerID: cfg.RunnerID}
	_, err := runnerLeaseCallGuarded(cfg, config, project, binding, lease, "progress", map[string]any{"summary": "fixture", "next": "fixture"}, guard)
	if err == nil || !strings.Contains(err.Error(), "LEASE_LOST") || posts.Load() != 0 {
		t.Fatalf("mutation started after its deadline: error=%v posts=%d", err, posts.Load())
	}
}

func TestRunnerLeaseGuardStopsArtifactChunksAfterDeadline(t *testing.T) {
	var chunks, finalized atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/meta":
			_, _ = fmt.Fprint(w, `{"mode":"server","protocol":1,"capabilities":["artifacts"]}`)
		case strings.HasSuffix(r.URL.Path, "/artifacts/uploads"):
			_, _ = fmt.Fprint(w, `{"upload_id":"fixture-upload","status":"open","received":0}`)
		case strings.HasSuffix(r.URL.Path, "/chunks"):
			chunks.Add(1)
			time.Sleep(700 * time.Millisecond)
			_, _ = fmt.Fprintf(w, `{"received":%d}`, remoteArtifactChunk)
		case strings.HasSuffix(r.URL.Path, "/finalize"):
			finalized.Add(1)
			_, _ = fmt.Fprint(w, `{}`)
		default:
			http.Error(w, `{"code":"UNEXPECTED"}`, http.StatusNotFound)
		}
	})
	cfg, config, project, binding := runnerRecoveryFixture(t, handler)
	guard := newRunnerLeaseGuard(context.Background(), time.Now().Add(500*time.Millisecond))
	lease := &runnerUploadLease{RunnerID: cfg.RunnerID, Token: "fixture-lease-token", Fence: 4, guard: guard}
	a := args{Flags: map[string]string{"task": "task", "attempt": "AT-fixture", "expected-revision": "revision", "operation-id": "upload", "idempotency-key": "upload", "mime": "text/plain"}}
	_, err := remoteUploadBytes(a, config, project, binding, "fixture.txt", make([]byte, remoteArtifactChunk+1), lease)
	if err == nil || !strings.Contains(err.Error(), "LEASE_LOST") || chunks.Load() != 1 || finalized.Load() != 0 {
		t.Fatalf("upload continued after lease deadline: error=%v chunks=%d finalized=%d", err, chunks.Load(), finalized.Load())
	}
}

func TestRunnerLeaseRenewalExtendsOnlyLiveAuthority(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	guard := newRunnerLeaseGuard(ctx, time.Now().Add(160*time.Millisecond))
	first := make(chan struct{}, 1)
	release := make(chan struct{})
	calls := 0
	done := guard.watch(20*time.Millisecond, cancel, func() (time.Time, error) {
		calls++
		if calls == 1 {
			first <- struct{}{}
			return time.Now().Add(400 * time.Millisecond), nil
		}
		<-release
		return time.Now().Add(time.Second), nil
	})
	defer close(release)
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("heartbeat was not attempted")
	}
	time.Sleep(200 * time.Millisecond)
	if err := guard.check(); err != nil {
		t.Fatalf("valid renewal did not extend the deadline: %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("renewed deadline did not cancel the blocked next heartbeat")
	}
	if err := <-done; err == nil {
		t.Fatal("expired renewed lease was reported as active")
	}
}

func TestRunnerHeartbeatUsesLocalElapsedTimeForLeaseDeadline(t *testing.T) {
	issued := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	leaseDuration := 2 * time.Second
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/meta" {
			_, _ = fmt.Fprint(w, `{"mode":"server","protocol":1,"capabilities":["remote_runners"]}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/heartbeat") {
			time.Sleep(100 * time.Millisecond)
			_ = json.NewEncoder(w).Encode(map[string]any{"attempt_id": "AT-fixture", "fence": 4, "lease_issued_at": issued, "lease_until": issued.Add(leaseDuration)})
			return
		}
		http.Error(w, `{"code":"UNEXPECTED"}`, http.StatusNotFound)
	})
	cfg, config, project, binding := runnerRecoveryFixture(t, handler)
	lease := remoteRunnerLease{TaskID: "task", AttemptID: "AT-fixture", Token: "fixture-lease-token", Fence: 4, RunnerID: cfg.RunnerID}
	start := time.Now()
	deadline, err := runnerHeartbeatDeadline(cfg, config, project, binding, lease)
	if err != nil {
		t.Fatal(err)
	}
	if deadline.Before(start.Add(leaseDuration-20*time.Millisecond)) || deadline.After(start.Add(leaseDuration+200*time.Millisecond)) {
		t.Fatalf("deadline did not use the server interval and local request start: %v", deadline.Sub(start))
	}
	if remaining := time.Until(deadline); remaining >= leaseDuration || remaining <= 0 {
		t.Fatalf("network time was not deducted from the lease: %v", remaining)
	}
}

func TestRunnerHeartbeatRejectsMissingOrExpiredLeaseInterval(t *testing.T) {
	for _, mode := range []string{"missing", "expired", "wrong-fence"} {
		t.Run(mode, func(t *testing.T) {
			issued := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v1/meta" {
					_, _ = fmt.Fprint(w, `{"mode":"server","protocol":1,"capabilities":["remote_runners"]}`)
					return
				}
				result := map[string]any{"attempt_id": "AT-fixture", "fence": 4}
				if mode == "missing" {
					result["lease_until"] = issued.Add(time.Second)
				}
				if mode != "missing" {
					duration := time.Second
					if mode == "expired" {
						duration = 20 * time.Millisecond
						time.Sleep(80 * time.Millisecond)
					}
					result["lease_issued_at"], result["lease_until"] = issued, issued.Add(duration)
				}
				if mode == "wrong-fence" {
					result["fence"] = 5
				}
				_ = json.NewEncoder(w).Encode(result)
			})
			cfg, config, project, binding := runnerRecoveryFixture(t, handler)
			lease := remoteRunnerLease{TaskID: "task", AttemptID: "AT-fixture", Token: "fixture-lease-token", Fence: 4, RunnerID: cfg.RunnerID}
			if _, err := runnerHeartbeatDeadline(cfg, config, project, binding, lease); err == nil {
				t.Fatalf("%s lease response was accepted", mode)
			} else if mode == "missing" && !strings.Contains(err.Error(), "REMOTE_PROTOCOL_UNSUPPORTED") {
				t.Fatalf("old-server response lacked an upgrade hint: %v", err)
			}
		})
	}
}
