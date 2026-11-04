package knowledge

import (
	"ael/internal/core"
	"ael/internal/store"
	"encoding/json"
	"os"
	"path/filepath"
)

type snapshot struct {
	Path string `json:"path"`
	Raw  []byte `json:"raw"`
}

func versionPath(s *core.Service, id, version string) (string, error) {
	if len(version) != 64 {
		return "", core.Error("INVALID_KNOWLEDGE_VERSION", version)
	}
	for _, c := range version {
		if !((c >= 'a' && c <= 'f') || (c >= '0' && c <= '9')) {
			return "", core.Error("INVALID_KNOWLEDGE_VERSION", version)
		}
	}
	return store.SafePath(s.Store.Dir, filepath.Join("knowledge-versions", store.Hash([]byte(id)), version+".json"))
}
func captureVersion(s *core.Service, r Record) error {
	p, e := store.SafePath(s.Root, r.Path)
	if e != nil {
		return e
	}
	raw, e := os.ReadFile(p)
	if e != nil {
		return e
	}
	if store.Hash(raw) != r.Hash {
		return core.Error("KNOWLEDGE_SOURCE_CHANGED", r.Path)
	}
	dest, e := versionPath(s, r.ID, r.Hash)
	if e != nil {
		return e
	}
	b, e := json.Marshal(snapshot{Path: r.Path, Raw: raw})
	if e != nil {
		return e
	}
	if old, e := os.ReadFile(dest); e == nil {
		if string(old) != string(b) {
			return core.Error("KNOWLEDGE_VERSION_CONFLICT", r.ID)
		}
		return nil
	} else if !os.IsNotExist(e) {
		return e
	}
	return store.Atomic(dest, b, 0600)
}
func ReadVersion(s *core.Service, id, version string) (Record, error) {
	p, e := versionPath(s, id, version)
	if e != nil {
		return Record{}, e
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return Record{}, core.Error("KNOWLEDGE_VERSION_UNAVAILABLE", id+"@"+version)
	}
	var snap snapshot
	if e = json.Unmarshal(b, &snap); e != nil {
		return Record{}, e
	}
	if store.Hash(snap.Raw) != version {
		return Record{}, core.Error("KNOWLEDGE_VERSION_CORRUPT", id)
	}
	r, e := parseSource(snap.Path, snap.Raw)
	if e != nil {
		return r, e
	}
	if r.ID != id {
		return r, core.Error("KNOWLEDGE_VERSION_CORRUPT", id)
	}
	r.Reason = "immutable supplied-version snapshot; current authoritative file may have changed"
	return r, nil
}
