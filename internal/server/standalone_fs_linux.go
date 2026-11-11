//go:build linux

package server

import (
	"ael/internal/securityfs"
	"errors"

	"golang.org/x/sys/unix"
)

// Unknown and FUSE filesystems are refused rather than assuming a network
// mount is safe for SQLite. Common local Linux filesystems remain supported.
func standaloneLocalFilesystem(path string) error {
	var info unix.Statfs_t
	if err := unix.Statfs(path, &info); err != nil {
		return errors.New("SERVER_LOCAL_FILESYSTEM_REQUIRED")
	}
	switch uint64(info.Type) & 0xffffffff {
	case unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC, unix.F2FS_SUPER_MAGIC, unix.TMPFS_MAGIC, unix.OVERLAYFS_SUPER_MAGIC, unix.RAMFS_MAGIC:
		return nil
	default:
		return errors.New("SERVER_LOCAL_FILESYSTEM_REQUIRED")
	}
}

// Linux POSIX ACL effective permissions are reflected in mode bits, which
// are checked separately for the directory and each SQLite file.
func standaloneNoACL(path string) error { return securityfs.NoACL(path) }
