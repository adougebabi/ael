package core

// TaskRelations lists definition links exactly as stored. Managed membership is
// separate so a legacy definition link cannot become an integration requirement.
type TaskRelation struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	Step           string `json:"step"`
	Historical     bool   `json:"historical"`
	DefinitionLink bool   `json:"definition_parent"`
	ManagedMember  bool   `json:"managed_member"`
	Feedback       bool   `json:"feedback"`
}

type TaskRelationPage struct {
	Items []TaskRelation `json:"items"`
	Total int            `json:"total"`
}

func (s *Service) TaskRelations(id string, limit, offset int) (TaskRelationPage, error) {
	page := TaskRelationPage{Items: []TaskRelation{}}
	const relation = `WITH relations AS (
		SELECT id, COALESCE(json_extract(body,'$.definition.title'),id) AS title, status, step,
			COALESCE(json_extract(body,'$.historical'),0) AS historical,
			COALESCE(json_extract(body,'$.definition.parent')=?,0) AS definition_parent,
			COALESCE(json_extract(body,'$.epic_membership.parent')=?,0) AS managed_member,
			COALESCE(json_extract(body,'$.definition.feedback_of')=?,0) AS feedback
		FROM tasks WHERE id<>?
	), related AS (
		SELECT * FROM relations WHERE feedback OR (definition_parent AND NOT managed_member)
	)`
	rows, err := s.Store.DB.Query(relation+`
		SELECT id,title,status,step,historical,definition_parent,managed_member,feedback,0
		FROM (SELECT * FROM related ORDER BY id LIMIT ? OFFSET ?)
		UNION ALL SELECT '','','','',0,0,0,0,COUNT(*) FROM related`, id, id, id, id, limit, offset)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var item TaskRelation
		var total int
		if err := rows.Scan(&item.ID, &item.Title, &item.Status, &item.Step, &item.Historical, &item.DefinitionLink, &item.ManagedMember, &item.Feedback, &total); err != nil {
			return page, err
		}
		if item.ID == "" {
			page.Total = total
		} else {
			page.Items = append(page.Items, item)
		}
	}
	return page, rows.Err()
}
