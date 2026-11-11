//go:build darwin

package server

import (
	"ael/internal/securityfs"
	"errors"

	"golang.org/x/sys/unix"
)

func standaloneLocalFilesystem(path string) error {
	var info unix.Statfs_t
	if err := unix.Statfs(path, &info); err != nil || info.Flags&unix.MNT_LOCAL == 0 {
		return errors.New("SERVER_LOCAL_FILESYSTEM_REQUIRED")
	}
	return nil
}

// macOS ACLs can grant access independently of mode bits. Reject any ACL
// rather than assuming a directory or database remains owner-only.
func standaloneNoACL(path string) error {
	return securityfs.NoACL(path)
}
