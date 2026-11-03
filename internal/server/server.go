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
		case "/api/timeline":
			if e := knowledge.Sync(h.Service); e != nil {
				reply(w, nil, e)
				return
			}
			v, e := h.Service.Timeline(100, 0)
			reply(w, v, e)
		case "/api/knowledge":
			v, e := knowledge.Search(h.Service.Root, r.URL.Query().Get("q"))
			reply(w, v, e)
		case "/api/info":
			v, e := bootstrap.Doctor(h.Service.Root)
			reply(w, v, e)
		case "/api/tasks":
			if r.Method == "GET" {
				limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
				offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
				days, _ := strconv.Atoi(r.URL.Query().Get("days"))
				v, e := h.Service.QueryPage(core.Query{Limit: limit, Offset: offset, Days: days, Text: r.URL.Query().Get("q"), Board: r.URL.Query().Get("board")})
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
		case "/api/task":
			v, e := h.Service.Next(r.URL.Query().Get("id"))
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
				Kind         string          `json:"kind"`
				Candidate    string          `json:"candidate"`
				Revision     int             `json:"revision"`
				PlanRevision int             `json:"plan_revision"`
				Ack          bool            `json:"ack_stopped"`
				Submission   core.Submission `json:"submission"`
				Definition   core.Definition `json:"definition"`
				Artifacts    []string        `json:"artifacts"`
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
			case "review":
				v, e = h.Service.Review(a.ID, a.Unit, a.Executor, a.Reason, a.Artifacts)
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

// A wildcard bind accepts IP authorities on the bound port and local host names.
// Arbitrary DNS names remain rejected; Origin must match the validated request.
func (h *Handler) acceptsHost(authority string) bool {
	if authority == h.Host {
		return true
	}
	bind, port, e := net.SplitHostPort(h.Host)
	if e != nil {
		return false
	}
	ip := net.ParseIP(bind)
	if bind != "" && (ip == nil || !ip.IsUnspecified()) {
		return false
	}
	host, p, e := net.SplitHostPort(authority)
	if e != nil || p != port {
		return false
	}
	if parsed := net.ParseIP(host); parsed != nil {
		return !parsed.IsUnspecified() && !parsed.IsMulticast()
	}
	name, _ := os.Hostname()
	return strings.EqualFold(host, "localhost") || (name != "" && strings.EqualFold(host, name))
}
