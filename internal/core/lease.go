package core

import (
	"ael/internal/process"
	"ael/internal/store"
	"context"
	"fmt"
	"sync"
	"time"
)

// LeaseKeeper renews independently of model output and tool calls. A failed
// renewal cancels the managed process; it never revives an expired fence.
type LeaseKeeper struct {
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	err    error
}

func (s *Service) BindLeaseOwner(id, attempt, token string, owner int, identity string) (Next, error) {
	current, e := process.Identity(owner)
	if e != nil {
		return Next{}, e
	}
	if current != identity {
		return Next{}, Error("OWNER_CHANGED", "")
	}
	n, e := s.mutate(id, "lease_owner_bound", func(tx *store.Tx, t *Task) error {
		a, e := findAttempt(t, attempt, token)
		if e != nil {
			return e
		}
		a.OwnerPID = owner
		a.OwnerIdentity = identity
		return nil
	})
	if e == nil {
		for i := range n.Task.Attempts {
			if n.Task.Attempts[i].ID == attempt {
				copy := n.Task.Attempts[i]
				n.Attempt = &copy
				break
			}
		}
	}
	return n, e
}

func (k *LeaseKeeper) Err() error { k.mu.Lock(); defer k.mu.Unlock(); return k.err }
func (k *LeaseKeeper) Stop()      { k.cancel(); <-k.done }
func (s *Service) KeepLease(ctx context.Context, id string, a *Attempt, interval time.Duration, onLost func()) (*LeaseKeeper, error) {
	if interval <= 0 {
		return nil, Error("INVALID_HEARTBEAT_INTERVAL", "")
	}
	if _, e := s.Heartbeat(id, a.ID, a.Token); e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(ctx)
	k := &LeaseKeeper{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(k.done)
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if _, e := s.Heartbeat(id, a.ID, a.Token); e != nil {
					k.mu.Lock()
					k.err = e
					k.mu.Unlock()
					if onLost != nil {
						onLost()
					}
					return
				}
			}
		}
	}()
	return k, nil
}

// Interrupt retains the workspace fence until the previous writer is checked.
func (s *Service) Interrupt(id, attempt, token, reason string) (Next, error) {
	return s.mutate(id, "interrupted", func(tx *store.Tx, t *Task) error {
		for i := range t.Attempts {
			a := &t.Attempts[i]
			if a.ID != attempt {
				continue
			}
			if a.Token != token || a.Status != "running" {
				return Error("STALE_TOKEN", attempt)
			}
			a.Status = "interrupted"
			a.Reason = reason
			t.Paused = true
			t.Failure = reason
			return nil
		}
		return Error("ATTEMPT_MISSING", attempt)
	})
}

// WatchLease is the attached-mode keeper. It is explicitly bound to the Agent's
// long-lived process; owner loss stops renewal and retains the writer fence.
func (s *Service) WatchLease(ctx context.Context, id, attempt, token string, owner int, identity string, interval time.Duration) error {
	if interval <= 0 {
		return Error("INVALID_HEARTBEAT_INTERVAL", "")
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		t, e := s.Get(id)
		if e != nil {
			_, _ = s.Interrupt(id, attempt, token, "attached keeper could not read task; workspace remains fenced")
			return e
		}
		var a *Attempt
		for i := range t.Attempts {
			if t.Attempts[i].ID == attempt {
				a = &t.Attempts[i]
				break
			}
		}
		if a == nil {
			return Error("ATTEMPT_MISSING", attempt)
		}
		if a.Status != "running" {
			return nil
		}
		if a.Token != token {
			return Error("STALE_TOKEN", attempt)
		}
		current, e := process.Identity(owner)
		if e != nil || current != identity {
			_, e = s.Interrupt(id, attempt, token, "attached owner stopped; checkpoint retained; verify old tools stopped before resume")
			return e
		}
		if _, e = s.Heartbeat(id, attempt, token); e != nil {
			_, _ = s.Interrupt(id, attempt, token, "attached lease renewal failed; stop old tools before recovery: "+e.Error())
			return fmt.Errorf("LEASE_RENEWAL_FAILED: %w", e)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
