package core

import (
	"path"
	"regexp"
	"strings"
)

// HumanEvidenceRequirements applies equally to existing and new definitions.
// kind=ui alone is not an explicit agreement to upload a screenshot.
func HumanEvidenceRequirements(t Task, c Check) (attachment, screenshot bool) {
	text := strings.ToLower(strings.Join(c.Covers, "\n") + "\n" + t.Definition.OriginalRequest)
	for _, phrase := range []string{"无需截图", "不需要截图", "不要求截图", "no screenshot required"} {
		text = strings.ReplaceAll(text, phrase, "")
	}

	// A request involving a screenshot feature or logo.png is not a demand
	// for screenshot evidence. Only explicit evidence language applies here.
	screenshot = regexp.MustCompile(`(?i)(必须|验收证据|截图证据|需提供|需提交|请提供|请提交|附上|附带|with|required|attach).{0,20}(截图|对照图|screenshot)|(提供|提交).{0,10}(截图|对照图|screenshot).{0,10}(作为|用于|验证|验收|证据)`).MatchString(text)
	attachment = screenshot || len(c.Artifacts) > 0
	for _, word := range []string{"专项报告", "验收报告文件", "附上报告", "上传报告"} {
		attachment = attachment || strings.Contains(text, word)
	}
	for _, ref := range c.Artifacts {
		ext := strings.ToLower(path.Ext(ref))
		screenshot = screenshot || ext == ".png" || ext == ".jpg" || ext == ".jpeg"
	}
	return
}

func humanObservation(reason, recovery, result string, refs []string) (string, string, string) {
	if strings.TrimSpace(reason) == "" && result == "pass" {
		reason = "人工已查看所示候选，确认本项验收通过"
	}
	if strings.TrimSpace(recovery) == "" && result != "pass" {
		if result == "fail" {
			recovery = "由执行者核对所报告的问题并修复后重新验收"
		} else {
			recovery = "所述无法验证的条件解除后，继续本项人工验收"
		}
	}
	mode := "direct_observation"
	if len(refs) > 0 {
		mode = "observation_with_attachments"
	}
	return reason, recovery, mode
}

func validateHumanArtifacts(c Check, refs []string, names map[string]string, result string) error {
	if result == "blocked" {
		return nil
	}
	for _, required := range c.Artifacts {
		found := false
		for _, ref := range refs {
			name := ref
			if names[ref] != "" {
				name = names[ref]
			}
			if ref == required || path.Base(strings.TrimPrefix(name, "runtime:")) == path.Base(strings.TrimPrefix(required, "runtime:")) {
				found = true
			}
		}
		if !found {
			return Error("HUMAN_EVIDENCE_REQUIRED", "missing explicitly required attachment: "+required)
		}
	}
	return nil
}
