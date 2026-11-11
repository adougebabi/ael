//go:build darwin || linux

// Package projectmode serializes local and remote authority transitions using
// the checkout directory itself, without creating a lock file in the project.
package projectmode

import (
	"errors"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

func Lock(root string, exclusive bool) (func(), error) {
	f, err := os.Open(root)
	if err != nil {
		return nil, errors.New("PROJECT_MODE_UNAVAILABLE")
	}
	info, err := f.Stat()
	if err != nil || !info.IsDir() {
		f.Close()
		return nil, errors.New("PROJECT_MODE_UNAVAILABLE")
	}
	mode := unix.LOCK_SH | unix.LOCK_NB
	if exclusive {
		mode = unix.LOCK_EX | unix.LOCK_NB
	}
	for {
		err = unix.Flock(int(f.Fd()), mode)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, errors.New("PROJECT_MODE_BUSY: another local or remote authority transition is active")
	}
	var once sync.Once
	return func() { once.Do(func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }) }, nil
}
