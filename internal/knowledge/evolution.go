package knowledge

import (
	"ael/internal/core"
	"ael/internal/store"
	"database/sql"
	"encoding/json"
)

// Sync indexes content versions, never treating mtime as a new rule or import time as decision time.
func Sync(s *core.Service) error {
	all, e := ReadAll(s.Root)
	if e != nil {
		return e
	}
	for _, r := range all {
		if e = captureVersion(s, r); e != nil {
			return e
		}
	}
	return s.Store.Write(func(tx *store.Tx) error {
		old := map[string]string{}
		var raw string
		e := tx.Row("SELECT body FROM metadata WHERE key='knowledge-index'").Scan(&raw)
		if e != nil && e != sql.ErrNoRows {
			return e
		}
		if e == nil {
			if e = json.Unmarshal([]byte(raw), &old); e != nil {
				return e
			}
		}
		next := map[string]string{}
		for _, r := range all {
			next[r.ID] = r.Hash
			if old[r.ID] != r.Hash {
				if e = tx.Event("", "knowledge_observed", map[string]any{"id": r.ID, "title": r.Title, "kind": r.Kind, "status": r.Status, "hash": r.Hash, "source": r.Source, "path": r.Path, "tasks": r.Tasks, "superseded_by": r.SupersededBy, "time_semantics": "observation time; original decision date is not inferred"}); e != nil {
					return e
				}
			}
		}
		for id := range old {
			if _, ok := next[id]; !ok {
				if e = tx.Event("", "knowledge_removed", map[string]string{"id": id, "old_hash": old[id]}); e != nil {
					return e
				}
			}
		}
		b, _ := json.Marshal(next)
		_, e = tx.Exec("INSERT INTO metadata(key,body) VALUES('knowledge-index',?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", string(b))
		return e
	})
}
