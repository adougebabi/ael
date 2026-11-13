package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
)

func serverMaintenanceCommand(a args) (any, error) {
	allowed := map[string]bool{"url": true, "username": true, "password-stdin": true, "ca-file": true, "action": true, "reason": true, "deadline": true, "request-key": true, "json": true, "verbose": true, "no-color": true}
	for flag, value := range a.Flags {
		if !allowed[flag] {
			return nil, core.Error("UNKNOWN_FLAG", flag)
		}
		if !booleanFlag(flag) && value == "true" {
			return nil, core.Error("FLAG_VALUE_REQUIRED", flag)
		}
	}
	base, err := url.Parse(a.get("url", ""))
	if err != nil || base.Host == "" || base.User != nil || base.Path != "" || base.RawQuery != "" || base.Fragment != "" || (base.Scheme != "https" && base.Scheme != "http") {
		return nil, core.Error("SERVER_ORIGIN_REQUIRED", "supply the exact server HTTPS origin")
	}
	if base.Scheme == "http" {
		ip := net.ParseIP(base.Hostname())
		if !strings.EqualFold(base.Hostname(), "localhost") && (ip == nil || !ip.IsLoopback()) {
			return nil, core.Error("SERVER_ORIGIN_REQUIRED", "HTTP control is restricted to loopback")
		}
	}
	if !a.has("password-stdin") || a.get("username", "") == "" {
		return nil, core.Error("SERVER_ADMIN_INPUT_REQUIRED", "provide --username and --password-stdin")
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 1026))
	if err != nil || len(raw) > 1025 {
		return nil, core.Error("SERVER_ADMIN_INPUT_REQUIRED", "invalid password input")
	}
	password := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if strings.ContainsAny(password, "\r\n") {
		return nil, core.Error("SERVER_ADMIN_INPUT_REQUIRED", "provide one password line")
	}
	authority := ""
	if a.get("ca-file", "") != "" {
		authority, err = remote.ReadAuthority(a.get("ca-file", ""))
		if err != nil {
			return nil, err
		}
	}
	client, err := remote.NewHTTPClient(remote.Profile{URL: base.String(), CertificateAuthority: authority})
	if err != nil {
		return nil, err
	}
	if authority != "" {
		defer client.CloseIdleConnections()
	}
	client.Jar, _ = cookiejar.New(nil)
	csrf := ""
	call := func(method, path string, body any) (map[string]any, error) {
		var data io.Reader
		if body != nil {
			encoded, err := json.Marshal(body)
			if err != nil {
				return nil, err
			}
			data = bytes.NewReader(encoded)
		}
		request, err := http.NewRequest(method, base.String()+path, data)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Origin", base.String())
		request.Header.Set("X-AEL-Protocol", "1")
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		if csrf != "" {
			request.Header.Set("X-AEL-CSRF", csrf)
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, core.Error("SERVER_UNAVAILABLE", "server administration connection failed")
		}
		defer response.Body.Close()
		var result map[string]any
		if json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result) != nil {
			return nil, core.Error("SERVER_RESPONSE_INVALID", "invalid control response")
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			code, _ := result["code"].(string)
			if code == "" || strings.ContainsAny(code, " \r\n\x00") {
				code = "SERVER_ADMIN_REJECTED"
			}
			return nil, core.Error(code, "server administration request rejected")
		}
		return result, nil
	}
	login, err := call(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": a.get("username", ""), "password": password})
	if err != nil {
		return nil, err
	}
	csrf, _ = login["csrf"].(string)
	defer func() { _, _ = call(http.MethodPost, "/api/v1/auth/logout", map[string]string{}) }()
	action := a.get("action", "status")
	if action == "status" {
		return call(http.MethodGet, "/api/v1/admin/maintenance", nil)
	}
	body := map[string]any{"action": action, "reason": a.get("reason", ""), "request_key": a.get("request-key", "")}
	if a.get("deadline", "") != "" {
		body["deadline"] = a.get("deadline", "")
	}
	return call(http.MethodPost, "/api/v1/admin/maintenance", body)
}
