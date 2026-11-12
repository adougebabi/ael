package server

import (
	"bytes"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

type StandaloneHTTPOptions struct {
	Origin         string
	TrustedProxies []string
}

type standaloneHandler struct {
	server  *Standalone
	origin  *url.URL
	proxies []netip.Prefix
	mu      sync.Mutex
	logins  map[string][]time.Time
}

func (s *Standalone) Handler(options StandaloneHTTPOptions) (http.Handler, error) {
	origin, err := url.Parse(options.Origin)
	if err != nil || origin.Host == "" || origin.User != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return nil, errors.New("INVALID_SERVER_ORIGIN")
	}
	h := &standaloneHandler{server: s, origin: origin, logins: map[string][]time.Time{}}
	for _, value := range options.TrustedProxies {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			ip, ipErr := netip.ParseAddr(value)
			if ipErr != nil {
				return nil, errors.New("INVALID_TRUSTED_PROXY")
			}
			prefix = netip.PrefixFrom(ip, ip.BitLen())
		}
		h.proxies = append(h.proxies, prefix.Masked())
	}
	if origin.Scheme == "https" && len(h.proxies) == 0 {
		return nil, errors.New("TRUSTED_PROXY_REQUIRED")
	}
	return h, nil
}

func (h *standaloneHandler) isTrustedProxy(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return h.trustedAddress(ip.Unmap())
}

func (h *standaloneHandler) trustedAddress(ip netip.Addr) bool {
	for _, prefix := range h.proxies {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// Only walk addresses supplied through a configured proxy. Stop at the first
// untrusted hop, so a client cannot evade limits by prepending a forged value.
func (h *standaloneHandler) clientIP(r *http.Request) (string, error) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "", errors.New("INVALID_CLIENT")
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return "", errors.New("INVALID_CLIENT")
	}
	peer = peer.Unmap()
	if !h.trustedAddress(peer) || len(r.Header.Values("X-Forwarded-For")) == 0 {
		return peer.String(), nil
	}
	chain := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if len(chain) > 4096 {
		return "", errors.New("INVALID_PROXY_CHAIN")
	}
	parts := strings.Split(chain, ",")
	if len(parts) > 32 {
		return "", errors.New("INVALID_PROXY_CHAIN")
	}
	current := peer
	for i := len(parts) - 1; i >= 0 && h.trustedAddress(current); i-- {
		current, err = netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			return "", errors.New("INVALID_PROXY_CHAIN")
		}
		current = current.Unmap()
	}
	return current.String(), nil
}

func (h *standaloneHandler) validOrigin(r *http.Request) bool {
	if !strings.EqualFold(r.Host, h.origin.Host) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	scheme := "http"
	if r.TLS != nil || (h.isTrustedProxy(r.RemoteAddr) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")) {
		scheme = "https"
	}
	if scheme != h.origin.Scheme {
		return false
	}
	return r.Header.Get("Origin") == "" || r.Header.Get("Origin") == h.origin.String()
}

func (h *standaloneHandler) allowLogin(key string, limit int) bool {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.logins) > 4096 {
		for key, times := range h.logins {
			if len(times) == 0 || now.Sub(times[len(times)-1]) > time.Minute {
				delete(h.logins, key)
			}
		}
		if len(h.logins) > 4096 {
			return false
		}
	}
	recent := h.logins[key][:0]
	for _, at := range h.logins[key] {
		if now.Sub(at) < time.Minute {
			recent = append(recent, at)
		}
	}
	if len(recent) >= limit {
		h.logins[key] = recent
		return false
	}
	h.logins[key] = append(recent, now)
	return true
}

func standaloneReply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func standaloneFail(w http.ResponseWriter, status int, code string) {
	standaloneReply(w, status, map[string]string{"code": code, "error": code})
}

func (h *standaloneHandler) authenticate(r *http.Request) (standalonePrincipal, string, error) {
	if auth := r.Header.Get("Authorization"); auth != "" {
		if !strings.HasPrefix(auth, "Bearer ") {
			return standalonePrincipal{}, "", errors.New("AUTH_REQUIRED")
		}
		p, err := h.server.token(strings.TrimPrefix(auth, "Bearer "))
		return p, "token", err
	}
	cookie, err := r.Cookie("ael_session")
	if err != nil {
		return standalonePrincipal{}, "", err
	}
	p, err := h.server.session(cookie.Value)
	return p, "session", err
}

