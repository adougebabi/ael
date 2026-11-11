//go:build darwin

// Package securityfs provides filesystem checks shared by server and client
// credential storage. It does not grant or modify permissions.
package securityfs

import (
	"encoding/binary"
	"errors"
	"unsafe"

	"golang.org/x/sys/unix"
)

// NoACL conservatively rejects macOS ACLs, whose effective access may not be
// reflected in ordinary Unix mode bits.
func NoACL(path string) error {
	name, err := unix.BytePtrFromString(path)
	if err != nil {
		return errors.New("ACL_UNAVAILABLE")
	}
	attrs := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	var result [8192]byte
	_, _, errno := unix.Syscall6(unix.SYS_GETATTRLIST, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&result[0])), uintptr(len(result)), 0, 0)
	if errno != 0 {
		return errors.New("ACL_UNAVAILABLE")
	}
	length := binary.LittleEndian.Uint32(result[0:4])
	aclLength := binary.LittleEndian.Uint32(result[8:12])
	if length < 12 || length > uint32(len(result)) || aclLength != 0 {
		return errors.New("ACL_UNSUPPORTED")
	}
	return nil
}
