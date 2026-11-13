package main

import (
	"ael/internal/core"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func serverHealthCommand(a args) (any, error) {
	for flag, value := range a.Flags {
		if flag != "url" && flag != "json" && flag != "verbose" && flag != "no-color" {
			return nil, core.Error("UNKNOWN_FLAG", flag)
		}
		if !booleanFlag(flag) && value == "true" {
			return nil, core.Error("FLAG_VALUE_REQUIRED", flag)
		}
	}
	base, err := url.Parse(a.get("url", "http://127.0.0.1:7332"))
	if err != nil || base.Scheme != "http" || base.Host == "" || base.User != nil || base.Path != "" || base.RawQuery != "" || base.Fragment != "" {
		return nil, core.Error("HEALTH_URL_INVALID", "use the private loopback HTTP listener")
	}
	ip := net.ParseIP(base.Hostname())
	if !strings.EqualFold(base.Hostname(), "localhost") && (ip == nil || !ip.IsLoopback()) {
		return nil, core.Error("HEALTH_URL_INVALID", "health is a private loopback probe")
	}
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(base.String() + "/healthz")
	if err != nil {
		return nil, core.Error("SERVER_UNHEALTHY", "private health listener unavailable")
	}
	defer response.Body.Close()
	var result struct {
		Status      string `json:"status"`
		Protocol    int    `json:"protocol"`
		Database    string `json:"database"`
		Initialized bool   `json:"initialized"`
		Version     string `json:"version"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<10))
	if response.StatusCode != http.StatusOK || decoder.Decode(&result) != nil || result.Status != "healthy" || result.Protocol != 1 || !result.Initialized {
		return nil, core.Error("SERVER_UNHEALTHY", "database or administrator readiness failed")
	}
	return result, nil
}
