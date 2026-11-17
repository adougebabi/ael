package core_test

import (
	"ael/internal/core"
	"ael/internal/store"
	"testing"
)

func TestTaskRelationsPreservesDefinitionParentAndFeedbackSeparately(t *testing.T) {
	s := fixture(t)
	create(t, s, core.Definition{ID: "old-parent", Title: "旧分组", Type: "bug", Goal: "verified", Acceptance: []string{"保留来源"}})
	create(t, s, core.Definition{ID: "managed", Title: "受管分组", Type: "epic", Goal: "verified", Acceptance: []string{"验证集成"}, Checks: []core.Check{{ID: "integration", Kind: "review", Method: "human", Covers: []string{"验证集成"}, Required: true}}})
	create(t, s, core.Definition{ID: "legacy-child", Title: "保留的子项", Type: "bug", Goal: "verified", Acceptance: []string{"保留来源"}})
	create(t, s, core.Definition{ID: "feedback", Title: "后续反馈", Type: "bug", Goal: "verified", Acceptance: []string{"记录反馈"}, FeedbackOf: "old-parent"})
	if _, err := s.LinkEpic("managed", "legacy-child", false, ""); err != nil {
		t.Fatal(err)
	}
	// Older records could store a definition-level link to an ordinary task.
	if err := s.Store.Write(func(tx *store.Tx) error {
		_, err := tx.Exec("UPDATE tasks SET body=json_set(body,'$.definition.parent','old-parent','$.historical',json('true')) WHERE id='legacy-child'")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	first, err := s.TaskRelations("old-parent", 1, 0)
	if err != nil || first.Total != 2 || len(first.Items) != 1 || first.Items[0].ID != "feedback" || !first.Items[0].Feedback || first.Items[0].DefinitionLink {
		t.Fatal(first, err)
	}
	second, err := s.TaskRelations("old-parent", 1, 1)
	if err != nil || second.Total != 2 || len(second.Items) != 1 || second.Items[0].ID != "legacy-child" || !second.Items[0].DefinitionLink || !second.Items[0].Historical || second.Items[0].ManagedMember {
		t.Fatal(second, err)
	}
	managed, err := s.TaskRelations("managed", 8, 0)
	if err != nil || managed.Total != 0 {
		t.Fatal(managed, err)
	}
}
