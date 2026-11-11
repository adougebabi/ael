//go:build darwin || linux

package server

import (
	"ael/internal/store"
	"errors"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// Authorization changes and protected requests share this cross-process
// fence, so a completed revocation cannot race a later committed write.
func (s *Standalone) lockAuthorization(exclusive bool) (func(), error) {
	path, err := store.SafePath(s.store.Dir, "authorization.lock")
	if err != nil {
		return nil, err
	}
	if err = standaloneSafeDatabaseFile(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = standaloneSafeDatabaseFile(path); err != nil {
		f.Close()
		return nil, err
	}
	mode := unix.LOCK_SH
	if exclusive {
		mode = unix.LOCK_EX
	}
	for {
		err = unix.Flock(int(f.Fd()), mode)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }) }, nil
}
