package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStandaloneHealthIsPrivateAndKeepsAPIOrigin(t *testing.T) {
	service, err := OpenStandalone(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if err = service.BootstrapAdmin("owner", "fixture-password-220"); err != nil {
		t.Fatal(err)
	}
	handler, err := service.Handler(StandaloneHTTPOptions{Origin: "https://localhost:9443", TrustedProxies: []string{"172.30.0.2"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		peer, host, origin, path string
		status                   int
	}{
		{"127.0.0.1:4000", "127.0.0.1:7332", "", "/healthz", 200},
		{"172.30.0.2:4000", "127.0.0.1:7332", "", "/healthz", 403},
		{"127.0.0.1:4000", "untrusted.invalid", "", "/healthz", 403},
		{"127.0.0.1:4000", "127.0.0.1:7332", "https://untrusted.invalid", "/healthz", 403},
		{"127.0.0.1:4000", "localhost:9443", "", "/api/v1/projects", 403},
	} {
		request := httptest.NewRequest(http.MethodGet, "http://"+test.host+test.path, nil)
		request.RemoteAddr = test.peer
		request.Header.Set("Origin", test.origin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("probe %+v returned %d %s", test, response.Code, response.Body.String())
		}
		if test.status == 200 && (!strings.Contains(response.Body.String(), "healthy") || strings.Contains(response.Body.String(), "runtime.db")) {
			t.Fatal("wrong health projection")
		}
	}
}
