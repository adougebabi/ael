package main

import (
	"ael/internal/core"
	"ael/internal/server"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func standaloneCommand(cmd string, a args) (any, error) {
	allowed := map[string]bool{"data-dir": true, "json": true, "verbose": true, "no-color": true}
	if cmd == "server bootstrap" {
		allowed["username"], allowed["password-stdin"] = true, true
	} else {
		allowed["addr"], allowed["origin"], allowed["trusted-proxy"] = true, true, true
	}
	for flag, value := range a.Flags {
		if !allowed[flag] {
			return nil, core.Error("UNKNOWN_FLAG", flag)
		}
		if booleanFlag(flag) && value != "true" && value != "false" {
			return nil, core.Error("INVALID_FLAG_VALUE", flag)
		}
		if !booleanFlag(flag) && value == "true" {
			return nil, core.Error("FLAG_VALUE_REQUIRED", flag)
		}
	}
	dir := a.get("data-dir", "")
	if !filepath.IsAbs(dir) {
		return nil, core.Error("SERVER_DATA_DIR_REQUIRED", "supply an absolute dedicated --data-dir")
	}
	if cmd == "server bootstrap" && (!a.has("password-stdin") || a.get("username", "") == "") {
		return nil, core.Error("SERVER_BOOTSTRAP_INPUT_REQUIRED", "provide --username and --password-stdin")
	}
	s, err := server.OpenStandalone(dir)
	if err != nil {
		return nil, core.Error("SERVER_OPEN_FAILED", "cannot open the dedicated local data directory")
	}
	defer s.Close()
	if cmd == "server bootstrap" {
		raw, err := io.ReadAll(io.LimitReader(os.Stdin, 1026))
		if err != nil || len(raw) > 1025 {
			return nil, core.Error("SERVER_BOOTSTRAP_INPUT_REQUIRED", "password input is invalid")
		}
		password := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
		if strings.ContainsAny(password, "\r\n") {
			return nil, core.Error("SERVER_BOOTSTRAP_INPUT_REQUIRED", "provide one password line")
		}
		if err = s.BootstrapAdmin(a.get("username", ""), password); err != nil {
			return nil, core.Error("SERVER_BOOTSTRAP_FAILED", "invalid administrator details or server already initialized")
		}
		return map[string]string{"status": "bootstrapped", "username": strings.ToLower(strings.TrimSpace(a.get("username", "")))}, nil
	}
	bootstrapped, err := s.Bootstrapped()
	if err != nil || !bootstrapped {
		return nil, core.Error("SERVER_NOT_BOOTSTRAPPED", "bootstrap an administrator first")
	}
	options := server.StandaloneHTTPOptions{Origin: a.get("origin", ""), TrustedProxies: csvValues(a.get("trusted-proxy", ""))}
	err = server.ListenStandalone(s, a.get("addr", "127.0.0.1:7332"), options, func(url string) {
		_ = writeOutput(os.Stdout, map[string]string{"url": url}, a.has("json"), a.has("verbose"))
	})
	if err != nil {
		if strings.Contains(err.Error(), "LOOPBACK_REQUIRED") {
			return nil, core.Error("LOOPBACK_REQUIRED", "standalone server must bind loopback")
		}
		return nil, core.Error("SERVER_LISTEN_FAILED", "cannot start the server with the supplied listener and proxy settings")
	}
	return nil, nil
}
