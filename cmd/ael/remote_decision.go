package main

import (
	"ael/internal/core"
	"ael/internal/remote"
	"bufio"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Submit the user's decision to the same versioned endpoints as the panel.
// The runner's project credential never represents the reviewer.
func remoteDecision(cmd string, a args, config, project string, binding remote.Binding) (any, error) {
	username := a.get("username", "")
	if username == "" || !a.has("password-stdin") {
		return nil, core.Error("REVIEWER_LOGIN_REQUIRED", "provide the reviewer's username and credential via --password-stdin")
	}
	var body map[string]any
	if err := readAgentRequest(a.get("file", ""), &body); err != nil {
		return nil, err
	}
	if body == nil {
		return nil, core.Error("DECISION_REQUIRED", "provide the explicit decision for the reviewed version")
	}
	path := "/api/v1/projects/" + url.PathEscape(binding.ProjectID)
	if cmd == "task review" {
		path += "/reviews"
	} else {
		field, collection, action := "task", "tasks", "confirm"
		if cmd == "finding review" {
			field, collection, action = "finding", "findings", "decisions"
		}
		id := a.get(field, "")
		if id == "" || strings.ContainsAny(id, "/\\?#") || id == "." || id == ".." {
			return nil, core.Error("DECISION_TARGET_REQUIRED", "provide the reviewed "+field+" ID")
		}
		path += "/" + collection + "/" + url.PathEscape(id) + "/" + action
	}
	profile, err := remote.LoadProfile(config, project, binding.Profile)
	if err != nil {
		return nil, err
	}
	// A checkout may choose its own enrollment origin and TLS authority.
	// Reviewer passwords are scoped by trust provisioned outside the checkout.
	if strings.HasPrefix(binding.Profile, "clone-") {
		origin, err := remote.ValidateOrigin(os.Getenv("AEL_REVIEWER_ORIGIN"))
		if err != nil || origin != profile.URL {
			return nil, core.Error("REVIEWER_ORIGIN_REQUIRED", "the credential provider must set the trusted reviewer origin before supplying a password")
		}
		profile = remote.Profile{URL: origin}
		if ca := os.Getenv("AEL_REVIEWER_CA_FILE"); ca != "" {
			profile.CertificateAuthority, err = remote.ReadAuthority(ca)
			if err != nil {
				return nil, err
			}
		}
	}
	session, err := newServerSession(remote.Profile{URL: profile.URL, CertificateAuthority: profile.CertificateAuthority})
	if err != nil {
		return nil, err
	}
	defer session.close()
	password, err := initSecret(bufio.NewReader(os.Stdin), false, true)
	if err != nil {
		return nil, err
	}
	if err = session.login(username, password); err != nil {
		return nil, err
	}
	return session.call(http.MethodPost, path, body)
}
