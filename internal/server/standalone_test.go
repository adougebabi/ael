package server

import (
	"ael/internal/store"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func standaloneRequest(t *testing.T, h http.Handler, method, path string, body any, cookies []*http.Cookie, csrf, bearer, origin, protocol string) *httptest.ResponseRecorder {
	t.Helper()
	var raw io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		raw = bytes.NewReader(data)
	}
	r := httptest.NewRequest(method, "http://server.test"+path, raw)
	r.RemoteAddr = "127.0.0.1:12345"
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if protocol != "" {
		r.Header.Set("X-AEL-Protocol", protocol)
	}
	if csrf != "" {
		r.Header.Set("X-AEL-CSRF", csrf)
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func standaloneJSON(t *testing.T, w *httptest.ResponseRecorder, want int) map[string]any {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status %d, want %d: %s", w.Code, want, w.Body.String())
	}
	var value map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func standaloneLogin(t *testing.T, h http.Handler, user, password string) ([]*http.Cookie, string) {
	t.Helper()
	w := standaloneRequest(t, h, "POST", "/api/v1/auth/login", map[string]string{"username": user, "password": password}, nil, "", "", "http://server.test", "1")
	v := standaloneJSON(t, w, http.StatusOK)
	csrf, _ := v["csrf"].(string)
	if csrf == "" || len(w.Result().Cookies()) == 0 {
		t.Fatal("login did not establish a browser session and CSRF token")
	}
	return w.Result().Cookies(), csrf
}

func TestStandaloneServerPersistsProjectsAndEnforcesIdentity(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStandalone(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BootstrapAdmin("owner", "fixture-admin-password"); err != nil {
		t.Fatal(err)
	}
	if err = s.BootstrapAdmin("another", "fixture-other-password"); err == nil {
		t.Fatal("second bootstrap succeeded")
	}
	h, err := s.Handler(StandaloneHTTPOptions{Origin: "http://server.test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/projects", "/api/v1/projects/private", "/api/v1/projects/private/tasks", "/api/v1/projects/private/evidence/guess"} {
		w := standaloneRequest(t, h, "GET", path, nil, nil, "", "", "", "")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	admin, csrf := standaloneLogin(t, h, "owner", "fixture-admin-password")
	bound := standaloneRequest(t, h, "GET", "/api/v1/projects", nil, admin, "", "", "", "")
	if bound.Code != http.StatusOK || bound.Header().Get("X-AEL-Session-Context") != csrf {
		t.Fatal("authenticated response was not bound to its browser session")
	}
	var passwordHash []byte
	if err = s.store.DB.QueryRow("SELECT password_hash FROM server_users WHERE username='owner'").Scan(&passwordHash); err != nil || bytes.Contains(passwordHash, []byte("fixture-admin-password")) {
		t.Fatal("administrator password was stored in plaintext", err)
	}
	var sessionValue string
	for _, cookie := range admin {
		if cookie.Name == "ael_session" {
			sessionValue = cookie.Value
		}
	}
	var sessionHash []byte
	if sessionValue == "" || s.store.DB.QueryRow("SELECT secret_hash FROM server_sessions WHERE secret_hash=?", standaloneHash(sessionValue)).Scan(&sessionHash) != nil || bytes.Equal(sessionHash, []byte(sessionValue)) {
		t.Fatal("session secret was not stored as a digest")
	}
	post := func(path string, body any) *httptest.ResponseRecorder {
		return standaloneRequest(t, h, "POST", path, body, admin, csrf, "", "http://server.test", "1")
	}
	standaloneJSON(t, post("/api/v1/projects", map[string]string{"id": "private", "name": "Private Project"}), http.StatusCreated)
	standaloneJSON(t, post("/api/v1/projects", map[string]string{"id": "shared", "name": "Shared Project"}), http.StatusCreated)
	standaloneJSON(t, post("/api/v1/users", map[string]string{"username": "worker", "password": "fixture-worker-password"}), http.StatusCreated)
	standaloneJSON(t, post("/api/v1/projects/shared/members", map[string]string{"username": "worker", "role": "executor"}), http.StatusCreated)
	for _, role := range []string{"reviewer", "reader"} {
		standaloneJSON(t, post("/api/v1/users", map[string]string{"username": role, "password": "fixture-role-password"}), http.StatusCreated)
		standaloneJSON(t, post("/api/v1/projects/shared/members", map[string]string{"username": role, "role": role}), http.StatusCreated)
		issued := standaloneJSON(t, post("/api/v1/tokens", map[string]string{"username": role, "project_id": "shared", "label": role}), http.StatusCreated)
		roleToken, _ := issued["token"].(string)
		read := standaloneRequest(t, h, "GET", "/api/v1/projects/shared", nil, nil, "", roleToken, "", "")
		project := standaloneJSON(t, read, http.StatusOK)
		if project["role"] != role {
			t.Fatalf("token did not retain project role %s: %+v", role, project)
		}
		write := standaloneRequest(t, h, "POST", "/api/v1/projects", map[string]string{"id": "forged", "name": "forged", "executor": "owner"}, nil, "", roleToken, "", "1")
		if write.Code != http.StatusForbidden {
			t.Fatalf("%s token acquired administrator privilege: %d", role, write.Code)
		}
	}
	tokenResponse := standaloneJSON(t, post("/api/v1/tokens", map[string]string{"username": "worker", "project_id": "shared", "label": "fixture"}), http.StatusCreated)
	token, _ := tokenResponse["token"].(string)
	tokenID, _ := tokenResponse["id"].(string)
	if token == "" || tokenID == "" {
		t.Fatal("token was not issued")
	}
	var tokenHash []byte
	if err = s.store.DB.QueryRow("SELECT secret_hash FROM server_tokens WHERE id=?", tokenID).Scan(&tokenHash); err != nil || bytes.Equal(tokenHash, []byte(token)) || bytes.Contains(tokenHash, []byte(token)) {
		t.Fatal("agent token was stored in plaintext", err)
	}
	w := standaloneRequest(t, h, "GET", "/api/v1/tokens", nil, admin, "", "", "", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tokenID) || strings.Contains(w.Body.String(), token) {
		t.Fatalf("token listing is missing its ID or re-exposes the secret: %d %s", w.Code, w.Body.String())
	}
	worker, _ := standaloneLogin(t, h, "worker", "fixture-worker-password")
	w = standaloneRequest(t, h, "GET", "/api/v1/projects", nil, worker, "", "", "", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "shared") || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("project listing did not enforce membership: %d %s", w.Code, w.Body.String())
	}
	w = standaloneRequest(t, h, "GET", "/api/v1/projects/private", nil, worker, "", "", "", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("project ID guessing disclosed another project: %d %s", w.Code, w.Body.String())
	}
	w = standaloneRequest(t, h, "GET", "/api/v1/projects/shared", nil, nil, "", token, "", "")
	standaloneJSON(t, w, http.StatusOK)
	w = standaloneRequest(t, h, "POST", "/api/v1/projects", map[string]string{"id": "forged", "name": "forged", "executor": "owner"}, nil, "", token, "http://server.test", "1")
	if w.Code != http.StatusForbidden {
		t.Fatalf("executor field escalated token privileges: %d %s", w.Code, w.Body.String())
	}
	w = standaloneRequest(t, h, "POST", "/api/v1/projects", map[string]string{"id": "csrf", "name": "CSRF"}, admin, "", "", "http://server.test", "1")
	if w.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF accepted: %d", w.Code)
	}
	w = standaloneRequest(t, h, "POST", "/api/v1/projects", map[string]string{"id": "version", "name": "Version"}, admin, csrf, "", "http://server.test", "99")
	if w.Code != http.StatusUpgradeRequired {
		t.Fatalf("incompatible protocol accepted: %d", w.Code)
	}
	standaloneJSON(t, post("/api/v1/tokens/revoke", map[string]string{"id": tokenID}), http.StatusOK)
	w = standaloneRequest(t, h, "GET", "/api/v1/projects/shared", nil, nil, "", token, "", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token accepted: %d", w.Code)
	}
	standaloneJSON(t, post("/api/v1/auth/logout", map[string]string{}), http.StatusOK)
	w = standaloneRequest(t, h, "GET", "/api/v1/projects", nil, admin, "", "", "", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("logged-out session accepted: %d", w.Code)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStandalone(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err = s.Handler(StandaloneHTTPOptions{Origin: "http://server.test"})
	if err != nil {
		t.Fatal(err)
	}
	admin, _ = standaloneLogin(t, h, "owner", "fixture-admin-password")
	w = standaloneRequest(t, h, "GET", "/api/v1/projects/private", nil, admin, "", "", "", "")
	standaloneJSON(t, w, http.StatusOK)
	w = standaloneRequest(t, h, "GET", "/api/v1/projects/shared", nil, nil, "", token, "", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token revived after restart: %d", w.Code)
	}
	if _, err = os.Stat(filepath.Join(dir, "attachments")); err != nil {
		t.Fatal("persistent attachment root missing", err)
	}
	if _, err = os.Stat(filepath.Join(dir, ".ael")); !os.IsNotExist(err) {
		t.Fatal("standalone server initialized a checkout")
	}
}

func TestStandaloneUnauthenticatedErrorsAreGeneric(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStandalone(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.BootstrapAdmin("owner", "fixture-admin-password"); err != nil {
		t.Fatal(err)
	}
	h, err := s.Handler(StandaloneHTTPOptions{Origin: "http://server.test"})
	if err != nil {
		t.Fatal(err)
	}
	secret := "wrong-fixture-password"
	w := standaloneRequest(t, h, "POST", "/api/v1/auth/login", map[string]string{"username": "owner", "password": secret}, nil, "", "", "http://server.test", "1")
	if w.Code != http.StatusUnauthorized || strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), dir) {
		t.Fatalf("authentication error leaked sensitive details: %d %s", w.Code, w.Body.String())
	}
	w = standaloneRequest(t, h, "POST", "/api/v1/auth/login", map[string]string{"username": "owner", "password": "fixture-admin-password"}, nil, "", "", "http://evil.test", "1")
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin login accepted: %d", w.Code)
	}
}

func TestStandaloneRejectsExistingDataAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("do not change"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStandalone(dir); err == nil {
		t.Fatal("server accepted a nonempty unmarked directory")
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "existing.txt")); err != nil || string(raw) != "do not change" {
		t.Fatal("existing data changed", err)
	}
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ael-server.marker"), []byte("AEL-STANDALONE-1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	targetDir := t.TempDir()
	existing, err := store.Open(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = existing.DB.Exec("CREATE TABLE sentinel(value TEXT); INSERT INTO sentinel(value) VALUES('do not change')"); err != nil {
		t.Fatal(err)
	}
	if err = existing.Close(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(targetDir, "runtime.db")
	if err := os.Symlink(target, filepath.Join(dir, "runtime.db")); err != nil {
		t.Fatal(err)
	}
	unexpectedServer, err := OpenStandalone(dir)
	if err == nil {
		_ = unexpectedServer.Close()
		t.Fatal("server followed a database symlink")
	}
	existing, err = store.Open(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	defer existing.Close()
	var unexpected int
	if err = existing.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='server_users'").Scan(&unexpected); err != nil || unexpected != 0 {
		t.Fatal("symlink target changed", err)
	}
}

func TestStandaloneTrustsForwardedHTTPSOnlyFromConfiguredProxy(t *testing.T) {
	s, err := OpenStandalone(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.BootstrapAdmin("owner", "fixture-admin-password"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Handler(StandaloneHTTPOptions{Origin: "https://server.test"}); err == nil {
		t.Fatal("HTTPS proxy origin accepted without a trusted proxy")
	}
	h, err := s.Handler(StandaloneHTTPOptions{Origin: "https://server.test", TrustedProxies: []string{"127.0.0.1/32"}})
	if err != nil {
		t.Fatal(err)
	}
	request := func(remote string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://server.test/api/v1/meta", nil)
		r.RemoteAddr = remote
		r.Header.Set("X-Forwarded-Proto", "https")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("192.0.2.1:1234"); w.Code != http.StatusForbidden {
		t.Fatalf("untrusted forwarded protocol accepted: %d", w.Code)
	}
	if w := request("127.0.0.1:1234"); w.Code != http.StatusOK {
		t.Fatalf("trusted forwarded HTTPS rejected: %d", w.Code)
	}
	data := strings.NewReader(`{"username":"owner","password":"fixture-admin-password"}`)
	r := httptest.NewRequest("POST", "http://server.test/api/v1/auth/login", data)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Origin", "https://server.test")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-AEL-Protocol", "1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("trusted proxy login failed: %d %s", w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
			t.Fatalf("unsafe browser cookie: %+v", c)
		}
	}
}

func TestStandaloneThrottlesRepeatedFailedLogins(t *testing.T) {
	s, err := OpenStandalone(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.BootstrapAdmin("owner", "fixture-admin-password"); err != nil {
		t.Fatal(err)
	}
	h, err := s.Handler(StandaloneHTTPOptions{Origin: "http://server.test"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		w := standaloneRequest(t, h, "POST", "/api/v1/auth/login", map[string]string{"username": "owner", "password": "wrong-password"}, nil, "", "", "http://server.test", "1")
		want := http.StatusUnauthorized
		if i == 5 {
			want = http.StatusTooManyRequests
		}
		if w.Code != want {
			t.Fatalf("attempt %d: got %d want %d", i+1, w.Code, want)
		}
	}
}

func TestStandaloneRejectsDatabaseAndSidecarHardLinks(t *testing.T) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		t.Run("database"+suffix, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "ael-server.marker"), []byte("AEL-STANDALONE-1\n"), 0600); err != nil {
				t.Fatal(err)
			}
			victimDir := t.TempDir()
			victim := filepath.Join(victimDir, "victim")
			if suffix == "" {
				existing, err := store.Open(victimDir)
				if err != nil {
					t.Fatal(err)
				}
				if err = existing.Close(); err != nil {
					t.Fatal(err)
				}
				victim = filepath.Join(victimDir, "runtime.db")
				if err = os.Chmod(victim, 0600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(victim, []byte("unrelated data"), 0600); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(victim)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Link(victim, filepath.Join(dir, "runtime.db"+suffix)); err != nil {
				t.Fatal(err)
			}
			unexpected, err := OpenStandalone(dir)
			if err == nil {
				_ = unexpected.Close()
				t.Fatal("server accepted a hard-linked database or sidecar")
			}
			if raw, err := os.ReadFile(victim); err != nil || !bytes.Equal(raw, before) {
				t.Fatal("unrelated hard-link target changed", err)
			}
		})
	}
}

func TestStandaloneProxyLoginThrottleSeparatesTrustedClients(t *testing.T) {
	s, err := OpenStandalone(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.BootstrapAdmin("owner", "fixture-admin-password"); err != nil {
		t.Fatal(err)
	}
	h, err := s.Handler(StandaloneHTTPOptions{Origin: "https://server.test", TrustedProxies: []string{"127.0.0.1/32"}})
	if err != nil {
		t.Fatal(err)
	}
	login := func(client, password string) *httptest.ResponseRecorder {
		data, _ := json.Marshal(map[string]string{"username": "owner", "password": password})
		r := httptest.NewRequest("POST", "http://server.test/api/v1/auth/login", bytes.NewReader(data))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", "https://server.test")
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-For", client)
		r.Header.Set("X-AEL-Protocol", "1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for i := 0; i < 5; i++ {
		if w := login("192.0.2.1", "wrong-password"); w.Code != http.StatusUnauthorized {
			t.Fatalf("first client attempt %d: %d", i+1, w.Code)
		}
	}
	if w := login("192.0.2.1", "wrong-password"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("first client was not throttled: %d", w.Code)
	}
	if w := login("192.0.2.2", "fixture-admin-password"); w.Code != http.StatusOK {
		t.Fatalf("different proxy client was incorrectly throttled: %d %s", w.Code, w.Body.String())
	}
}

func TestStandaloneIgnoresForgedForwardedClientHops(t *testing.T) {
	s, err := OpenStandalone(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.BootstrapAdmin("owner", "fixture-admin-password"); err != nil {
		t.Fatal(err)
	}
	h, err := s.Handler(StandaloneHTTPOptions{Origin: "http://server.test", TrustedProxies: []string{"127.0.0.1/32"}})
	if err != nil {
		t.Fatal(err)
	}
	chain := httptest.NewRequest("GET", "http://server.test/api/v1/meta", nil)
	chain.RemoteAddr = "127.0.0.1:1234"
	chain.Header.Set("X-Forwarded-For", "198.51.100.99, 192.0.2.10")
	client, err := h.(*standaloneHandler).clientIP(chain)
	if err != nil || client != "192.0.2.10" {
		t.Fatalf("trusted proxy chain accepted client-supplied leftmost hop: %q %v", client, err)
	}
	for i := 0; i < 6; i++ {
		data := strings.NewReader(`{"username":"owner","password":"wrong-password"}`)
		r := httptest.NewRequest("POST", "http://server.test/api/v1/auth/login", data)
		r.RemoteAddr = "192.0.2.10:1234"
		r.Header.Set("Origin", "http://server.test")
		r.Header.Set("X-AEL-Protocol", "1")
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i+1))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := http.StatusUnauthorized
		if i == 5 {
			want = http.StatusTooManyRequests
		}
		if w.Code != want {
			t.Fatalf("forged upstream attempt %d: got %d want %d", i+1, w.Code, want)
		}
	}
}

func TestStandaloneThrottlesAccountAcrossProxyClients(t *testing.T) {
	s, err := OpenStandalone(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.BootstrapAdmin("owner", "fixture-admin-password"); err != nil {
		t.Fatal(err)
	}
	h, err := s.Handler(StandaloneHTTPOptions{Origin: "https://server.test", TrustedProxies: []string{"127.0.0.1/32"}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 11; i++ {
		data := strings.NewReader(`{"username":"owner","password":"wrong-password"}`)
		r := httptest.NewRequest("POST", "http://server.test/api/v1/auth/login", data)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", "https://server.test")
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("192.0.2.%d", i+1))
		r.Header.Set("X-AEL-Protocol", "1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := http.StatusUnauthorized
		if i == 10 {
			want = http.StatusTooManyRequests
		}
		if w.Code != want {
			t.Fatalf("rotating client attempt %d: got %d want %d", i+1, w.Code, want)
		}
	}
}
