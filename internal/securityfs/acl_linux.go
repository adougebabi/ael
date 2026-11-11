//go:build linux

package securityfs

// Linux POSIX ACL effective permissions are reflected in mode bits; callers
// separately check directory and file permissions.
func NoACL(string) error { return nil }
