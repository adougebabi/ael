package remote

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyBindingAndSessionProtocolStayExplicit(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, ".ael"), 0755)
	original := []byte(`{"schema":1,"profile":"central","project_id":"shared"}`)
	path := filepath.Join(root, ".ael", "remote.json")
	os.WriteFile(path, original, 0644)
	binding, exists, err := ReadBinding(root)
	if err != nil || !exists || binding.APIProtocol != 1 || binding.SkillProtocol != 1 {
		t.Fatalf("legacy binding %+v %v", binding, err)
	}
	current, _ := os.ReadFile(path)
	if string(current) != string(original) {
		t.Fatal("read changed legacy requirements")
	}
	t.Setenv("AEL_SKILL_PROTOCOL", "2")
	if _, err := Call(t.TempDir(), root, binding, http.MethodPost, "/tasks", map[string]string{}, nil); err == nil || !strings.Contains(err.Error(), "CLIENT_SESSION_PROTOCOL_UNSUPPORTED") {
		t.Fatalf("session was reinterpreted %v", err)
	}
	t.Setenv("AEL_SKILL_PROTOCOL", "1")
	binding.APIProtocol = 2
	if _, err := Call(t.TempDir(), root, binding, http.MethodGet, "/tasks", nil, nil); err == nil || !strings.Contains(err.Error(), "REMOTE_BINDING_PROTOCOL_UNSUPPORTED") {
		t.Fatalf("project protocol was ignored %v", err)
	}
}

func TestServerMetadataCompatibility(t *testing.T) {
	for _, test := range []struct {
		body      string
		errorCode string
	}{
		{`{"mode":"server","protocol":1,"capabilities":["remote_tasks"]}`, ""},
		{`{"mode":"server","protocol":2,"capabilities":["remote_tasks"]}`, "REMOTE_PROTOCOL_UNSUPPORTED"},
		{`{"mode":"server","protocol":1,"skill_protocol":2,"capabilities":["remote_tasks"]}`, "REMOTE_SKILL_PROTOCOL_UNSUPPORTED"},
	} {
		client := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(test.body)), Header: make(http.Header)}, nil
		})}
		meta, err := Metadata(client, Profile{URL: "http://127.0.0.1:7332"})
		if test.errorCode != "" {
			if err == nil || !strings.Contains(err.Error(), test.errorCode) {
				t.Fatalf("meta %+v %v", meta, err)
			}
		} else if err != nil || meta.Version != "" || meta.SkillProtocol != 1 {
			t.Fatalf("legacy meta invented a version %+v %v", meta, err)
		}
	}
}

func TestRemoteGETRejectsIncompatibleServerBeforeAuthoritativeRead(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(t.TempDir(), "config")
	if err := SaveProfile(config, root, "central", "http://127.0.0.1:7332", "ael_fixture"); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		body := `{"tasks":[]}`
		if request.URL.Path == "/api/v1/meta" {
			body = `{"mode":"server","protocol":2,"capabilities":["remote_tasks"]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	if value, err := Call(config, root, Binding{Schema: 1, Profile: "central", ProjectID: "shared"}, http.MethodGet, "/tasks", nil, client); err == nil || !strings.Contains(err.Error(), "REMOTE_PROTOCOL_UNSUPPORTED") {
		t.Fatalf("incompatible GET became authoritative %+v %v", value, err)
	}
}
