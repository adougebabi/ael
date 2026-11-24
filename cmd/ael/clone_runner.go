package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/store"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

type cloneRunner struct {
	ID         string `json:"id"`
	Alias      string `json:"alias"`
	Repository string `json:"repository"`
	Version    string `json:"version"`
	Registered bool   `json:"registered"`
}

func cloneAlias(host, id string) string {
	host = strings.TrimSpace(host)
	if host == "" || len(host) > 160 || strings.IndexFunc(host, unicode.IsControl) >= 0 {
		return id
	}
	return host
}

func ensureCloneRunner(a args, root string, b *remote.Binding) (cloneRunner, error) {
	var runner cloneRunner
	access, err := readCloneAccess(root)
	if os.IsNotExist(err) {
		return runner, nil
	}
	if err != nil {
		return runner, err
	}
	if access.Project != b.ProjectID {
		return runner, core.Error("CLONE_PROJECT_MISMATCH", "repository enrollment and remote binding differ")
	}
	root, err = bootstrap.Canonical(root)
	if err != nil {
		return runner, err
	}
	config, err := remoteConfigDir(a)
	if err != nil {
		return runner, err
	}
	dir, err := remote.CloneStateDirectory(config, root)
	if err != nil {
		return runner, err
	}
	instance, err := remote.CheckoutInstance(root)
	if err != nil {
		return runner, err
	}
	key := store.Hash([]byte(access.URL + "\x00" + access.Project + "\x00" + access.Grant + "\x00" + instance))[:32]
	b.Profile = "clone-" + key
	state := filepath.Join(dir, key+".json")
	// Only first use writes. Subsequent commands also work with offline read caches.
	read := func() error {
		raw, err := os.ReadFile(state)
		if err != nil {
			return err
		}
		if json.Unmarshal(raw, &runner) != nil || !cloneIdentifier.MatchString(runner.ID) || runner.Repository != access.Repository {
			return core.Error("CLONE_STATE_INVALID", "restore this checkout's private runner state")
		}
		return nil
	}
	if err = read(); err == nil && runner.Registered {
		return runner, nil
	} else if err != nil && !os.IsNotExist(err) {
		return runner, err
	}
	end := time.Now().Add(30 * time.Second)
	var unlock func()
	for {
		unlock, err = lockAgentOperation(state)
		if err == nil {
			break
		}
		if !strings.Contains(err.Error(), "AGENT_OPERATION_BUSY") || time.Now().After(end) {
			return runner, err
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer unlock()
	err = read()
	if err == nil && runner.Registered {
		return runner, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return runner, err
	}
	profile, err := remote.LoadProfile(config, root, b.Profile)
	if err != nil {
		token, err := cloneSecret("ael_")
		if err != nil {
			return runner, err
		}
		profile = remote.Profile{URL: access.URL, CertificateAuthority: access.CA, Token: token}
		if err = remote.SaveProfileWithAuthority(config, root, b.Profile, profile.URL, profile.Token, profile.CertificateAuthority); err != nil {
			return runner, err
		}
	}
	if profile.URL != access.URL || profile.CertificateAuthority != access.CA {
		return runner, core.Error("CLONE_PROFILE_CONFLICT", "private enrollment target changed")
	}
	save := func() error {
		raw, err := json.Marshal(runner)
		if err != nil {
			return err
		}
		return store.Atomic(state, raw, 0600)
	}
	if runner.ID == "" {
		id := "runner-" + store.Hash([]byte(profile.Token))[:24]
		host, _ := os.Hostname()
		runner = cloneRunner{ID: id, Alias: cloneAlias(host, id), Repository: access.Repository, Version: bootstrap.Version}
		if err = save(); err != nil {
			return runner, err
		}
	}
	session, err := newServerSession(remote.Profile{URL: access.URL, CertificateAuthority: access.CA, Token: access.Grant})
	if err != nil {
		return runner, err
	}
	defer session.close()
	if _, err = session.call(http.MethodPost, "/api/v1/projects/"+b.ProjectID+"/clone-enroll", map[string]string{"token": profile.Token}); err != nil {
		return runner, err
	}
	body := map[string]any{"id": runner.ID, "alias": runner.Alias, "machine": runner.Alias, "checkout_id": runner.ID, "workspace_id": runner.ID, "repository": runner.Repository, "version": runner.Version, "capabilities": []string{"development", "scenario", "model"}, "expected_revision": "absent", "operation_id": runner.ID, "idempotency_key": runner.ID}
	if _, err = remote.Call(config, root, *b, http.MethodPost, "/runners", body, nil); err != nil {
		return runner, err
	}
	runner.Registered = true
	return runner, save()
}

func (r cloneRunner) defaults(cmd string, a args) {
	if r.ID == "" {
		return
	}
	set := func(key, value string) {
		if a.get(key, "") == "" {
			a.Flags[key] = value
		}
	}
	switch cmd {
	case "session start", "recovery save", "runner show", "runner next", "runner claim", "runner recover", "runner queue":
		set("runner", r.ID)
	case "candidate capture":
		set("runner", r.ID)
		set("checkout", r.ID)
		set("workspace", r.ID)
		set("repository", r.Repository)
	case "runner submit", "runner observe":
		if a.get("lease-file", "") != "" && a.get("file", "") == "" {
			set("repository", r.Repository)
		}
	}
}
