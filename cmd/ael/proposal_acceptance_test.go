package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPlanPreviewCLIEntryPoint(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	if _, err := bootstrap.Init(root, false, false); err != nil {
		t.Fatal(err)
	}
	s, _, err := bootstrap.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create(core.Definition{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"完整交付"}}); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PreparePlan("epic", core.PlanInput{Request: "实现一个可验收的结果"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	proposal := filepath.Join(t.TempDir(), "proposal.json")
	raw := `{"version":1,"id":"sample-plan","parent":"epic","parent_revision":1,"request":"实现一个可验收的结果","user_items":[],"leaves":[{"key":"one","source_ids":[],"definition":{"title":"交付结果","type":"bug","goal":"verified","scope":["fixture.txt"],"non_goals":["不发布"],"acceptance":["结果正确"],"checks":[{"id":"proof","kind":"functional","method":"command","command":["sh","-c","true"],"covers":["结果正确"],"required":true}]},"deliverables":["fixture.txt"],"basis":["用户要求"],"capabilities":["开发"],"depends_on":[],"order":1,"required":true,"milestone":"第一阶段"}],"coverage":[{"acceptance":"完整交付","leaves":["one"],"integration":""}]}`
	raw = strings.Replace(raw, `"parent_revision":1`, `"parent_revision":1,"input_hash":`+strconv.Quote(prepared.InputHash), 1)
	if err = os.WriteFile(proposal, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = run([]string{"plan", "preview", "--project", root, "--file", proposal, "--json"}); err != nil {
		t.Fatalf("preview valid proposal: %v", err)
	}
	value, err := run([]string{"plan", "preview", "--project", root, "--file", proposal})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = writeOutput(&out, value, false, false); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"交付结果", "fixture.txt", "结果正确", "顺序"} {
		if !strings.Contains(out.String(), expected) {
			t.Fatalf("text preview omitted %q: %s", expected, out.String())
		}
	}
}

func TestPlanPrepareSchemaCoversExistingDefinition(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AEL_DATA_DIR", t.TempDir())
	if _, err := bootstrap.Init(root, false, false); err != nil {
		t.Fatal(err)
	}
	s, _, err := bootstrap.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []core.Definition{
		{ID: "epic", Title: "父", Type: "epic", Goal: "verified", Acceptance: []string{"整体"}},
		{ID: "leaf", Title: "已有", Type: "bug", Goal: "merged", Target: "main", Acceptance: []string{"结果"}, Legacy: map[string]any{"secret": "not-for-planner"}},
	} {
		if _, err = s.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	inputPath := filepath.Join(t.TempDir(), "input.json")
	if err = os.WriteFile(inputPath, []byte(`{"request":"保留现有任务","items":[{"id":"user-1","description":"已有","existing_task_id":"leaf"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	value, err := run([]string{"plan", "prepare", "--project", root, "--task", "epic", "--file", inputPath, "--json"})
	if err != nil {
		t.Fatal(err)
	}
	prepared, ok := value.(planPreparationOutput)
	if !ok || len(prepared.ExistingLeaves) != 1 || prepared.InputHash == "" {
		t.Fatalf("bad preparation: %+v", value)
	}
	var schema map[string]any
	if err = json.Unmarshal(prepared.Schema, &schema); err != nil {
		t.Fatal(err)
	}
	properties := schema["properties"].(map[string]any)
	if _, ok = properties["input_hash"]; !ok {
		t.Fatal("input binding missing from schema")
	}
	leafSchema := properties["leaves"].(map[string]any)["items"].(map[string]any)
	definitionSchema := leafSchema["properties"].(map[string]any)["definition"].(map[string]any)
	allowed := definitionSchema["properties"].(map[string]any)
	definition := prepared.ExistingLeaves[0].Definition
	for key := range definition {
		if _, ok = allowed[key]; !ok {
			t.Fatalf("existing definition field %q missing from schema", key)
		}
	}
	for _, key := range definitionSchema["required"].([]any) {
		if _, ok = definition[key.(string)]; !ok {
			t.Fatalf("required field %q absent from prepared definition", key)
		}
	}
	if definition["target"] != "main" || definition["legacy"] != nil {
		t.Fatalf("target lost or legacy leaked: %+v", definition)
	}
}