func (h *standaloneHandler) csrf(r *http.Request, p standalonePrincipal) bool {
	cookie, err := r.Cookie("ael_csrf")
	if err != nil || r.Header.Get("X-AEL-CSRF") == "" || cookie.Value != r.Header.Get("X-AEL-CSRF") {
		return false
	}
	return subtle.ConstantTimeCompare(standaloneHash(cookie.Value), p.CSRFHash) == 1
}

func (h *standaloneHandler) setCookie(w http.ResponseWriter, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: h.origin.Scheme == "https", SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}

func (h *standaloneHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'")
	if !h.validOrigin(r) {
		standaloneFail(w, http.StatusForbidden, "ORIGIN_REJECTED")
		return
	}
	if r.URL.Path == "/api/v1/meta" && r.Method == http.MethodGet {
		standaloneReply(w, http.StatusOK, map[string]any{"mode": "server", "protocol": 1, "capabilities": []string{"login", "projects", "project_roles", "revocable_tokens", "remote_tasks", "remote_plans", "operation_receipts", "artifacts", "portable_candidates", "portable_contexts", "remote_runners", "findings"}})
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		if r.Method != http.MethodGet {
			standaloneFail(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
			return
		}
		sub, _ := fs.Sub(ui, "ui")
		http.FileServer(http.FS(sub)).ServeHTTP(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Header.Get("X-AEL-Protocol") != "1" {
		standaloneReply(w, http.StatusUpgradeRequired, map[string]string{"code": "PROTOCOL_UNSUPPORTED", "error": "update the client to protocol 1"})
		return
	}
	if r.URL.Path == "/api/v1/auth/login" && r.Method == http.MethodPost {
		if r.Header.Get("Origin") != h.origin.String() {
			standaloneFail(w, http.StatusForbidden, "ORIGIN_REJECTED")
			return
		}
		client, err := h.clientIP(r)
		if err != nil {
			standaloneFail(w, http.StatusBadRequest, "INVALID_PROXY_CHAIN")
			return
		}
		if !h.allowLogin("ip:"+client, 5) {
			standaloneFail(w, http.StatusTooManyRequests, "LOGIN_THROTTLED")
			return
		}
		var in struct{ Username, Password string }
		if decode(r, &in) != nil {
			standaloneFail(w, http.StatusBadRequest, "INVALID_REQUEST")
			return
		}
		account := strings.ToLower(strings.TrimSpace(in.Username))
		if !h.allowLogin("account:"+hex.EncodeToString(standaloneHash(account)), 10) {
			standaloneFail(w, http.StatusTooManyRequests, "LOGIN_THROTTLED")
			return
		}
		session, csrf, err := h.server.login(in.Username, in.Password)
		if err != nil {
			standaloneFail(w, http.StatusUnauthorized, "INVALID_CREDENTIALS")
			return
		}
		h.setCookie(w, "ael_session", session, 12*60*60)
		h.setCookie(w, "ael_csrf", csrf, 12*60*60)
		standaloneReply(w, http.StatusOK, map[string]string{"csrf": csrf})
		return
	}
	p, method, err := h.authenticate(r)
	if err != nil {
		standaloneFail(w, http.StatusUnauthorized, "AUTH_REQUIRED")
		return
	}
	if r.Method != http.MethodGet {
		if method == "session" && (r.Header.Get("Origin") != h.origin.String() || !h.csrf(r, p)) {
			standaloneFail(w, http.StatusForbidden, "CSRF_REJECTED")
			return
		}
		// Read before acquiring the fence: a slow client must not prevent a
		// concurrent revocation from completing.
		raw, err := io.ReadAll(io.LimitReader(r.Body, (2<<20)+1))
		if err != nil || len(raw) > 2<<20 {
			standaloneFail(w, http.StatusBadRequest, "INVALID_REQUEST")
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(raw))
	}
	exclusive := r.Method != http.MethodGet && (r.URL.Path == "/api/v1/auth/logout" || r.URL.Path == "/api/v1/tokens/revoke" || strings.HasSuffix(r.URL.Path, "/members"))
	unlock, err := h.server.lockAuthorization(exclusive)
	if err != nil {
		standaloneFail(w, http.StatusInternalServerError, "SERVER_ERROR")
		return
	}
	defer unlock()
	fresh, freshMethod, err := h.authenticate(r)
	if err != nil || freshMethod != method || fresh.UserID != p.UserID || fresh.TokenID != p.TokenID {
		standaloneFail(w, http.StatusUnauthorized, "AUTH_REQUIRED")
		return
	}
	p = fresh
	if method == "session" {
		cookie, err := r.Cookie("ael_csrf")
		if err != nil || subtle.ConstantTimeCompare(standaloneHash(cookie.Value), p.CSRFHash) != 1 || (r.Method != http.MethodGet && !h.csrf(r, p)) {
			standaloneFail(w, http.StatusUnauthorized, "AUTH_REQUIRED")
			return
		}
		// Allows an existing tab to detect that another tab replaced its
		// browser session before applying an authenticated response.
		w.Header().Set("X-AEL-Session-Context", cookie.Value)
	}
	if r.URL.Path == "/api/v1/auth/session" && r.Method == http.MethodGet && method == "session" {
		cookie, err := r.Cookie("ael_csrf")
		if err != nil || subtle.ConstantTimeCompare(standaloneHash(cookie.Value), p.CSRFHash) != 1 {
			standaloneFail(w, http.StatusUnauthorized, "AUTH_REQUIRED")
			return
		}
		standaloneReply(w, http.StatusOK, map[string]any{"username": p.Username, "admin": p.Admin, "csrf": cookie.Value})
		return
	}
	if r.URL.Path == "/api/v1/auth/logout" && r.Method == http.MethodPost && method == "session" {
		cookie, _ := r.Cookie("ael_session")
		if h.server.logout(cookie.Value) != nil {
			standaloneFail(w, http.StatusInternalServerError, "SERVER_ERROR")
			return
		}
		h.setCookie(w, "ael_session", "", -1)
		h.setCookie(w, "ael_csrf", "", -1)
		standaloneReply(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if r.URL.Path == "/api/v1/tokens" && r.Method == http.MethodGet {
		if !p.Admin || method != "session" {
			standaloneFail(w, http.StatusForbidden, "FORBIDDEN")
			return
		}
		tokens, err := h.server.tokens()
		if err != nil {
			standaloneFail(w, http.StatusInternalServerError, "SERVER_ERROR")
			return
		}
		standaloneReply(w, http.StatusOK, map[string]any{"tokens": tokens})
		return
	}
	if r.URL.Path == "/api/v1/projects" {
		switch r.Method {
		case http.MethodGet:
			projects, err := h.server.projects(p)
			if err != nil {
				standaloneFail(w, http.StatusInternalServerError, "SERVER_ERROR")
				return
			}
			standaloneReply(w, http.StatusOK, map[string]any{"projects": projects})
		case http.MethodPost:
			if !p.Admin || method != "session" {
				standaloneFail(w, http.StatusForbidden, "FORBIDDEN")
				return
			}
			var in struct{ ID, Name string }
			if decode(r, &in) != nil || h.server.createProject(in.ID, in.Name) != nil {
				standaloneFail(w, http.StatusBadRequest, "PROJECT_REJECTED")
				return
			}
			standaloneReply(w, http.StatusCreated, map[string]string{"id": in.ID, "name": in.Name})
		default:
			standaloneFail(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		}
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/projects/") {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/projects/"), "/")
		if len(parts) == 2 && parts[1] == "reviews" {
			h.remoteReview(w, r, p, parts[0])
			return
		}
		if len(parts) >= 2 && parts[1] == "findings" {
			h.remoteFindings(w, r, p, parts[0], parts[2:])
			return
		}
		if len(parts) == 3 && parts[1] == "dashboard" {
			h.remoteDashboard(w, r, p, parts[0], parts[2])
			return
		}
		if len(parts) >= 2 && parts[1] == "artifacts" {
			h.remoteArtifacts(w, r, p, method, parts[0], parts[2:])
			return
		}
		if len(parts) >= 2 && (parts[1] == "repositories" || parts[1] == "candidates" || parts[1] == "observations" || parts[1] == "contexts" || parts[1] == "portable-operations" || parts[1] == "runners") {
			h.remotePortable(w, r, p, parts[0], parts[1], parts[2:])
			return
		}
		if len(parts) >= 2 && parts[1] == "tasks" {
			h.remoteTasks(w, r, p, parts[0], parts[2:])
			return
		}
		if len(parts) == 3 && parts[1] == "operations" && r.Method == http.MethodGet {
			h.remoteOperation(w, p, parts[0], parts[2])
			return
		}
		if len(parts) == 1 && r.Method == http.MethodGet {
			project, err := h.server.project(p, parts[0])
			if err != nil {
				standaloneFail(w, http.StatusNotFound, "PROJECT_NOT_FOUND")
				return
			}
			standaloneReply(w, http.StatusOK, project)
			return
		}
		if len(parts) == 2 && parts[1] == "members" && r.Method == http.MethodPost {
			if !p.Admin || method != "session" {
				standaloneFail(w, http.StatusForbidden, "FORBIDDEN")
				return
			}
			var in struct{ Username, Role string }
			if decode(r, &in) != nil || h.server.addMember(parts[0], in.Username, in.Role) != nil {
				standaloneFail(w, http.StatusBadRequest, "MEMBERSHIP_REJECTED")
				return
			}
			standaloneReply(w, http.StatusCreated, map[string]bool{"ok": true})
			return
		}
	}
	if r.Method == http.MethodPost && (r.URL.Path == "/api/v1/users" || r.URL.Path == "/api/v1/tokens" || r.URL.Path == "/api/v1/tokens/revoke") {
		if !p.Admin || method != "session" {
			standaloneFail(w, http.StatusForbidden, "FORBIDDEN")
			return
		}
		switch r.URL.Path {
		case "/api/v1/users":
			var in struct{ Username, Password string }
			if decode(r, &in) != nil || h.server.createUser(in.Username, in.Password) != nil {
				standaloneFail(w, http.StatusBadRequest, "USER_REJECTED")
				return
			}
			standaloneReply(w, http.StatusCreated, map[string]bool{"ok": true})
		case "/api/v1/tokens":
			var in struct {
				Username  string `json:"username"`
				ProjectID string `json:"project_id"`
				Label     string `json:"label"`
			}
			if decode(r, &in) != nil {
				standaloneFail(w, http.StatusBadRequest, "TOKEN_REJECTED")
				return
			}
			id, secret, err := h.server.issueToken(in.Username, in.ProjectID, in.Label)
			if err != nil {
				standaloneFail(w, http.StatusBadRequest, "TOKEN_REJECTED")
				return
			}
			standaloneReply(w, http.StatusCreated, map[string]string{"id": id, "token": secret})
		case "/api/v1/tokens/revoke":
			var in struct{ ID string }
			if decode(r, &in) != nil || h.server.revokeToken(in.ID) != nil {
				standaloneFail(w, http.StatusBadRequest, "TOKEN_REJECTED")
				return
			}
			standaloneReply(w, http.StatusOK, map[string]bool{"ok": true})
		}
		return
	}
	standaloneFail(w, http.StatusNotFound, "NOT_FOUND")
}

// ListenStandalone only accepts loopback binds; TLS termination, if used,
// must come from an explicitly configured trusted proxy and origin.
func ListenStandalone(s *Standalone, addr string, options StandaloneHTTPOptions, ready func(string)) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return errors.New("LOOPBACK_REQUIRED")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer ln.Close()
	if options.Origin == "" {
		options.Origin = "http://" + ln.Addr().String()
	}
	h, err := s.Handler(options)
	if err != nil {
		return err
	}
	if ready != nil {
		ready(options.Origin)
	}
	return (&http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 << 10}).Serve(ln)
}
