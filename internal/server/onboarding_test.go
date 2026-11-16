package server

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

func TestOnboardingCreatesOnceAndScopesCredential(t *testing.T) {
	f := newArtifactFixture(t)
	token := "ael_" + base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("f", 32)))
	input := map[string]any{"project_id": "new-project", "create_name": "New project", "operation_id": "create-one", "token": token}
	first := standaloneRequest(t, f.h, "POST", "/api/v1/onboarding", input, f.admin, f.csrf, "", "http://server.test", "1")
	standaloneJSON(t, first, http.StatusOK)
	replayed := standaloneRequest(t, f.h, "POST", "/api/v1/onboarding", input, f.admin, f.csrf, "", "http://server.test", "1")
	if first.Body.String() != replayed.Body.String() || replayed.Code != http.StatusOK {
		t.Fatalf("replay differed %d %s", replayed.Code, replayed.Body.String())
	}
	if strings.Contains(first.Body.String(), token) {
		t.Fatal("credential returned in ordinary receipt")
	}
	var count int
	if err := f.s.store.DB.QueryRow("SELECT count(*) FROM server_projects WHERE id='new-project'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("project duplicated %d %v", count, err)
	}
	if err := f.s.store.DB.QueryRow("SELECT count(*) FROM server_tokens WHERE project_id='new-project'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("token duplicated %d %v", count, err)
	}
	config := standaloneJSON(t, standaloneRequest(t, f.h, "GET", "/api/v1/projects/new-project/onboarding", nil, nil, "", token, "", ""), http.StatusOK)
	if config["specification_state"] != "awaiting_sources" || config["project"].(map[string]any)["role"] != "executor" {
		t.Fatalf("wrong readiness/role %+v", config)
	}
	if response := standaloneRequest(t, f.h, "POST", "/api/v1/onboarding", input, nil, "", f.uploader, "", "1"); response.Code != http.StatusForbidden {
		t.Fatalf("project token created project %d", response.Code)
	}
	user, csrf := standaloneLogin(t, f.h, "upload", "fixture-user-password")
	if response := standaloneRequest(t, f.h, "POST", "/api/v1/onboarding", input, user, csrf, "", "http://server.test", "1"); response.Code == http.StatusOK {
		t.Fatal("ordinary member created project")
	}
	input["project_id"], input["create_name"], input["operation_id"] = "shared", "", "connect-one"
	input["token"] = "ael_" + base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("g", 32)))
	standaloneJSON(t, standaloneRequest(t, f.h, "POST", "/api/v1/onboarding", input, user, csrf, "", "http://server.test", "1"), http.StatusOK)
	input["project_id"] = "other"
	if response := standaloneRequest(t, f.h, "POST", "/api/v1/onboarding", input, user, csrf, "", "http://server.test", "1"); response.Code == http.StatusOK {
		t.Fatal("member granted inaccessible project")
	}
}

func TestServiceReleaseInformationRequiresAdministratorSession(t *testing.T) {
	f := newArtifactFixture(t)
	info := standaloneJSON(t, standaloneRequest(t, f.h, "GET", "/api/v1/admin/client-release", nil, f.admin, f.csrf, "", "", ""), http.StatusOK)
	if info["minimum_client_version"] != "0.1.22" || info["client_release"].(map[string]any)["status"] != "no_configured_source" {
		t.Fatalf("invalid service info %+v", info)
	}
	if response := standaloneRequest(t, f.h, "GET", "/api/v1/admin/client-release", nil, nil, "", f.reader, "", ""); response.Code != http.StatusForbidden {
		t.Fatalf("reader obtained service administration %d", response.Code)
	}
	if response := standaloneRequest(t, f.h, "GET", "/api/v1/admin/client-release", nil, nil, "", f.uploader, "", ""); response.Code != http.StatusForbidden {
		t.Fatalf("project token obtained service administration %d", response.Code)
	}
}
