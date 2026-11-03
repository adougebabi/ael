package server

import (
	"ael/internal/migration"
	"encoding/json"
	"net/http"
)

func (h *Handler) migrations(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		runs, e := migration.List(h.Service)
		var preview any
		var raw string
		if err := h.Service.Store.DB.QueryRow("SELECT body FROM metadata WHERE key='migration-preview'").Scan(&raw); err == nil {
			_ = json.Unmarshal([]byte(raw), &preview)
		}
		reply(w, map[string]any{"runs": runs, "preview": preview}, e)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "METHOD", 405)
		return
	}
	var action struct {
		Action string `json:"action"`
		ID     string `json:"id"`
		Digest string `json:"approved_digest"`
	}
	if e := decode(r, &action); e != nil {
		reply(w, nil, e)
		return
	}
	switch action.Action {
	case "preview":
		inv, e := migration.Scan(h.Service.Root)
		if e != nil {
			reply(w, nil, e)
			return
		}
		p, e := migration.MakePlan(inv)
		if e != nil {
			reply(w, nil, e)
			return
		}
		b, _ := json.Marshal(p)
		_, e = h.Service.Store.DB.Exec("INSERT INTO metadata(key,body) VALUES('migration-preview',?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", string(b))
		reply(w, p, e)
	case "apply":
		var raw string
		e := h.Service.Store.DB.QueryRow("SELECT body FROM metadata WHERE key='migration-preview'").Scan(&raw)
		if e != nil {
			reply(w, nil, e)
			return
		}
		var p migration.Plan
		if e = json.Unmarshal([]byte(raw), &p); e != nil {
			reply(w, nil, e)
			return
		}
		v, e := migration.Apply(h.Service, p, nil, action.Digest)
		reply(w, v, e)
	case "verify":
		v, e := migration.Verify(h.Service, action.ID)
		reply(w, v, e)
	case "repair":
		v, e := migration.Repair(h.Service, action.ID)
		reply(w, v, e)
	case "rollback":
		v, e := migration.Rollback(h.Service, action.ID)
		reply(w, v, e)
	default:
		http.Error(w, "UNKNOWN_ACTION", 400)
	}
}
