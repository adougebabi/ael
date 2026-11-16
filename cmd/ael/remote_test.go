package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteBindingStopsLocalFallbackBeforeOpen(t *testing.T) {
	project := t.TempDir()
	config := filepath.Join(t.TempDir(), "config")
	runtime := t.TempDir()
	t.Setenv("AEL_DATA_DIR", runtime)
	if err := os.Mkdir(filepath.Join(project, ".ael"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".ael", "remote.json"), []byte(`{"schema":1,"profile":"central","project_id":"shared"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(project, ".ael", "records"), 0700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(project, ".ael", "records", "stale.json")
	if err := os.WriteFile(stale, []byte(`{"status":"completed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range [][]string{{"task", "list"}, {"task", "create", "--file", "missing.json"}, {"init"}, {"repair"}, {"storage", "restore", "--from", "missing"}} {
		args := append(append([]string{}, command...), "--project", project, "--config-dir", config)
		_, err := run(args)
		remoteFailure := err != nil && (strings.Contains(err.Error(), "REMOTE_") || (command[0] == "init" && strings.Contains(err.Error(), "LOCAL_PROJECT_EXISTS")))
		if !remoteFailure {
			t.Fatalf("remote command fell through to local authority: %v %v", command, err)
		}
	}
	if entries, err := os.ReadDir(runtime); err != nil || len(entries) != 0 {
		t.Fatalf("remote command created local runtime: %v %v", entries, err)
	}
	if data, err := os.ReadFile(stale); err != nil || string(data) != `{"status":"completed"}` {
		t.Fatalf("local records changed: %s %v", data, err)
	}
}

func TestRemoteTextAndJSONClearlyMarkOfflineCache(t *testing.T) {
	value := remoteOutput{"mode": "remote", "online": false, "authoritative": false, "cached_at": "2026-01-01T00:00:00Z", "task": map[string]any{"status": "completed"}}
	var text bytes.Buffer
	if err := writeOutput(&text, value, false, false); err != nil || !strings.Contains(text.String(), "离线") || !strings.Contains(text.String(), "不能作为当前完成证明") || !strings.Contains(text.String(), "2026-01-01") {
		t.Fatalf("offline text was ambiguous: %s %v", text.String(), err)
	}
	var machine bytes.Buffer
	if err := writeOutput(&machine, value, true, false); err != nil || !strings.Contains(machine.String(), `"online":false`) || !strings.Contains(machine.String(), `"authoritative":false`) {
		t.Fatalf("offline JSON was ambiguous: %s %v", machine.String(), err)
	}
}

func TestRemoteTextClearlyMarksBlockedProjection(t *testing.T) {
	value := remoteOutput{"mode": "remote", "online": true, "authoritative": false, "projection_status": "blocked", "observed_at": "2026-01-01T00:00:00Z"}
	var text bytes.Buffer
	if err := writeOutput(&text, value, false, false); err != nil || !strings.Contains(text.String(), "投影待修复") || !strings.Contains(text.String(), "不能作为完整回执") {
		t.Fatalf("blocked projection text was ambiguous: %s %v", text.String(), err)
	}
}

func TestRemoteProfileCLIReadsSecretFromStdin(t *testing.T) {
	project := t.TempDir()
	config := filepath.Join(t.TempDir(), "config")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.WriteString("ael_fixture_token\n"); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	old := os.Stdin
	os.Stdin = reader
	defer func() { os.Stdin = old; reader.Close() }()
	value, err := run([]string{"server", "profile", "add", "--name", "central", "--url", "http://127.0.0.1:7332", "--token-stdin", "--project", project, "--config-dir", config, "--json"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(value)
	if strings.Contains(string(raw), "ael_fixture_token") {
		t.Fatal("profile creation returned the secret")
	}
	if _, err = os.Stat(filepath.Join(config, "profiles", "central.json")); err != nil {
		t.Fatal(err)
	}
}
