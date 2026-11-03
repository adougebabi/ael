//go:build darwin || linux

// Package process confines each managed command to its own process group.
package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		e := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(e, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return e
	}
	return cmd
}

// Alive treats permission-denied as unknown/alive; only ESRCH proves absence.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func GroupAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH)
}
func StopGroup(pid int) error {
	if pid <= 0 {
		return nil
	}
	e := syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(e, syscall.ESRCH) {
		return nil
	}
	return e
}
