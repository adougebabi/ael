//go:build darwin || linux

package core

import (
	"ael/internal/store"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// One OS lock per parent serializes application and abandonment across CLI
// processes. The kernel releases it if a process exits or crashes.
func (s *Service) lockPlan(parent string) (func(), error) {
	dir := filepath.Join(s.Store.Dir, "plan-locks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, store.Hash([]byte(parent))+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = unix.Flock(int(file.Fd()), unix.LOCK_EX)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return func() { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN); _ = file.Close() }, nil
}
