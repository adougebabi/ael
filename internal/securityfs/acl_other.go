//go:build !darwin && !linux

package securityfs

import "errors"

func NoACL(string) error { return errors.New("ACL_CHECK_UNSUPPORTED") }

func SingleLinkRegular(string) error { return errors.New("PRIVATE_FILE_CHECK_UNSUPPORTED") }
