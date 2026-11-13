package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"context"
	"io"
	"net/http"
	"sync"
	"time"
)

type runnerLeaseTransport struct {
	guard *runnerLeaseGuard
	base  http.RoundTripper
}

type runnerLeaseResponseBody struct {
	io.ReadCloser
	cleanup func()
}

func (body *runnerLeaseResponseBody) Close() error {
	err := body.ReadCloser.Close()
	body.cleanup()
	return err
}

func (t runnerLeaseTransport) CloseIdleConnections() {
	if transport, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		transport.CloseIdleConnections()
	}
}

func (t runnerLeaseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := t.guard.check(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(request.Context())
	stop := context.AfterFunc(t.guard.ctx, cancel)
	cleanup := func() { stop(); cancel() }
	// Follow renewals while the request is in flight. Go's monotonic timer can
	// pause during sleep, so also recheck wall time after wake.
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			remaining, changed := t.guard.deadlineState()
			timer := time.NewTimer(max(remaining, 0))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-changed:
				timer.Stop()
				continue
			case <-timer.C:
				if t.guard.check() != nil {
					cancel()
					return
				}
			case <-ticker.C:
				timer.Stop()
				if t.guard.check() != nil {
					cancel()
					return
				}
			}
		}
	}()
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	response, err := base.RoundTrip(request.Clone(ctx))
	if err != nil {
		cleanup()
		return nil, err
	}
	if response.Body == nil {
		cleanup()
	} else {
		response.Body = &runnerLeaseResponseBody{ReadCloser: response.Body, cleanup: cleanup}
	}
	return response, nil
}

func runnerGuardedCall(config, project string, binding remote.Binding, guard *runnerLeaseGuard, method, path string, body any) (map[string]any, error) {
	if guard == nil {
		return nil, core.Error("LEASE_LOST", "a local lease guard is required")
	}
	profile, err := remote.LoadProfile(config, project, binding.Profile)
	if err != nil {
		return nil, err
	}
	client, err := remote.NewHTTPClient(profile)
	if err != nil {
		return nil, err
	}
	client.Transport = runnerLeaseTransport{guard: guard, base: client.Transport}
	if profile.CertificateAuthority != "" {
		defer client.CloseIdleConnections()
	}
	result, err := remote.Call(config, project, binding, method, path, body, client)
	if err != nil {
		if leaseErr := guard.check(); leaseErr != nil {
			return nil, leaseErr
		}
	}
	return result, err
}

type runnerLeaseGuard struct {
	ctx      context.Context
	mu       sync.RWMutex
	deadline time.Time
	changed  chan struct{}
}

func newRunnerLeaseGuard(ctx context.Context, deadline time.Time) *runnerLeaseGuard {
	return &runnerLeaseGuard{ctx: ctx, deadline: deadline, changed: make(chan struct{})}
}

func (g *runnerLeaseGuard) check() error {
	now := time.Now()
	return g.checkAt(now, now)
}

func (g *runnerLeaseGuard) checkAt(monotonicNow, wallNow time.Time) error {
	g.mu.RLock()
	deadline := g.deadline
	g.mu.RUnlock()
	if g.ctx.Err() != nil || deadline.IsZero() || !monotonicNow.Before(deadline) || !wallNow.Round(0).Before(deadline.Round(0)) {
		return core.Error("LEASE_LOST", "the conservative local lease deadline has passed")
	}
	return nil
}

func (g *runnerLeaseGuard) extend(deadline time.Time) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	if g.ctx.Err() != nil || !now.Before(g.deadline) || !now.Round(0).Before(g.deadline.Round(0)) || !now.Before(deadline) || !now.Round(0).Before(deadline.Round(0)) {
		return core.Error("LEASE_LOST", "a late heartbeat cannot restart expired authority")
	}
	g.deadline = deadline
	close(g.changed)
	g.changed = make(chan struct{})
	return nil
}

func (g *runnerLeaseGuard) deadlineState() (time.Duration, <-chan struct{}) {
	g.mu.RLock()
	deadline := g.deadline
	changed := g.changed
	g.mu.RUnlock()
	now := time.Now()
	return min(deadline.Sub(now), deadline.Round(0).Sub(now.Round(0))), changed
}

func (g *runnerLeaseGuard) remaining() time.Duration {
	remaining, _ := g.deadlineState()
	return remaining
}

func runnerResetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(max(duration, 0))
}

// watch enforces the local deadline even if the current network request hangs.
// At most one renewal is in flight; a late response cannot revive an expired
// lease. Cancellation does not claim that detached child processes stopped.
func (g *runnerLeaseGuard) watch(interval time.Duration, cancel context.CancelFunc, renew func() (time.Time, error)) <-chan error {
	done := make(chan error, 1)
	type renewal struct {
		deadline time.Time
		err      error
	}
	responses := make(chan renewal, 1)
	go func() {
		remaining := g.remaining()
		timer := time.NewTimer(max(remaining, 0))
		defer timer.Stop()
		renewTimer := time.NewTimer(max(min(interval, remaining/3), 0))
		defer renewTimer.Stop()
		wallTicker := time.NewTicker(time.Second)
		defer wallTicker.Stop()
		inFlight := false
		fail := func(err error) {
			cancel()
			done <- err
		}
		for {
			select {
			case <-g.ctx.Done():
				done <- nil
				return
			case <-timer.C:
				fail(core.Error("LEASE_LOST", "the conservative local lease deadline has passed"))
				return
			case <-wallTicker.C:
				if err := g.check(); err != nil {
					fail(err)
					return
				}
			case <-renewTimer.C:
				if err := g.check(); err != nil {
					fail(err)
					return
				}
				if !inFlight {
					inFlight = true
					go func() {
						deadline, err := renew()
						responses <- renewal{deadline: deadline, err: err}
					}()
				}
			case response := <-responses:
				inFlight = false
				if response.err != nil {
					fail(response.err)
					return
				}
				if err := g.extend(response.deadline); err != nil {
					fail(err)
					return
				}
				remaining := g.remaining()
				runnerResetTimer(timer, remaining)
				runnerResetTimer(renewTimer, min(interval, remaining/3))
			}
		}
	}()
	return done
}
