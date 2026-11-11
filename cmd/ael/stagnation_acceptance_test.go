package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFailureReviewCLIAndReadableStatus(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	if _, err := bootstrap.Init(root, false, false); err != nil {
		t.Fatal(err)
	}
	s, _, err := bootstrap.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create(core.Definition{ID: "stuck", Title: "隔离失败", Type: "bug", Goal: "verified", Acceptance: []string{"结果"}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		n, e := s.Claim("stuck", "", "fixture", "", "")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.Submit(core.Submission{Task: "stuck", Attempt: n.Attempt.ID, Token: n.Attempt.Token, Key: core.ID("cli-failure-"), Result: core.StepResult{Status: "fail", Summary: fmt.Sprintf("failure %d", i)}}); e != nil {
			t.Fatal(e)
		}
	}
	if err = os.WriteFile(filepath.Join(s.Store.Dir, "review.txt"), []byte("isolated review evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	value, err := run([]string{"task", "show", "--project", root, "--task", "stuck"})
	var out bytes.Buffer
	if err != nil || writeOutput(&out, value, false, false) != nil || !strings.Contains(out.String(), "同单元失败：3 次") || !strings.Contains(out.String(), "失败输入：") || !strings.Contains(out.String(), "task review-failure") {
		t.Fatalf("CLI omitted retained stop: %q %v", out.String(), err)
	}
	review := core.FailureReview{Actor: "fixture", Tried: "核对输入", Excluded: "排除缺失", Hypothesis: "配置不符", Next: "复核配置", Recovery: "配置已核实", Evidence: []string{"runtime:review.txt"}}
	raw, _ := json.Marshal(review)
	file := filepath.Join(t.TempDir(), "review.json")
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	value, err = run([]string{"task", "review-failure", "--project", root, "--task", "stuck", "--file", file, "--json"})
	if err != nil {
		t.Fatal(err)
	}
	n, ok := value.(core.Next)
	if !ok || n.Task.FailureCycles["define"].Count != 3 || n.Task.FailureCycles["define"].State != "reviewed" {
		t.Fatalf("review changed budget: %+v", value)
	}
	if _, err = run([]string{"resume", "--project", root, "--task", "stuck", "--json"}); err != nil {
		t.Fatalf("reviewed resume: %v", err)
	}
}
