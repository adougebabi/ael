//go:build !darwin && !linux

package main

import "ael/internal/core"

func execToolDelegation(delegation toolDelegation) error {
	return core.Error("CLIENT_PLATFORM_UNSUPPORTED", "pinned client execution supports macOS and Linux")
}
