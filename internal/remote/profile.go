// Package remote holds client-side configuration and transport for the single
// remote authority. It never opens a local task runtime as a fallback.
package remote

import (
	"ael/internal/projectmode"
	"ael/internal/securityfs"
	"ael/internal/store"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

type Profile struct {
	CertificateAuthority string `json:"ca_pem,omitempty"`
	URL                  string `json:"url"`
	Token                string `json:"token"`
}

type Binding struct {
	APIProtocol   int    `json:"api_protocol,omitempty"`
	SkillProtocol int    `json:"skill_protocol,omitempty"`
	Schema        int    `json:"schema"`
	Profile       string `json:"profile"`
	ProjectID     string `json:"project_id"`
}

func cleanURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", errors.New("REMOTE_URL_INVALID: use an HTTPS origin or loopback HTTP origin")
	}
	if u.Scheme == "http" {
		ip := net.ParseIP(u.Hostname())
		if !strings.EqualFold(u.Hostname(), "localhost") && (ip == nil || !ip.IsLoopback()) {
			return "", errors.New("REMOTE_URL_INSECURE: non-loopback servers require HTTPS")
		}
	}
	return u.String(), nil
}

func ValidateOrigin(raw string) (string, error) { return cleanURL(raw) }

func CheckRemoteCheckout(root string) error { return checkConnectLocal(root, nil) }

// resolvePath follows existing ancestors to detect paths which enter a
// checkout through a symlink, even when the final config directory is new.
func resolvePath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("REMOTE_CONFIG_DIR_REQUIRED: use an absolute config directory")
	}
	path = filepath.Clean(path)
	ancestor := path
	for {
		if _, err := os.Lstat(ancestor); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return "", errors.New("REMOTE_CONFIG_UNAVAILABLE")
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", errors.New("REMOTE_CONFIG_UNAVAILABLE")
		}
		ancestor = parent
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", errors.New("REMOTE_CONFIG_UNAVAILABLE")
	}
	rest, err := filepath.Rel(ancestor, path)
	if err != nil {
		return "", errors.New("REMOTE_CONFIG_UNAVAILABLE")
	}
	return filepath.Join(resolved, rest), nil
}

func outsideCheckout(config, project string) (string, error) {
	resolved, err := resolvePath(config)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(project)
	if err != nil {
		return "", errors.New("REMOTE_PROJECT_UNAVAILABLE")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", errors.New("REMOTE_PROJECT_UNAVAILABLE")
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return "", errors.New("REMOTE_CREDENTIALS_IN_CHECKOUT: use a configuration directory outside the project")
	}
	for path := resolved; ; path = filepath.Dir(path) {
		_, err := os.Lstat(filepath.Join(path, ".git"))
		if err == nil {
			return "", errors.New("REMOTE_CREDENTIALS_IN_CHECKOUT: use a configuration directory outside Git checkouts")
		}
		if !os.IsNotExist(err) {
			return "", errors.New("REMOTE_CONFIG_UNAVAILABLE")
		}
		parent := filepath.Dir(path)
		if parent == path {
			break
		}
	}
	return resolved, nil
}

func SaveProfile(config, project, name, rawURL, token string) error {
	return SaveProfileWithAuthority(config, project, name, rawURL, token, "")
}

func SaveProfileWithAuthority(config, project, name, rawURL, token, authority string) error {
	authority, authorityErr := canonicalAuthority(authority)
	if authorityErr != nil {
		return authorityErr
	}
	if !identifier.MatchString(name) || token == "" || len(token) > 512 || strings.ContainsAny(token, "\r\n") {
		return errors.New("REMOTE_PROFILE_INVALID")
	}
	origin, err := cleanURL(rawURL)
	if err != nil {
		return err
	}
	config, err = outsideCheckout(config, project)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(config, 0700); err != nil {
		return errors.New("REMOTE_CONFIG_UNAVAILABLE")
	}
	info, err := os.Lstat(config)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("REMOTE_CONFIG_PERMISSIONS: config directory must be private")
	}
	if err = securityfs.NoACL(config); err != nil {
		return errors.New("REMOTE_CONFIG_PERMISSIONS: config directory ACL is not supported")
	}
	profiles, err := store.SafePath(config, "profiles")
	if err != nil {
		return errors.New("REMOTE_CONFIG_UNAVAILABLE")
	}
	if err = os.MkdirAll(profiles, 0700); err != nil {
		return errors.New("REMOTE_CONFIG_UNAVAILABLE")
	}
	info, err = os.Lstat(profiles)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("REMOTE_CONFIG_PERMISSIONS: profiles directory must be private")
	}
	if err = securityfs.NoACL(profiles); err != nil {
		return errors.New("REMOTE_CONFIG_PERMISSIONS: profiles directory ACL is not supported")
	}
	path, err := store.SafePath(config, filepath.Join("profiles", name+".json"))
	if err != nil {
		return errors.New("REMOTE_CONFIG_UNAVAILABLE")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("REMOTE_PROFILE_EXISTS_OR_UNAVAILABLE")
	}
	data, _ := json.Marshal(Profile{URL: origin, Token: token, CertificateAuthority: authority})
	_, writeErr := f.Write(append(data, '\n'))
	if writeErr == nil {
		writeErr = f.Sync()
	}
	if closeErr := f.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr == nil {
		writeErr = securityfs.NoACL(path)
	}
	if writeErr == nil {
		writeErr = securityfs.SingleLinkRegular(path)
	}
	if writeErr != nil {
		_ = os.Remove(path)
		return errors.New("REMOTE_CONFIG_UNAVAILABLE")
	}
	return nil
}

