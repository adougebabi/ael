package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the actual CLI process, including stdout, stderr and exit status.
// AEL is initialized only inside t.TempDir, with a separate runtime directory.
func TestActualCLITextAndJSON(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ael")
	build := exec.Command("go", "build", "-mod=readonly", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	root := t.TempDir()
	runtime := t.TempDir()
	t.Setenv("AEL_DATA_DIR", runtime)
	invoke := func(argv ...string) (string, string, error) {
		t.Helper()
		cmd := exec.Command(binary, argv...)
		cmd.Dir = root
		cmd.Env = os.Environ()
		var out, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &stderr
		err := cmd.Run()
		return out.String(), stderr.String(), err
	}
	for _, cmd := range []string{"help", "version"} {
		out, stderr, err := invoke(cmd)
		if err != nil || stderr != "" || strings.HasPrefix(strings.TrimSpace(out), "{") || out == "" {
			t.Fatalf("%s text: out=%q stderr=%q err=%v", cmd, out, stderr, err)
		}
		if strings.Contains(out, "\x1b[") {
			t.Fatalf("%s contains ANSI: %q", cmd, out)
		}
	}
	out, stderr, err := invoke("--json", "version")
	var version map[string]any
	if err != nil || stderr != "" || json.Unmarshal([]byte(out), &version) != nil || version["version"] != bootstrap.Version {
		t.Fatalf("leading --json swallowed version: out=%q stderr=%q err=%v", out, stderr, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".ael")); !os.IsNotExist(err) {
		t.Fatal("help/version initialized the empty project", err)
	}
	if _, err := bootstrap.Init(root, false, false); err != nil {
		t.Fatal(err)
	}
	svc, _, err := bootstrap.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	n, err := svc.Create(core.Definition{ID: "中文-task-full-id-1234567890", Title: "中文验收任务", Parent: "parent-id", Type: "bug", Goal: "verified", Acceptance: []string{"实际结果"}})
	if err != nil {
		t.Fatal(err)
	}
	id := n.Task.Definition.ID
	n, err = svc.Claim(id, "", "fixture-executor", "", "")
	if err != nil {
		t.Fatal(err)
	}
	a := n.Attempt
	_, err = svc.Submit(core.Submission{Task: id, Attempt: a.ID, Token: a.Token, Key: a.ID, Result: core.StepResult{Status: "blocked", Summary: "环境未准备", Blocker: &core.Blocker{Reason: "测试环境缺失", Recovery: "安装隔离测试工具"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{{"doctor"}, {"task", "list"}, {"task", "show", "--task", id}} {
		out, stderr, err = invoke(argv...)
		if err != nil || stderr != "" || out == "" || strings.HasPrefix(strings.TrimSpace(out), "{") || strings.Contains(out, "\x1b[") {
			t.Fatalf("%v text: out=%q stderr=%q err=%v", argv, out, stderr, err)
		}
	}
	text, _, err := invoke("task", "show", "--task", id, "--verbose", "--no-color")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{id, "中文验收任务", "parent-id", "测试环境缺失", "安装隔离测试工具", "wait_environment"} {
		if !strings.Contains(text, want) {
			t.Fatalf("task text lacks %q: %s", want, text)
		}
	}
	out, stderr, err = invoke("task", "--json", "show", "--task", id)
	var next core.Next
	if err != nil || stderr != "" || json.Unmarshal([]byte(out), &next) != nil || next.Task == nil || next.Task.Definition.ID != id || next.Action != "wait_environment" || next.Task.Blocker.Reason != "测试环境缺失" {
		t.Fatalf("task JSON: out=%q stderr=%q err=%v", out, stderr, err)
	}
	if strings.Contains(out, "\x1b[") {
		t.Fatal("JSON contains ANSI")
	}
	planText, _, err := invoke("task", "plan", "--task", id)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range append([]string{next.Task.Plan.Preset, next.Task.Plan.Reason}, next.Task.Plan.Steps...) {
		if want != "" && !strings.Contains(planText, want) {
			t.Fatalf("task plan omits %q: %s", want, planText)
		}
	}
	out, stderr, err = invoke("not-a-command")
	if err == nil || out != "" || !strings.Contains(stderr, "UNKNOWN_COMMAND") || strings.HasPrefix(strings.TrimSpace(stderr), "{") {
		t.Fatalf("text error: out=%q stderr=%q err=%v", out, stderr, err)
	}
	out, stderr, err = invoke("not-a-command", "--json")
	var failure map[string]any
	if err == nil || out != "" || json.Unmarshal([]byte(stderr), &failure) != nil || failure["code"] != "UNKNOWN_COMMAND" {
		t.Fatalf("JSON error: out=%q stderr=%q err=%v", out, stderr, err)
	}
}

func TestTextClaimShowsLeaseKeeper(t *testing.T) {
	var out bytes.Buffer
	n := core.Next{Task: &core.Task{Definition: core.Definition{ID: "keeper-task", Title: "keeper"}}, KeeperPID: 424242}
	if err := writeOutput(&out, n, false, false); err != nil || !strings.Contains(out.String(), "424242") {
		t.Fatalf("keeper PID omitted: %q %v", out.String(), err)
	}
}
