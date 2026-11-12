package knowledge

import (
	"ael/internal/core"
	"ael/internal/store"
	"net/url"
	"path"
	"slices"
	"strings"
	"unicode"
)

type BrowseQuery struct {
	Text, View, Scope string
	Limit, Offset     int
}
type Summary struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Kind        string   `json:"kind"`
	Status      string   `json:"status"`
	Description string   `json:"description"`
	Excerpt     bool     `json:"excerpt"`
	Paths       []string `json:"paths"`
	OccurredAt  string   `json:"occurred_at,omitempty"`
	Hash        string   `json:"hash,omitempty"`
	TaskCount   int      `json:"task_count"`
}
type ScopeCounts struct {
	All          int            `json:"all"`
	Unclassified int            `json:"unclassified"`
	Paths        map[string]int `json:"paths"`
}
type BrowsePage struct {
	Items       []Summary      `json:"items"`
	Counts      map[string]int `json:"counts"`
	ViewCounts  map[string]int `json:"view_counts"`
	ScopeCounts ScopeCounts    `json:"scope_counts"`
	Total       int            `json:"total"`
	Scopes      []string       `json:"scopes"`
	Limit       int            `json:"limit"`
	Offset      int            `json:"offset"`
}

func Title(r Record) string {
	source := strings.SplitN(r.Source, "#sha256=", 2)[0]
	if r.Title != "" && r.Title != path.Base(r.Path) && r.Title != r.Path && r.Title != source && r.Title != path.Base(source) {
		return r.Title
	}
	for _, line := range strings.Split(r.Body, "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	if source != "" {
		return path.Base(source)
	}
	if r.Title != "" {
		return r.Title
	}
	return path.Base(r.Path)
}
func Excerpt(body string) string {
	// Clearly labelled source excerpt, never a generated conclusion.
	out := []rune(strings.Join(strings.FieldsFunc(body, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune("#`*|>", r) }), " "))
	if len(out) > 160 {
		return string(out[:160]) + "…"
	}
	return string(out)
}
func summary(r Record) Summary {
	d := r.Summary
	if d == "" {
		d = Excerpt(r.Body)
	}
	return Summary{ID: r.ID, Title: Title(r), Kind: r.Kind, Status: r.Status, Description: d, Excerpt: r.Summary == "", Paths: r.Paths, OccurredAt: r.OccurredAt, Hash: r.Hash, TaskCount: len(r.Tasks)}
}
func view(r Record) string {
	if r.Kind == "decision" {
		return "decisions"
	}
	if r.Kind == "spec" && (r.Status == "current" || r.Status == "accepted" || r.Status == "conflict" || r.Status == "proposed") {
		return "current"
	}
	return "history"
}
func Browse(root string, q BrowseQuery) (BrowsePage, error) {
	all, err := ReadAll(root)
	if err != nil {
		return BrowsePage{}, err
	}
	return browseRecords(all, q), nil
}

// BrowseWithUsage includes explicit and task-supplied references in one
// projection; list responses still contain no document bodies.
func BrowseWithUsage(s *core.Service, q BrowseQuery) (BrowsePage, error) {
	all, err := WithUsage(s, "")
	if err != nil {
		return BrowsePage{}, err
	}
	return browseRecords(all, q), nil
}

func browseRecords(all []Record, q BrowseQuery) BrowsePage {
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = 25
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	if q.View == "" {
		q.View = "current"
	}
	p := BrowsePage{Items: []Summary{}, Counts: map[string]int{"current": 0, "decisions": 0, "history": 0}, ViewCounts: map[string]int{"current": 0, "decisions": 0, "history": 0}, ScopeCounts: ScopeCounts{Paths: map[string]int{}}, Scopes: []string{}, Limit: q.Limit, Offset: q.Offset}
	scopes := map[string]bool{}
	for _, r := range all {
		p.ViewCounts[view(r)]++
		for _, x := range r.Paths {
			scopes[x] = true
		}
		if q.View == "all" || view(r) == q.View {
			p.ScopeCounts.All++
			if len(r.Paths) == 0 {
				p.ScopeCounts.Unclassified++
			}
			seen := map[string]bool{}
			for _, x := range r.Paths {
				if !seen[x] {
					p.ScopeCounts.Paths[x]++
					seen[x] = true
				}
			}
		}
		if q.Text != "" && !strings.Contains(strings.ToLower(r.Title+" "+r.ID+" "+r.Path+" "+r.Source+" "+r.Summary+" "+r.Body+" "+strings.Join(r.Paths, " ")), strings.ToLower(q.Text)) {
			continue
		}
		if q.Scope == "unclassified" && len(r.Paths) > 0 {
			continue
		}
		if q.Scope != "" && q.Scope != "unclassified" && !slices.Contains(r.Paths, q.Scope) {
			continue
		}
		p.Counts[view(r)]++
		if q.View != "all" && view(r) != q.View {
			continue
		}
		p.Total++
		if p.Total > q.Offset && len(p.Items) < q.Limit {
			p.Items = append(p.Items, summary(r))
		}
	}
	for x := range scopes {
		p.Scopes = append(p.Scopes, x)
	}
	slices.Sort(p.Scopes)
	return p
}
func Read(s *core.Service, id, version string) (Record, error) {
	if version != "" {
		r, e := ReadVersion(s, id, version)
		r.Title = Title(r)
		return r, e
	}
	all, e := WithUsage(s, "")
	if e != nil {
		return Record{}, e
	}
	for _, r := range all {
		if r.ID == id {
			r.Title = Title(r)
			return r, nil
		}
	}
	return Record{}, core.Error("KNOWLEDGE_NOT_FOUND", id)
}
func ResolveLink(s *core.Service, id, href string) (any, error) {
	r, e := Read(s, id, "")
	if e != nil {
		return nil, e
	}
	u, e := url.Parse(href)
	if e != nil || u.IsAbs() || u.Host != "" {
		return nil, core.Error("KNOWLEDGE_LINK_UNAVAILABLE", "only indexed project documents can be opened here")
	}
	target := path.Clean(path.Join(path.Dir(r.Path), u.Path))
	if u.Path == "" {
		target = r.Path
	}
	if _, e = store.SafePath(s.Root, target); e != nil {
		return nil, e
	}
	all, e := ReadAll(s.Root)
	if e != nil {
		return nil, e
	}
	for _, x := range all {
		if x.Path == target {
			return map[string]string{"id": x.ID, "fragment": u.Fragment}, nil
		}
	}
	return nil, core.Error("KNOWLEDGE_LINK_UNAVAILABLE", "target is not an accessible indexed document")
}

// BrowseRecords reuses the library projection for authenticated central snapshots.
func BrowseRecords(records []Record, query BrowseQuery) BrowsePage {
	return browseRecords(records, query)
}
