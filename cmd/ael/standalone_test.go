package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStandaloneBootstrapWithoutCheckout(t *testing.T) {
	working := t.TempDir()
	data := filepath.Join(t.TempDir(), "server-data")
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(working); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.WriteString("fixture-admin-password\n"); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	oldInput := os.Stdin
	os.Stdin = reader
	defer func() { os.Stdin = oldInput; reader.Close() }()
	value, err := run([]string{"server", "bootstrap", "--data-dir", data, "--username", "owner", "--password-stdin", "--json"})
	if err != nil || value == nil {
		t.Fatalf("bootstrap failed: %+v %v", value, err)
	}
	if _, err = os.Stat(filepath.Join(working, ".ael")); !os.IsNotExist(err) {
		t.Fatal("server bootstrap initialized a checkout")
	}
	if _, err = os.Stat(filepath.Join(data, "runtime.db")); err != nil {
		t.Fatal("server database not in explicit data directory", err)
	}
	if _, err = run([]string{"server", "start", "--data-dir", data, "--addr", "0.0.0.0:0"}); err == nil || !strings.Contains(err.Error(), "LOOPBACK_REQUIRED") {
		t.Fatalf("server allowed a non-loopback bind: %v", err)
	}
	if _, err = run([]string{"server", "start", "--data-dir", data, "--project", working}); err == nil {
		t.Fatal("standalone server accepted a project checkout argument")
	}
}
