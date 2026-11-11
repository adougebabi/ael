package remote

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var responseCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// Call contacts only the configured server and never falls back to a local
// authority. Client file paths are resolved before this function is called.
func Call(config, project string, binding Binding, method, suffix string, payload any, client *http.Client) (map[string]any, error) {
	profile, err := LoadProfile(config, project, binding.Profile)
	if err != nil {
		return nil, err
	}
	if binding.Schema != 1 || !identifier.MatchString(binding.ProjectID) || (suffix != "" && suffix[0] != '/') {
		return nil, errors.New("REMOTE_REQUEST_INVALID")
	}
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, errors.New("REMOTE_REQUEST_INVALID")
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, profile.URL+"/api/v1/projects/"+binding.ProjectID+suffix, body)
	if err != nil {
		return nil, errors.New("REMOTE_REQUEST_INVALID")
	}
	req.Header.Set("Authorization", "Bearer "+profile.Token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-AEL-Protocol", "1")
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	if method != http.MethodGet {
		capability := "remote_tasks"
		if strings.Contains(suffix, "/plan/") {
			capability = "remote_plans"
		}
		if strings.HasPrefix(suffix, "/artifacts") {
			capability = "artifacts"
		}
		if strings.HasPrefix(suffix, "/repositories") || strings.HasPrefix(suffix, "/candidates") {
			capability = "portable_candidates"
		}
		if strings.HasPrefix(suffix, "/contexts") {
			capability = "portable_contexts"
		}
		if strings.HasPrefix(suffix, "/runners") {
			capability = "remote_runners"
		}
		if err = checkCapability(client, profile, capability); err != nil {
			return nil, err
		}
	}
	response, err := client.Do(req)
	if err != nil {
		if method == http.MethodGet && cacheableRemoteRead(suffix) {
			if cached, cacheErr := readCache(config, profile, binding, suffix); cacheErr == nil {
				cached.Data["mode"], cached.Data["online"], cached.Data["authoritative"] = "remote", false, false
				cached.Data["cached_at"] = cached.ObservedAt
				return cached.Data, nil
			}
		}
		return nil, errors.New("REMOTE_UNAVAILABLE: server unreachable; local fallback is disabled")
	}
	defer response.Body.Close()
	var result map[string]any
	decoder := json.NewDecoder(io.LimitReader(response.Body, 2<<20))
	decoder.UseNumber()
	if err = decoder.Decode(&result); err != nil || result == nil {
		return nil, errors.New("REMOTE_RESPONSE_INVALID")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code, _ := result["code"].(string)
		if !responseCode.MatchString(code) {
			code = "REMOTE_REJECTED"
		}
		if detail, ok := result["detail"].(string); ok && len(detail) <= 500 && !strings.ContainsAny(detail, "\r\n\x00") && (code == "KNOWLEDGE_MISSING" || code == "KNOWLEDGE_CONFLICT" || code == "CONTEXT_SOURCE_MISMATCH") {
			return nil, fmt.Errorf("%s: %s", code, detail)
		}
		return nil, fmt.Errorf("%s: remote server rejected the request", code)
	}
	observed := time.Now().UTC().Format(time.RFC3339Nano)
	if method == http.MethodGet && cacheableRemoteRead(suffix) {
		result["cache_saved"] = writeCache(config, profile, binding, suffix, observed, result) == nil
	}
	result["mode"], result["online"], result["authoritative"], result["observed_at"] = "remote", true, result["projection_status"] != "blocked", observed
	return result, nil
}

func cacheableRemoteRead(suffix string) bool {
	return !strings.Contains(suffix, "/artifacts") && !strings.HasPrefix(suffix, "/repositories") && !strings.HasPrefix(suffix, "/candidates") && !strings.HasPrefix(suffix, "/observations") && !strings.HasPrefix(suffix, "/portable-operations") && !strings.HasPrefix(suffix, "/runners") && !strings.Contains(suffix, "/context")
}

// Artifact content is always fetched live: cached bytes cannot prove that the
// original still exists or that the caller remains authorized.
func ArtifactContent(config, project string, binding Binding, id string, client *http.Client) ([]byte, error) {
	if binding.Schema != 1 || !identifier.MatchString(binding.ProjectID) || !identifier.MatchString(id) {
		return nil, errors.New("REMOTE_REQUEST_INVALID")
	}
	profile, err := LoadProfile(config, project, binding.Profile)
	if err != nil {
		return nil, err
	}
	response, err := request(client, profile, "/api/v1/projects/"+binding.ProjectID+"/artifacts/"+id+"/content", true)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var result map[string]any
		_ = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result)
		code, _ := result["code"].(string)
		if !responseCode.MatchString(code) {
			code = "REMOTE_REJECTED"
		}
		return nil, fmt.Errorf("%s: artifact content unavailable", code)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (10<<20)+1))
	if err != nil || len(data) > 10<<20 {
		return nil, errors.New("REMOTE_RESPONSE_INVALID")
	}
	return data, nil
}
