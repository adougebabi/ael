package server

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"
)

// Container health is a private loopback probe. It carries no project data and
// does not broaden the canonical Origin rules of the authenticated API.
func (h *standaloneHandler) privateHealth(w http.ResponseWriter, r *http.Request) {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(peer)
	host := r.Host
	if value, _, splitErr := net.SplitHostPort(host); splitErr == nil {
		host = value
	}
	hostIP := net.ParseIP(strings.Trim(host, "[]"))
	localHost := strings.EqualFold(host, "localhost") || hostIP != nil && hostIP.IsLoopback()
	if err != nil || ip == nil || !ip.IsLoopback() || !localHost || r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		standaloneFail(w, http.StatusForbidden, "HEALTH_PRIVATE")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err = h.server.store.DB.PingContext(ctx); err != nil {
		standaloneFail(w, http.StatusServiceUnavailable, "SERVER_UNHEALTHY")
		return
	}
	initialized, err := h.server.Bootstrapped()
	if err != nil || !initialized {
		standaloneFail(w, http.StatusServiceUnavailable, "SERVER_UNHEALTHY")
		return
	}
	standaloneReply(w, http.StatusOK, map[string]any{"status": "healthy", "protocol": 1, "database": "reachable", "initialized": true, "version": h.version})
}
