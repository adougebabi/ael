package server

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/knowledge"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

//go:embed ui/* ui/assets/*
var ui embed.FS

type Handler struct {
	Service *core.Service
	Token   string
	Host    string
	Extra   func(http.ResponseWriter, *http.Request) bool
}

func New(s *core.Service, host string) *Handler { return &Handler{s, core.ID("session-"), host, nil} }
func reply(w http.ResponseWriter, v any, e error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if e != nil {
		w.WriteHeader(409)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": e.Error(), "code": strings.SplitN(e.Error(), ":", 2)[0]})
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}
func decode(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("INVALID_JSON: trailing content")
	}
	return nil
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'")
	if !h.acceptsHost(r.Host) {
		http.Error(w, "HOST_REJECTED", 403)
		return
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		http.Error(w, "ORIGIN_REJECTED", 403)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, e := url.Parse(origin)
		if e != nil || u.Host != r.Host || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			http.Error(w, "ORIGIN_REJECTED", 403)
			return
		}
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if r.URL.Path == "/api/session" && r.Method == "GET" {
			reply(w, map[string]string{"token": h.Token}, nil)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+h.Token {
			http.Error(w, "SESSION_REQUIRED", 401)
			return
		}
		if h.Extra != nil && h.Extra(w, r) {
			return
		}
		switch r.URL.Path {
		case "/api/migrations":
			h.migrations(w, r)
		case "/api/migration-view":
			h.migrationView(w, r)
		case "/api/project-status":
			h.projectStatus(w, r)
		case "/api/executor-probe":
			h.executorProbe(w, r)
		case "/api/sources":
			health := knowledge.Status(h.Service.Root)
			limit, offset := bounds(r)
			total := len(health.Sources)
			sources := []knowledge.Source{}
			if offset < total {
				sources = health.Sources[offset:min(offset+limit, total)]
			}
			health.Sources = nil
			reply(w, map[string]any{"health": health, "items": sources, "total": total, "limit": limit, "offset": offset}, nil)
		case "/api/timeline":
			// Diagnostics are a read-only view; opening them must not manufacture
			// new observations or alter the apparent project history.
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			v, e := h.Service.Timeline(limit, offset)
			reply(w, v, e)
		case "/api/knowledge-page":
			q := r.URL.Query()
			limit, _ := strconv.Atoi(q.Get("limit"))
			offset, _ := strconv.Atoi(q.Get("offset"))
			v, e := knowledge.Browse(h.Service.Root, knowledge.BrowseQuery{Text: q.Get("q"), View: q.Get("view"), Scope: q.Get("scope"), Limit: limit, Offset: offset})
			reply(w, v, e)
		case "/api/knowledge-record":
			h.knowledgeRecord(w, r)
		case "/api/knowledge-link":
			v, e := knowledge.ResolveLink(h.Service, r.URL.Query().Get("id"), r.URL.Query().Get("href"))
			reply(w, v, e)
		case "/api/evolution":
			q := r.URL.Query()
			limit, _ := strconv.Atoi(q.Get("limit"))
			offset, _ := strconv.Atoi(q.Get("offset"))
			v, e := knowledge.Changes(h.Service, knowledge.BrowseQuery{Text: q.Get("q"), Scope: q.Get("scope"), Limit: limit, Offset: offset}, q.Get("kind"), q.Get("from"), q.Get("to"))
			if q.Get("id") != "" && e == nil {
				// Stable object lookup is independent of the currently visible page.
				v, e = knowledge.Changes(h.Service, knowledge.BrowseQuery{Limit: 100}, "", "", "")
				for offset := 0; e == nil; offset += 100 {
					if offset > 0 {
						v, e = knowledge.Changes(h.Service, knowledge.BrowseQuery{Limit: 100, Offset: offset}, "", "", "")
					}
					for _, c := range v.Items {
						if c.ID == q.Get("id") {
							reply(w, c, nil)
							return
						}
					}
					if offset+100 >= v.Total {
						break
					}
				}
				if e == nil {
					e = core.Error("CHANGE_NOT_FOUND", q.Get("id"))
				}
			}
			reply(w, v, e)
		case "/api/knowledge-status":
			reply(w, knowledge.Status(h.Service.Root), nil)
		case "/api/task-knowledge":
			v, e := knowledge.TaskView(h.Service, r.URL.Query().Get("id"))
			if r.URL.Query().Get("compact") == "1" && e == nil {
				m := v.(map[string]any)
				records := []knowledge.Record{}
				next, err := h.Service.Next(r.URL.Query().Get("id"))
				if err != nil {
					reply(w, nil, err)
					return
				}
				t := next.Task
				for _, k := range m["records"].([]knowledge.Record) {
					explicit := false
					for _, b := range t.Basis {
						if b.ID == k.ID {
							explicit = true
						}
					}
					for _, id := range t.Definition.Knowledge {
						if id == k.ID {
							explicit = true
						}
					}
					for _, id := range k.Tasks {
						if id == t.Definition.ID {
							explicit = true
						}
					}
					blocking := false
					for _, reason := range next.Reasons {
						if reason == "unresolved knowledge conflict: "+k.ID {
							blocking = true
						}
					}
					if explicit || blocking {
						k.Title = knowledge.Title(k)
						k.Body = ""
						records = append(records, k)
					}
				}
				m["records"] = records
				health := knowledge.Status(h.Service.Root)
				health.Sources = nil
				m["health"] = health
			}
			reply(w, v, e)
		case "/api/knowledge":
			v, e := knowledge.WithUsage(h.Service, r.URL.Query().Get("q"))
			reply(w, v, e)
		case "/api/info":
			v, e := bootstrap.Doctor(h.Service.Root)
			v.Knowledge.Sources = nil
			reply(w, map[string]any{"project": v.Root, "data_dir": v.Data, "config": v.Config, "service_version": v.ServiceVersion, "service_build": v.ServiceBuild, "executable": v.Executable, "instance_id": h.Token[len(h.Token)-12:], "knowledge_health": v.Knowledge, "warnings": v.Warnings}, e)
		case "/api/tasks":
			if r.Method == "GET" {
				limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
				offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
				days, _ := strconv.Atoi(r.URL.Query().Get("days"))
				offsets := map[string]int{}
				for _, board := range []string{"active", "pending", "confirmation", "completed", "cancelled"} {
					if raw := r.URL.Query().Get("offset_" + board); raw != "" {
						value, _ := strconv.Atoi(raw)
						offsets[board] = max(0, value)
					}
				}
				v, e := h.Service.QueryPage(core.Query{Limit: limit, Offset: offset, Offsets: offsets, Days: days, Text: r.URL.Query().Get("q"), Type: r.URL.Query().Get("type"), Board: r.URL.Query().Get("board")})
				if r.URL.Query().Get("compact") == "1" && e == nil {
					items := []core.TaskSummary{}
					for _, n := range v.Items {
						summary, err := h.Service.Summarize(n)
						if err != nil {
							reply(w, nil, err)
							return
						}
						items = append(items, summary)
					}
					reply(w, map[string]any{"items": items, "counts": v.Counts, "total": v.Total, "limit": v.Limit, "offset": v.Offset, "offsets": v.Offsets}, nil)
					return
				}
				reply(w, v, e)
			} else if r.Method == "POST" {
				var d core.Definition
				e := decode(r, &d)
				if e != nil {
					reply(w, nil, e)
					return
				}
				v, e := h.Service.Create(d)
				reply(w, v, e)
			} else {
				http.Error(w, "METHOD", 405)
			}
		case "/api/attention":
			if r.Method != "GET" {
				http.Error(w, "METHOD", 405)
				return
			}
			v, e := h.Service.Attention()
			reply(w, v, e)
		case "/api/task":
			v, e := h.Service.Next(r.URL.Query().Get("id"))
			if e == nil {
				summary, err := h.Service.Summarize(v)
				checks := map[string]*core.Evidence{}
				access := map[string]core.CheckAccess{}
				for _, c := range v.Task.Definition.Checks {
					checks[c.ID] = core.CurrentEvidence(v.Task, c)
					access[c.ID] = core.ReviewAccess(v, c)
				}
				reply(w, struct {
					core.Next
					Summary core.TaskSummary               `json:"summary"`
					Checks  map[string]*core.Evidence      `json:"current_checks"`
					Runs    map[string]core.CheckExecution `json:"current_runs"`
					Access  map[string]core.CheckAccess    `json:"check_access"`
				}{v, summary, checks, summary.CurrentRuns, access}, err)
				return
			}
			reply(w, v, e)
		case "/api/context":
			v, e := knowledge.Context(h.Service, r.URL.Query().Get("id"), "", 12000)
			reply(w, v, e)
		case "/api/events":
			v, e := h.Service.Events(r.URL.Query().Get("id"))
			reply(w, v, e)
		case "/api/action":
			if r.Method != "POST" {
				http.Error(w, "METHOD", 405)
				return
			}
			var a struct {
				Action       string          `json:"action"`
				ID           string          `json:"id"`
				Unit         string          `json:"unit"`
				Executor     string          `json:"executor"`
				Reason       string          `json:"reason"`
				Decision     string          `json:"decision"`
				Result       string          `json:"result"`
				Recovery     string          `json:"recovery"`
				RequestKey   string          `json:"request_key"`
				Kind         string          `json:"kind"`
				Candidate    string          `json:"candidate"`
				Revision     int             `json:"revision"`
				PlanRevision int             `json:"plan_revision"`
				Ack          bool            `json:"ack_stopped"`
				Submission   core.Submission `json:"submission"`
				Definition   core.Definition `json:"definition"`
				Artifacts    []string        `json:"artifacts"`
				Assessment   core.Assessment `json:"assessment"`
			}
			e := decode(r, &a)
			if e != nil {
				reply(w, nil, e)
				return
			}
			var v any
			switch a.Action {
			case "claim":
				v, e = h.Service.Claim(a.ID, a.Unit, a.Executor, "", "")
			case "submit":
				v, e = h.Service.Submit(a.Submission)
			case "verify":
				v, e = h.Service.Verify(a.ID, a.Unit, "dashboard")
			case "assess":
				v, e = h.Service.Assess(a.Assessment)
			case "review":
				v, e = h.Service.ReviewExact(a.ID, a.Unit, a.Executor, a.Reason, a.Artifacts, core.ReviewExpectation{Candidate: a.Candidate, DefinitionRevision: a.Revision, PlanRevision: a.PlanRevision})
			case "review_result":
				v, e = h.Service.ReviewResultExact(a.ID, a.Unit, a.Executor, a.Reason, a.Recovery, a.Result, a.Artifacts, a.RequestKey, core.ReviewExpectation{Candidate: a.Candidate, DefinitionRevision: a.Revision, PlanRevision: a.PlanRevision})
			case "confirm":
				v, e = h.Service.Confirm(a.ID, a.Kind, a.Decision, a.Executor, a.Reason, a.Candidate, a.Revision, a.PlanRevision)
			case "pause":
				v, e = h.Service.Pause(a.ID, a.Reason)
			case "resume":
				v, e = h.Service.Resume(a.ID, a.Unit, a.Ack)
			case "cancel":
				v, e = h.Service.Cancel(a.ID, a.Reason)
			case "delete":
				v, e = h.Service.Delete(a.ID, a.Reason)
			case "edit":
				v, e = h.Service.Edit(a.ID, a.Definition, a.Reason)
			default:
				e = fmt.Errorf("UNKNOWN_ACTION")
			}
			reply(w, v, e)
		case "/api/artifact":
			var b []byte
			var e error
			if id := r.URL.Query().Get("task"); id != "" {
				b, e = h.Service.TaskArtifact(id, r.URL.Query().Get("ref"))
			} else {
				b, e = h.Service.Artifact(r.URL.Query().Get("ref"))
			}
			if e != nil {
				reply(w, nil, e)
				return
			}
			if r.URL.Query().Get("format") == "image" {
				contentType := http.DetectContentType(b)
				if contentType != "image/png" && contentType != "image/jpeg" {
					reply(w, nil, core.Error("UNSUPPORTED_IMAGE", "only PNG/JPEG evidence can be displayed"))
					return
				}
				w.Header().Set("Content-Type", contentType)
				w.Write(b)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write(b)
		default:
			http.NotFound(w, r)
		}
		return
	}
	if r.Method != "GET" {
		http.Error(w, "METHOD", 405)
		return
	}
	sub, _ := fs.Sub(ui, "ui")
	http.FileServer(http.FS(sub)).ServeHTTP(w, r)
}
func Listen(s *core.Service, addr string, extra func(http.ResponseWriter, *http.Request) bool) error {
	if _, _, e := net.SplitHostPort(addr); e != nil {
		return e
	}
	ln, e := net.Listen("tcp", addr)
	if e != nil {
		return e
	}
	h := New(s, ln.Addr().String())
	h.Extra = extra
	fmt.Printf("{\"url\":\"http://%s\"}\n", ln.Addr())
	return (&http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}).Serve(ln)
}

// A loopback bind also accepts localhost on the bound port. A wildcard bind
// accepts IP authorities and local host names; arbitrary DNS names stay rejected.
// Origin must always match the validated request authority.
func (h *Handler) acceptsHost(authority string) bool {
	if authority == h.Host {
		return true
	}
	bind, port, e := net.SplitHostPort(h.Host)
	if e != nil {
		return false
	}
	host, p, e := net.SplitHostPort(authority)
	if e != nil || p != port {
		return false
	}
	ip := net.ParseIP(bind)
	if ip != nil && ip.IsLoopback() {
		return strings.EqualFold(host, "localhost")
	}
	if bind != "" && (ip == nil || !ip.IsUnspecified()) {
		return false
	}
	if parsed := net.ParseIP(host); parsed != nil {
		return !parsed.IsUnspecified() && !parsed.IsMulticast()
	}
	name, _ := os.Hostname()
	return strings.EqualFold(host, "localhost") || (name != "" && strings.EqualFold(host, name))
}
