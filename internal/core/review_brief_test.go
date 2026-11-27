package core_test

import (
	"ael/internal/core"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewBriefTellsReviewerWhatToCheckOnCurrentCandidate(t *testing.T) {
	s := fixture(t)
	id := create(t, s, core.Definition{ID: "mobile", Title: "手机端布局", Type: "improvement", Goal: "verified", Acceptance: []string{"手机屏宽视觉正确"}, Checks: []core.Check{
		{ID: "unit", Kind: "functional", Command: []string{"true"}, Covers: []string{"手机屏宽视觉正确"}, Required: true},
		{ID: "ui", Kind: "ui", Method: "human", Covers: []string{"手机屏宽视觉正确"}, Required: true},
	}})
	brief := core.ReviewBrief{Check: "ui", Summary: "确认 107 张手机屏宽截图的变化符合手机端设计稿", Changes: "合并手机端工作后，聊天页和图库在 390px 下改为单列。", Steps: []string{"打开 shots/mobile/ 下的截图，与 shots/approved/ 同名文件对照"}, Focus: []string{"底部输入栏不遮挡最后一条消息"}, Evidence: []string{"shots/mobile/chat.png"}}
	if _, err := s.RecordReviewBrief(id, brief, "agent"); err == nil || !strings.Contains(err.Error(), "REVIEW_BRIEF_INVALID") {
		t.Fatalf("brief accepted before any candidate existed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(s.Root, "shots/mobile"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Root, "shots/mobile/chat.png"), []byte("png"), 0600); err != nil {
		t.Fatal(err)
	}
	step(t, s, id, "agent")
	for n := step(t, s, id, "agent", "shots/mobile/chat.png"); n.Step != "verify"; n = step(t, s, id, "agent", "shots/mobile/chat.png") {
	}
	for name, change := range map[string]func(*core.ReviewBrief){
		"automated check":  func(b *core.ReviewBrief) { b.Check = "unit" },
		"missing summary":  func(b *core.ReviewBrief) { b.Summary = " " },
		"missing evidence": func(b *core.ReviewBrief) { b.Evidence = []string{"shots/mobile/absent.png"} },
		"outside project":  func(b *core.ReviewBrief) { b.Evidence = []string{"../secret.png"} },
	} {
		bad := brief
		change(&bad)
		if _, err := s.RecordReviewBrief(id, bad, "agent"); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	n, err := s.RecordReviewBrief(id, brief, "agent")
	if err != nil {
		t.Fatal(err)
	}
	summary, err := s.Summarize(n)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.ManualReviews) != 1 || summary.ManualReviews[0].Brief == nil || summary.ManualReviews[0].Brief.Summary != brief.Summary || summary.ManualReviews[0].Brief.Candidate != n.Task.Candidate || summary.ManualReviews[0].Brief.Actor != "agent" {
		t.Fatalf("reviewer does not see the brief: %+v", summary.ManualReviews)
	}
	if ev := core.CurrentEvidence(n.Task, n.Task.Definition.Checks[1]); ev != nil {
		t.Fatalf("a brief recorded a check result: %+v", ev)
	}
	stale := *n.Task
	stale.Candidate = "another-candidate"
	if core.CurrentReviewBrief(stale, "ui") != nil {
		t.Fatal("brief for an older candidate was presented as current")
	}
}
