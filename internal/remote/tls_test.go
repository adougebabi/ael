package remote

import (
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplicitPublicAuthorityPreservesTLSVerification(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "trusted endpoint") }))
	defer server.Close()
	public := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	plain, err := NewHTTPClient(Profile{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if response, err := plain.Get(server.URL); err == nil {
		response.Body.Close()
		t.Fatal("untrusted certificate was accepted")
	}
	trusted, err := NewHTTPClient(Profile{URL: server.URL, CertificateAuthority: public})
	if err != nil {
		t.Fatal(err)
	}
	defer trusted.CloseIdleConnections()
	response, err := trusted.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if _, err := NewHTTPClient(Profile{CertificateAuthority: public + "\n-----BEGIN PRIVATE KEY-----\nZmFrZQ==\n-----END PRIVATE KEY-----\n"}); err == nil {
		t.Fatal("private key accepted as a trust profile")
	}
	config := filepath.Join(t.TempDir(), "config")
	project := t.TempDir()
	if err := SaveProfileWithAuthority(config, project, "private", server.URL, "ael_fixture", "# ignored annotation\n"+public); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadProfile(config, project, "private")
	if err != nil || strings.Contains(loaded.CertificateAuthority, "ignored annotation") {
		t.Fatalf("stored non-certificate data %+v %v", loaded, err)
	}
	if loaded.Token != "ael_fixture" || loaded.CertificateAuthority != public {
		t.Fatal("private token or chosen certificate changed")
	}
}
