//go:build darwin || linux

package securityfs

import (
	"errors"

	"golang.org/x/sys/unix"
)

func SingleLinkRegular(path string) error {
	var info unix.Stat_t
	if err := unix.Lstat(path, &info); err != nil || info.Mode&unix.S_IFMT != unix.S_IFREG || info.Nlink != 1 {
		return errors.New("PRIVATE_FILE_INVALID")
	}
	return nil
}
