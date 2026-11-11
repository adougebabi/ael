//go:build darwin

package remote

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestProfileRejectsMacACLOnCredentialDirectory(t *testing.T) {
	project := t.TempDir()
	config := filepath.Join(t.TempDir(), "config")
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/bin/chmod", "+a", "everyone allow search,add_file,delete_child", config).CombinedOutput(); err != nil {
		t.Fatalf("create isolated ACL: %v %s", err, out)
	}
	if err := SaveProfile(config, project, "central", "http://127.0.0.1:7332", "ael_synthetic"); err == nil {
		t.Fatal("credential directory with an unchecked ACL was accepted")
	}
	if _, err := os.Stat(filepath.Join(config, "profiles", "central.json")); !os.IsNotExist(err) {
		t.Fatal("credentials were written into an unsafe directory")
	}
}

func TestProfileRejectsMacACLOnCredentialFile(t *testing.T) {
	project := t.TempDir()
	config := filepath.Join(t.TempDir(), "config")
	if err := SaveProfile(config, project, "central", "http://127.0.0.1:7332", "ael_synthetic"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, "profiles", "central.json")
	if out, err := exec.Command("/bin/chmod", "+a", "everyone allow read", path).CombinedOutput(); err != nil {
		t.Fatalf("create isolated ACL: %v %s", err, out)
	}
	if _, err := LoadProfile(config, project, "central"); err == nil {
		t.Fatal("credential file with an unchecked ACL was accepted")
	}
}
