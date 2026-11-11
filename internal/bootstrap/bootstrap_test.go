package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitDryRunIdempotentAndUserConflict(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	original := "# 用户规则\n保持这个内容。\n"
	os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(original), 0644)
	r, e := Init(root, true, false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(root, ".ael")); !os.IsNotExist(e) {
		t.Fatal("dry run wrote project")
	}
	r, e = Init(root, false, false)
	if e != nil {
		t.Fatal(e)
	}
	again, e := Init(root, false, false)
	if e != nil || r.Config.ProjectID != again.Config.ProjectID {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(string(b), original) || strings.Count(string(b), begin) != 1 {
		t.Fatal(string(b))
	}
	workflow, e := os.ReadFile(filepath.Join(root, ".agents/skills/ael-work/references/workflow.md"))
	if e != nil || !strings.Contains(string(workflow), "单次完成的代码任务") || !strings.Contains(string(workflow), "再提交实施结果生成候选") || !strings.Contains(string(workflow), "重新执行全部必要检查") {
		t.Fatal("single-task commit guidance was not installed", e)
	}
	skill := filepath.Join(root, ".agents/skills/ael-work/SKILL.md")
	os.WriteFile(skill, []byte("user edit"), 0644)
	if _, e = Init(root, false, true); e == nil {
		t.Fatal("overwrote edited skill")
	}
	b, _ = os.ReadFile(skill)
	if string(b) != "user edit" {
		t.Fatal("lost user edit")
	}
}

// The installed instructions are the routing mechanism used by the host agent.
// Check the actual initialized project, rather than only the source assets.
func TestInitInstallsRequestRoutingBeforeWorkflow(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	if _, err := Init(root, false, false); err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		path        string
		gate        string
		commands    string
		mustContain []string
	}{
		{
			path:     "AGENTS.md",
			gate:     "Before any AEL command",
			commands: "`./ael doctor`",
			mustContain: []string{
				"review-only", "even when saved to a file",
				"do not run AEL commands or create tasks", "project iteration, bug fix, or other engineering change",
				"If the request later becomes an authorized engineering change", "If the user explicitly requests an AEL command or an AEL task",
			},
		},
		{
			path:     ".agents/skills/ael-work/SKILL.md",
			gate:     "先判断用户请求",
			commands: "doctor 和 task list",
			mustContain: []string{
				"仅供审核", "即使保存为文件", "不运行 AEL 命令或创建任务",
				"项目迭代、修复或其他工程变更", "请求后续转为已授权的工程变更", "用户明确要求",
			},
		},
		{
			path: ".agents/skills/ael-work/references/workflow.md",
			mustContain: []string{
				"仅供审核", "即使保存为文件", "不运行 AEL 命令或创建任务",
				"项目迭代、修复或其他工程变更", "用户明确要求", "首次实际修改前",
			},
		},
	}
	for _, check := range checks {
		t.Run(check.path, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, check.path))
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			if check.gate != "" {
				gate, commands := strings.Index(text, check.gate), strings.Index(text, check.commands)
				if gate < 0 || commands < 0 || gate >= commands {
					t.Errorf("request routing must precede AEL checks: gate=%d commands=%d", gate, commands)
				}
			}
			for _, fragment := range check.mustContain {
				if !strings.Contains(text, fragment) {
					t.Errorf("installed instructions omit %q", fragment)
				}
			}
		})
	}
}

func TestShadowAndSymlink(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	os.Mkdir(filepath.Join(root, ".trellis"), 0755)
	r, e := Init(root, false, false)
	if e != nil || r.Config.Mode != "shadow" {
		t.Fatal(r, e)
	}
	s, _, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	s.Store.Close()
	other := t.TempDir()
	os.Symlink(other, filepath.Join(other, ".ael"))
	if _, e = Init(other, false, false); e == nil {
		t.Fatal("symlink write allowed")
	}
}

func TestRuntimeDataCannotEnterBusinessRepository(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", filepath.Join(root, "private-runtime"))
	if _, e := Init(root, false, false); e == nil {
		t.Fatal("runtime data allowed inside project")
	}
	if _, e := os.Stat(filepath.Join(root, "private-runtime")); !os.IsNotExist(e) {
		t.Fatal("runtime was created before rejecting")
	}
}
