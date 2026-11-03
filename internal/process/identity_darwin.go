//go:build darwin

package process

import (
	"fmt"
	"golang.org/x/sys/unix"
)

// Identity binds renewal to this process incarnation without depending on ps.
func Identity(pid int) (string, error) {
	if pid <= 1 {
		return "", fmt.Errorf("OWNER_PROCESS_MISSING: %d", pid)
	}
	p, e := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if e != nil {
		return "", fmt.Errorf("OWNER_IDENTITY_UNAVAILABLE: %w", e)
	}
	if int(p.Proc.P_pid) != pid || p.Proc.P_stat == 5 || p.Proc.P_starttime.Sec == 0 {
		return "", fmt.Errorf("OWNER_PROCESS_MISSING: %d", pid)
	}
	return fmt.Sprintf("%d:%d", p.Proc.P_starttime.Sec, p.Proc.P_starttime.Usec), nil
}