func LoadProfile(config, project, name string) (Profile, error) {
	var p Profile
	if !identifier.MatchString(name) {
		return p, errors.New("REMOTE_PROFILE_INVALID")
	}
	config, err := outsideCheckout(config, project)
	if err != nil {
		return p, err
	}
	for _, dir := range []string{config, filepath.Join(config, "profiles")} {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || securityfs.NoACL(dir) != nil {
			return p, errors.New("REMOTE_CONFIG_PERMISSIONS: profile directories must be private")
		}
	}
	path, err := store.SafePath(config, filepath.Join("profiles", name+".json"))
	if err != nil {
		return p, errors.New("REMOTE_PROFILE_UNAVAILABLE")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return p, errors.New("REMOTE_PROFILE_UNAVAILABLE")
	}
	if securityfs.NoACL(path) != nil || securityfs.SingleLinkRegular(path) != nil {
		return p, errors.New("REMOTE_CONFIG_PERMISSIONS: profile must be private and not linked")
	}
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &p) != nil || p.Token == "" {
		return Profile{}, errors.New("REMOTE_PROFILE_INVALID")
	}
	if p.URL, err = cleanURL(p.URL); err != nil {
		return Profile{}, err
	}
	if err = validateAuthority(p.CertificateAuthority); err != nil {
		return Profile{}, err
	}
	return p, nil
}

func ReadBinding(project string) (Binding, bool, error) {
	binding, exists, _, err := ReadBindingSnapshot(project)
	return binding, exists, err
}

// ReadBindingSnapshot binds project setup to the exact validated input bytes.
func ReadBindingSnapshot(project string) (Binding, bool, string, error) {
	var b Binding
	root, err := filepath.Abs(project)
	if err != nil {
		return b, false, "", errors.New("REMOTE_PROJECT_UNAVAILABLE")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return b, false, "", errors.New("REMOTE_PROJECT_UNAVAILABLE")
	}
	path, err := store.SafePath(root, ".ael/remote.json")
	if err != nil {
		return b, true, "", errors.New("REMOTE_BINDING_INVALID")
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return b, false, "", nil
	}
	if err != nil || json.Unmarshal(raw, &b) != nil || b.Schema != 1 || !identifier.MatchString(b.Profile) || !identifier.MatchString(b.ProjectID) {
		return Binding{}, true, "", errors.New("REMOTE_BINDING_INVALID: local fallback is disabled")
	}
	if b.APIProtocol == 0 {
		b.APIProtocol = APIProtocol
	}
	if b.SkillProtocol == 0 {
		b.SkillProtocol = SkillProtocol
	}
	if err = CheckClientProtocols(b); err != nil {
		return b, true, "", err
	}
	return b, true, store.Hash(raw), nil
}

func request(client *http.Client, p Profile, path string, auth bool) (*http.Response, error) {
	if client == nil {
		var err error
		client, err = NewHTTPClient(p)
		if err != nil {
			return nil, err
		}
	}
	r, err := http.NewRequest(http.MethodGet, p.URL+path, nil)
	if err != nil {
		return nil, errors.New("REMOTE_REQUEST_INVALID")
	}
	if auth {
		r.Header.Set("Authorization", "Bearer "+p.Token)
	}
	response, err := client.Do(r)
	if err != nil {
		return nil, errors.New("REMOTE_UNAVAILABLE: server unreachable; local fallback is disabled")
	}
	return response, nil
}

func Connect(config, project, name, projectID string, client *http.Client) (Binding, error) {
	return connect(config, project, name, projectID, client, nil)
}

// ConnectTransferred retains normal profile, protocol, project access and
// binding checks. The caller verifies its frozen Local source under the same
// project-mode lock as the final binding installation.
func ConnectTransferred(config, project, name, projectID string, client *http.Client, verifyLocal func() error) (Binding, error) {
	if verifyLocal == nil {
		return Binding{}, errors.New("TRANSFER_FREEZE_REQUIRED")
	}
	return connect(config, project, name, projectID, client, verifyLocal)
}

