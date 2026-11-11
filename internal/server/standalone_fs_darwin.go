//go:build darwin

package server

import (
	"encoding/binary"
	"errors"
	"unsafe"

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
	name, err := unix.BytePtrFromString(path)
	if err != nil {
		return errors.New("SERVER_ACL_UNAVAILABLE")
	}
	attrs := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	var result [8192]byte
	_, _, errno := unix.Syscall6(unix.SYS_GETATTRLIST, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&result[0])), uintptr(len(result)), 0, 0)
	if errno != 0 {
		return errors.New("SERVER_ACL_UNAVAILABLE")
	}
	length := binary.LittleEndian.Uint32(result[0:4])
	aclLength := binary.LittleEndian.Uint32(result[8:12])
	if length < 12 || length > uint32(len(result)) || aclLength != 0 {
		return errors.New("SERVER_ACL_UNSUPPORTED")
	}
	return nil
}
