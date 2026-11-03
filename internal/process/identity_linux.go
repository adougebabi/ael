//go:build linux

package process

import (
	"fmt"
	"os"
	"strings"
)

func Identity(pid int) (string, error) {
	if pid <= 1 || !Alive(pid) {
		return "", fmt.Errorf("OWNER_PROCESS_MISSING: %d", pid)
	}
	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if e != nil {
		return "", e
	}
	at := strings.LastIndexByte(string(b), ')')
	if at < 0 {
		return "", fmt.Errorf("OWNER_IDENTITY_UNAVAILABLE")
	}
	fields := strings.Fields(string(b[at+1:]))
	if len(fields) <= 19 || fields[0] == "Z" {
		return "", fmt.Errorf("OWNER_PROCESS_MISSING")
	}
	return fields[19], nil
}
