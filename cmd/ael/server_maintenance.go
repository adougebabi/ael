package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func serverMaintenanceCommand(a args) (any, error) {
	allowed := map[string]bool{"url": true, "username": true, "password-stdin": true, "ca-file": true, "action": true, "reason": true, "deadline": true, "request-key": true, "expected-state": true, "json": true, "verbose": true, "no-color": true}
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
	session, err := newServerSession(remote.Profile{URL: base.String(), CertificateAuthority: authority})
	if err != nil {
		return nil, err
	}
	defer session.close()
	if err = session.login(a.get("username", ""), password); err != nil {
		return nil, err
	}
	action := a.get("action", "status")
	if action == "status" {
		return session.call(http.MethodGet, "/api/v1/admin/maintenance", nil)
	}
	body := map[string]any{"action": action, "reason": a.get("reason", ""), "request_key": a.get("request-key", "")}
	if a.get("deadline", "") != "" {
		body["deadline"] = a.get("deadline", "")
	}
	if a.get("expected-state", "") != "" {
		body["expected_state"] = a.get("expected-state", "")
	}
	return session.call(http.MethodPost, "/api/v1/admin/maintenance", body)
}
