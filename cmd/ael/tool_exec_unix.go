//go:build darwin || linux

package main

import (
	"os"
	"syscall"
)

func execToolDelegation(delegation toolDelegation) error {
	environment := append(os.Environ(), "AEL_TOOL_DELEGATED="+delegation.hash, "AEL_SESSION_ID="+delegation.session)
	return syscall.Exec(delegation.executable, delegation.arguments, environment)
}
