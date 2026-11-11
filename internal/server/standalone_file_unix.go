//go:build darwin || linux

package server

import (
	"errors"

	"golang.org/x/sys/unix"
)

// Existing SQLite files must not refer to another directory through a hard
// link, symlink, or separately mounted filesystem.
func standaloneSafeDatabaseFile(path string) error {
	var info unix.Stat_t
	if err := unix.Lstat(path, &info); errors.Is(err, unix.ENOENT) {
		return nil
	} else if err != nil {
		return errors.New("SERVER_DATABASE_INVALID")
	}
	if info.Mode&unix.S_IFMT != unix.S_IFREG || info.Nlink != 1 || info.Mode&0077 != 0 {
		return errors.New("SERVER_DATABASE_INVALID")
	}
	if err := standaloneNoACL(path); err != nil {
		return err
	}
	return standaloneLocalFilesystem(path)
}