func connect(config, project, name, projectID string, client *http.Client, verifyLocal func() error) (Binding, error) {
	if !identifier.MatchString(name) || !identifier.MatchString(projectID) {
		return Binding{}, errors.New("REMOTE_BINDING_INVALID")
	}
	p, err := LoadProfile(config, project, name)
	if err != nil {
		return Binding{}, err
	}
	wanted := Binding{Schema: 1, Profile: name, ProjectID: projectID, APIProtocol: APIProtocol, SkillProtocol: SkillProtocol}
	if err = CheckClientProtocols(wanted); err != nil {
		return Binding{}, err
	}
	existing, exists, err := ReadBinding(project)
	if err != nil {
		return Binding{}, err
	}
	if exists && existing != wanted {
		return Binding{}, errors.New("REMOTE_BINDING_EXISTS: existing binding was preserved")
	}
	root, err := filepath.Abs(project)
	if err != nil {
		return Binding{}, errors.New("REMOTE_PROJECT_UNAVAILABLE")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return Binding{}, errors.New("REMOTE_PROJECT_UNAVAILABLE")
	}
	if err = checkConnectLocal(root, verifyLocal); err != nil {
		return Binding{}, err
	}
	if err = checkCapability(client, p, "remote_tasks"); err != nil {
		return Binding{}, err
	}
	response, err := request(client, p, "/api/v1/projects/"+projectID, true)
	if err != nil {
		return Binding{}, err
	}
	var found struct {
		ID   string `json:"id"`
		Role string `json:"role"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&found)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || decodeErr != nil || found.ID != projectID || (found.Role != "admin" && found.Role != "executor" && found.Role != "reviewer" && found.Role != "reader") {
		return Binding{}, errors.New("REMOTE_PROJECT_UNAVAILABLE: access denied or project not found")
	}
	unlock, err := projectmode.Lock(root, true)
	if err != nil {
		return Binding{}, err
	}
	defer unlock()
	current, currentExists, err := ReadBinding(root)
	if err != nil {
		return Binding{}, err
	}
	if currentExists && current != wanted {
		return Binding{}, errors.New("REMOTE_BINDING_EXISTS: existing binding was preserved")
	}
	if err = checkConnectLocal(root, verifyLocal); err != nil {
		return Binding{}, err
	}
	if currentExists {
		return current, nil
	}
	dir, err := store.SafePath(root, ".ael")
	if err != nil {
		return Binding{}, errors.New("REMOTE_PROJECT_UNAVAILABLE")
	}
	if err = os.MkdirAll(dir, 0755); err != nil {
		return Binding{}, errors.New("REMOTE_PROJECT_UNAVAILABLE")
	}
	path, err := store.SafePath(root, ".ael/remote.json")
	if err != nil {
		return Binding{}, errors.New("REMOTE_PROJECT_UNAVAILABLE")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return Binding{}, errors.New("REMOTE_BINDING_EXISTS: existing binding was preserved")
	}
	raw, _ := json.Marshal(wanted)
	_, writeErr := f.Write(append(raw, '\n'))
	if writeErr == nil {
		writeErr = f.Sync()
	}
	if closeErr := f.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		_ = os.Remove(path)
		return Binding{}, errors.New("REMOTE_PROJECT_UNAVAILABLE")
	}
	return wanted, nil
}

func checkConnectLocal(root string, verifyLocal func() error) error {
	if verifyLocal != nil {
		return verifyLocal()
	}
	for _, rel := range []string{".ael/project.yaml", ".ael/work-items", ".ael/records", ".ael/outcomes"} {
		path, e := store.SafePath(root, rel)
		if e != nil {
			return errors.New("REMOTE_PROJECT_UNAVAILABLE")
		}
		if _, e = os.Lstat(path); e == nil {
			return errors.New("LOCAL_PROJECT_EXISTS: do not switch an existing local project")
		} else if !os.IsNotExist(e) {
			return errors.New("REMOTE_PROJECT_UNAVAILABLE")
		}
	}
	return nil
}

// CloneStateDirectory holds machine-private enrollment receipts, outside Git.
func CloneStateDirectory(config, root string) (string, error) {
	config, err := outsideCheckout(config, root)
	if err != nil {
		return "", err
	}
	for _, dir := range []string{config, filepath.Join(config, "clone-runners")} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return "", err
		}
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || securityfs.NoACL(dir) != nil {
			return "", errors.New("REMOTE_CONFIG_PERMISSIONS: clone runner directory must be private")
		}
	}
	return filepath.Join(config, "clone-runners"), nil
}
