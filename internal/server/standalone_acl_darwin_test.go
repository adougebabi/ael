//go:build darwin

package server

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStandaloneRejectsDarwinACLsOnDataAndDatabase(t *testing.T) {
	for _, target := range []string{"directory", "database"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			if target == "database" {
				s, err := OpenStandalone(dir)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.Close(); err != nil {
					t.Fatal(err)
				}
			}
			path, rule := dir, "everyone allow search,add_file,delete_child"
			if target == "database" {
				path, rule = filepath.Join(dir, "runtime.db"), "everyone allow read,write"
			}
			if output, err := exec.Command("/bin/chmod", "+a", rule, path).CombinedOutput(); err != nil {
				t.Fatalf("could not construct isolated ACL fixture: %v %s", err, output)
			}
			s, err := OpenStandalone(dir)
			if err == nil {
				_ = s.Close()
				t.Fatal("server accepted an unchecked access control list")
			}
		})
	}
}
