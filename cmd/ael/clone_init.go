package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/store"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"slices"
)

const cloneAccessFile = ".ael/runner-access.json"

var cloneIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

type cloneAccess struct {
	Schema     int    `json:"schema"`
	Project    string `json:"project_id"`
	Repository string `json:"repository"`
	URL        string `json:"url"`
	CA         string `json:"ca_pem,omitempty"`
	Grant      string `json:"enrollment_grant"`
}

func cloneSecret(prefix string) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
func readCloneAccess(root string) (cloneAccess, error) {
	var access cloneAccess
	path, err := store.SafePath(root, cloneAccessFile)
	if err != nil {
		return access, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return access, err
	}
	if json.Unmarshal(raw, &access) != nil || access.Schema != 1 || !cloneIdentifier.MatchString(access.Project) || !cloneIdentifier.MatchString(access.Repository) || len(access.Grant) != 48 || access.Grant[:5] != "join_" {
		return access, core.Error("CLONE_ACCESS_INVALID", "invalid repository enrollment descriptor")
	}
	_, err = remote.ValidateOrigin(access.URL)
	return access, err
}
func publishCloneAccess(a args, session *serverSession, root, config string, b remote.Binding, profile remote.Profile, grant string) error {
	prefix, err := bootstrap.InstallationPrefix(a.get("prefix", ""))
	if err != nil {
		return err
	}
	if err = bootstrap.InstallCloneRuntime(root, prefix); err != nil {
		return err
	}
	previous, err := readCloneAccess(root)
	if err == nil {
		if previous.Project != b.ProjectID || previous.URL != profile.URL || previous.CA != profile.CertificateAuthority || a.get("repository", previous.Repository) != previous.Repository {
			return core.Error("CLONE_ACCESS_CONFLICT", "existing descriptor belongs to another target")
		}
		// Re-authorize the same repository grant on an explicit admin login,
		// including after server restore. Never derive it from a checkout identity.
		if session.csrf == "" {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if session.csrf == "" {
		return core.Error("CLONE_ADMIN_REQUIRED", "first project initialization requires administrator login to authorize clone enrollment")
	}
	repos, err := remote.Call(config, root, b, http.MethodGet, "/repositories", nil, nil)
	if err != nil {
		return err
	}
	if repos["online"] != true {
		return core.Error("REMOTE_UNAVAILABLE", "live repository list required")
	}
	var ids []string
	items, ok := repos["items"].([]any)
	if !ok {
		return core.Error("REMOTE_RESPONSE_INVALID", "missing repository list")
	}
	for _, item := range items {
		v, _ := item.(map[string]any)
		id, _ := v["id"].(string)
		ids = append(ids, id)
	}
	id := a.get("repository", previous.Repository)
	if id == "" {
		switch len(ids) {
		case 0:
			id = b.ProjectID
		case 1:
			id = ids[0]
		default:
			return core.Error("CLONE_REPOSITORY_REQUIRED", "multiple registered repositories; select one using --repository ID")
		}
	}
	if !cloneIdentifier.MatchString(id) {
		return core.Error("CLONE_REPOSITORY_INVALID", "invalid repository ID")
	}
	if !slices.Contains(ids, id) {
		op := "clone-repo-" + store.Hash([]byte(id))[:24]
		_, err = remote.Call(config, root, b, http.MethodPost, "/repositories", map[string]string{"id": id, "expected_revision": "absent", "operation_id": op, "idempotency_key": op}, nil)
		if err != nil {
			return err
		}
	}
	if previous.Grant != "" {
		grant = previous.Grant
	}
	if _, err = session.call(http.MethodPost, "/api/v1/projects/"+b.ProjectID+"/clone-grants", map[string]string{"grant": grant}); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cloneAccess{1, b.ProjectID, id, profile.URL, profile.CertificateAuthority, grant}, "", "  ")
	if err != nil {
		return err
	}
	path, err := store.SafePath(root, cloneAccessFile)
	if err != nil {
		return err
	}
	return store.Atomic(path, append(raw, '\n'), 0600)
}
