package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"regexp"
)

// Browser sessions are command-scoped. Cookies and passwords stay in memory.
type serverSession struct {
	profile remote.Profile
	client  *http.Client
	csrf    string
}

func newServerSession(profile remote.Profile) (*serverSession, error) {
	client, err := remote.NewHTTPClient(profile)
	if err != nil {
		return nil, err
	}
	client.Jar, _ = cookiejar.New(nil)
	return &serverSession{profile: profile, client: client}, nil
}

func (s *serverSession) call(method, path string, body any) (map[string]any, error) {
	var data io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		data = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, s.profile.URL+path, data)
	if err != nil {
		return nil, core.Error("REMOTE_REQUEST_INVALID", "invalid Server request")
	}
	request.Header.Set("Origin", s.profile.URL)
	request.Header.Set("X-AEL-Protocol", "1")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if s.profile.Token != "" {
		request.Header.Set("Authorization", "Bearer "+s.profile.Token)
	}
	if s.csrf != "" {
		request.Header.Set("X-AEL-CSRF", s.csrf)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return nil, core.Error("SERVER_UNAVAILABLE", "server administration connection failed")
	}
	defer response.Body.Close()
	var result map[string]any
	if json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result) != nil || result == nil {
		return nil, core.Error("SERVER_RESPONSE_INVALID", "invalid control response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code, _ := result["code"].(string)
		if !serverSessionCode.MatchString(code) {
			code = "SERVER_ADMIN_REJECTED"
		}
		return nil, core.Error(code, "Server request rejected")
	}
	return result, nil
}

var serverSessionCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

func (s *serverSession) login(username, password string) error {
	result, err := s.call(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": username, "password": password})
	if err != nil {
		return err
	}
	s.csrf, _ = result["csrf"].(string)
	if s.csrf == "" {
		return core.Error("REMOTE_RESPONSE_INVALID", "login omitted CSRF binding")
	}
	return nil
}

func (s *serverSession) close() {
	if s.csrf != "" {
		_, _ = s.call(http.MethodPost, "/api/v1/auth/logout", map[string]string{})
	}
	s.client.CloseIdleConnections()
}
