package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/remote"
	"ael/internal/securityfs"
	"ael/internal/store"
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/mattn/go-isatty"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

type onboardingIntent struct {
	Server      string `json:"server"`
	Profile     string `json:"profile"`
	Checkout    string `json:"checkout"`
	ProjectID   string `json:"project_id"`
	CreateName  string `json:"create_name"`
	OperationID string `json:"operation_id"`
}

func initCommand(a args) (any, error) {
	root := a.get("project", ".")
	binding, exists, err := remote.ReadBinding(root)
	if err != nil {
		return nil, err
	}
	interactive := isatty.IsTerminal(os.Stdin.Fd())
	reader := bufio.NewReader(os.Stdin)
	remoteRequested := exists || a.get("server", "") != "" || a.get("profile", "") != "" || a.get("create", "") != "" || a.get("remote-project", "") != ""
	if !remoteRequested && interactive && a.get("bundle-dir", "") == "" {
		if _, localErr := os.Stat(filepath.Join(root, ".ael/project.yaml")); os.IsNotExist(localErr) {
			server, err := initPrompt(reader, "Server 地址或 profile 名称（留空使用 Local）")
			if err != nil {
				return nil, err
			}
			if server != "" {
				remoteRequested = true
				if strings.Contains(server, "://") {
					a.Flags["server"] = server
				} else {
					a.Flags["profile"] = server
				}
			}
		}
	}
	if !remoteRequested {
		return bootstrap.InitProject(root, a.has("dry-run"), false, a.get("bundle-dir", ""))
	}
	allowed := []string{"project", "server", "profile", "remote-project", "create", "username", "password-stdin", "token-stdin", "config-dir", "ca-file", "prefix", "from", "bundle-dir", "host", "update-policy", "pin-version", "release-source", "dry-run", "json", "verbose", "no-color"}
	for flag, value := range a.Flags {
		if !slices.Contains(allowed, flag) {
			return nil, core.Error("UNKNOWN_FLAG", flag)
		}
		if !booleanFlag(flag) && value == "true" {
			return nil, core.Error("FLAG_VALUE_REQUIRED", flag)
		}
	}
	if a.has("dry-run") {
		return nil, core.Error("INIT_REMOTE_PREVIEW_UNSUPPORTED", "Remote init requires live authenticated choices; use version --check or update --dry-run for an existing binding")
	}
	if a.get("create", "") != "" && a.get("remote-project", "") != "" {
		return nil, core.Error("INIT_PROJECT_CHOICE_CONFLICT", "choose --create or --remote-project")
	}
	root, err = bootstrap.Canonical(root)
	if err != nil {
		return nil, err
	}
	transferred := false
	if err = remote.CheckRemoteCheckout(root); err != nil {
		if !exists {
			return nil, err
		}
		if err = bootstrap.VerifyTransferredCheckout(root); err != nil {
			return nil, err
		}
		transferred = true
	}
	config, err := remoteConfigDir(a)
	if err != nil {
		return nil, err
	}
	profileName := a.get("profile", binding.Profile)
	if profileName == "" {
		profileName = "p-" + store.Hash([]byte(a.get("server", "") + "\n" + root))[:20]
	}
	profile, profileErr := remote.LoadProfile(config, root, profileName)
	origin := a.get("server", profile.URL)
	origin, err = remote.ValidateOrigin(origin)
	if err != nil {
		return nil, err
	}
	if profileErr == nil && profile.URL != origin {
		return nil, core.Error("REMOTE_PROFILE_CONFLICT", "profile Server differs, existing credentials preserved")
	}
	if exists && (binding.Profile != profileName || (a.get("remote-project", binding.ProjectID) != binding.ProjectID)) {
		return nil, core.Error("REMOTE_BINDING_EXISTS", "existing binding was preserved")
	}
	authority := profile.CertificateAuthority
	if a.get("ca-file", "") != "" {
		authority, err = remote.ReadAuthority(a.get("ca-file", ""))
		if err != nil {
			return nil, err
		}
		if profileErr == nil && authority != profile.CertificateAuthority {
			return nil, core.Error("REMOTE_PROFILE_CONFLICT", "profile TLS trust differs, review it explicitly")
		}
	}
	transportProfile := remote.Profile{URL: origin, CertificateAuthority: authority}
	client, err := remote.NewHTTPClient(transportProfile)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	meta, err := remote.Metadata(client, transportProfile)
	if err != nil {
		return nil, err
	}
	username := a.get("username", "")
	if profileErr != nil && !a.has("token-stdin") && username == "" && interactive {
		username, err = initPrompt(reader, "Server 用户名")
		if err != nil {
			return nil, err
		}
	}
	if a.has("token-stdin") {
		if username != "" || a.has("password-stdin") {
			return nil, core.Error("INIT_AUTH_CONFLICT", "choose account login or project token")
		}
		token, err := initSecret(reader, interactive, true)
		if err != nil {
			return nil, err
		}
		if profileErr == nil && profile.Token != token {
			return nil, core.Error("REMOTE_PROFILE_CONFLICT", "existing profile credential was preserved")
		}
		profile = remote.Profile{URL: origin, Token: token, CertificateAuthority: authority}
	}
	if username == "" && profile.Token == "" {
		return nil, core.Error("INIT_AUTH_REQUIRED", "provide an existing --profile, --token-stdin, or --username with --password-stdin")
	}
	session, err := newServerSession(transportProfile)
	if err != nil {
		return nil, err
	}
	defer session.close()
	if username != "" {
		if !slices.Contains(meta.Capabilities, "onboarding") {
			return nil, core.Error("REMOTE_PROTOCOL_UNSUPPORTED", "Server must support onboarding for account-based initialization")
		}
		password, err := initSecret(reader, interactive, a.has("password-stdin"))
		if err != nil {
			return nil, err
		}
		if err = session.login(username, password); err != nil {
			return nil, err
		}
	} else {
		session.profile.Token = profile.Token
	}
	projectID := a.get("remote-project", binding.ProjectID)
	createName := strings.TrimSpace(a.get("create", ""))
	if exists {
		project, err := session.call(http.MethodGet, "/api/v1/projects/"+binding.ProjectID, nil)
		if err != nil {
			return nil, err
		}
		if createName != "" && project["name"] != createName {
			return nil, core.Error("REMOTE_BINDING_EXISTS", "checkout is already connected to a different project")
		}
		createName = ""
	}
	if projectID == "" && createName == "" {
		if !interactive {
			return nil, core.Error("INIT_PROJECT_REQUIRED", "provide stable --remote-project ID or --create NAME")
		}
		projectID, createName, err = initChooseProject(reader, session)
		if err != nil {
			return nil, err
		}
	}
	if createName != "" && username == "" {
		return nil, core.Error("INIT_ACCOUNT_LOGIN_REQUIRED", "creating a project requires account login with existing Server registration permission")
	}
	if profile.Token == "" {
		secret := make([]byte, 32)
		if _, err = rand.Read(secret); err != nil {
			return nil, err
		}
		profile = remote.Profile{URL: origin, Token: "ael_" + base64.RawURLEncoding.EncodeToString(secret), CertificateAuthority: authority}
	}
	if username == "" {
		if _, err = session.call(http.MethodGet, "/api/v1/projects/"+projectID, nil); err != nil {
			return nil, err
		}
	}
	if profileErr != nil {
		if err = remote.SaveProfileWithAuthority(config, root, profileName, origin, profile.Token, authority); err != nil {
			return nil, err
		}
	}
	if _, err = remote.LoadProfile(config, root, profileName); err != nil {
		return nil, err
	}
	intentDir, err := store.SafePath(config, "onboarding")
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(intentDir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(intentDir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || securityfs.NoACL(intentDir) != nil {
		return nil, core.Error("INIT_PRIVATE_DIRECTORY_REQUIRED", "onboarding records must be private")
	}
	intentPath, err := store.SafePath(intentDir, store.Hash([]byte(origin+"\n"+profileName+"\n"+root))+".json")
	if err != nil {
		return nil, err
	}
	unlock, err := lockAgentOperation(intentPath)
	if err != nil {
		return nil, err
	}
	defer unlock()
	intent := onboardingIntent{Server: origin, Profile: profileName, Checkout: root, ProjectID: projectID, CreateName: createName}
	if _, err = os.Lstat(intentPath); err == nil {
		if err = readAgentRequest(intentPath, &intent); err != nil {
			return nil, err
		}
		if intent.Server != origin || intent.Profile != profileName || intent.Checkout != root || (!exists && intent.CreateName != createName) || (projectID != "" && intent.ProjectID != projectID) {
			return nil, core.Error("INIT_PENDING_CONFLICT", "retry the original Server/project choice to finish pending initialization")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if intent.ProjectID == "" {
		intent.ProjectID, err = runnerOperation("project")
		if err != nil {
			return nil, err
		}
	}
	if intent.OperationID == "" {
		intent.OperationID, err = runnerOperation("init")
		if err != nil {
			return nil, err
		}
	}
	raw, _ := json.Marshal(intent)
	if err = store.Atomic(intentPath, raw, 0600); err != nil {
		return nil, err
	}
	needsGrant := username != "" && !exists
	if needsGrant && profileErr == nil {
		tokenSession, err := newServerSession(profile)
		if err != nil {
			return nil, err
		}
		accessible, accessErr := tokenSession.call(http.MethodGet, "/api/v1/projects", nil)
		tokenSession.close()
		if accessErr == nil {
			found := false
			if projects, ok := accessible["projects"].([]any); ok {
				for _, item := range projects {
					if project, ok := item.(map[string]any); ok && project["id"] == intent.ProjectID {
						found = true
					}
				}
			}
			if !found {
				return nil, core.Error("REMOTE_PROFILE_SCOPE_CONFLICT", "existing profile is scoped to another project, select a new profile name")
			}
			needsGrant = false
		}
	}
	if needsGrant {
		if _, err = session.call(http.MethodPost, "/api/v1/onboarding", map[string]string{"project_id": intent.ProjectID, "create_name": intent.CreateName, "operation_id": intent.OperationID, "token": profile.Token}); err != nil {
			return nil, err
		}
	}
	if transferred {
		binding, err = remote.ConnectTransferred(config, root, profileName, intent.ProjectID, nil, func() error { return bootstrap.VerifyTransferredCheckout(root) })
	} else {
		binding, err = remote.Connect(config, root, profileName, intent.ProjectID, nil)
	}
	if err != nil {
		return nil, err
	}
	result, err := finishRemoteInit(a, config, root, binding, meta, reader, interactive)
	if err != nil {
		return nil, core.Error("INIT_LOCAL_SETUP_PENDING", "Server project "+intent.ProjectID+" is saved; retry the same init after resolving "+err.Error())
	}
	if err = os.Remove(intentPath); err != nil {
		return nil, err
	}
	return result, nil
}

func initPrompt(reader *bufio.Reader, label string) (string, error) {
	fmt.Fprint(os.Stderr, label+" > ")
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", core.Error("INIT_INPUT_REQUIRED", "interactive input ended before setup completed")
	}
	return strings.TrimSpace(line), nil
}

func initSecret(reader *bufio.Reader, interactive, stdin bool) (string, error) {
	if !stdin && !interactive {
		return "", core.Error("INIT_PASSWORD_REQUIRED", "provide --password-stdin, credentials are never accepted as arguments")
	}
	if interactive {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		command := exec.CommandContext(ctx, "stty", "-echo")
		command.Stdin = os.Stdin
		err := command.Run()
		cancel()
		if err != nil {
			return "", core.Error("INIT_TERMINAL_UNAVAILABLE", "use credential stdin from a secure input source")
		}
		fmt.Fprint(os.Stderr, "凭据（输入隐藏） > ")
		defer func() {
			command := exec.Command("stty", "echo")
			command.Stdin = os.Stdin
			_ = command.Run()
			fmt.Fprintln(os.Stderr)
		}()
	}
	raw, err := reader.ReadSlice('\n')
	line := string(raw)
	if err != nil && err != io.EOF {
		return "", core.Error("INIT_AUTH_REQUIRED", "credential input unavailable")
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if line == "" || len(line) > 1024 || strings.ContainsAny(line, "\r\n") {
		return "", core.Error("INIT_AUTH_REQUIRED", "provide one credential line")
	}
	return line, nil
}

func initChooseProject(reader *bufio.Reader, session *serverSession) (string, string, error) {
	result, err := session.call(http.MethodGet, "/api/v1/projects", nil)
	if err != nil {
		return "", "", err
	}
	projects, ok := result["projects"].([]any)
	if !ok {
		return "", "", core.Error("REMOTE_RESPONSE_INVALID", "missing accessible projects")
	}
	search, err := initPrompt(reader, "搜索项目（留空显示全部，或输入 create 项目名称）")
	if err != nil {
		return "", "", err
	}
	if strings.HasPrefix(search, "create ") {
		return "", strings.TrimSpace(strings.TrimPrefix(search, "create ")), nil
	}
	filtered := []map[string]any{}
	for _, item := range projects {
		project, ok := item.(map[string]any)
		if !ok {
			return "", "", core.Error("REMOTE_RESPONSE_INVALID", "invalid project entry")
		}
		name, _ := project["name"].(string)
		id, _ := project["id"].(string)
		if strings.Contains(strings.ToLower(name+" "+id), strings.ToLower(search)) {
			filtered = append(filtered, project)
			fmt.Fprintf(os.Stderr, "%d  %s  [%s]\n", len(filtered), name, id)
		}
	}
	choice, err := initPrompt(reader, "选择序号或稳定项目 ID，或输入 create 项目名称")
	if err != nil {
		return "", "", err
	}
	if strings.HasPrefix(choice, "create ") {
		return "", strings.TrimSpace(strings.TrimPrefix(choice, "create ")), nil
	}
	if number, err := strconv.Atoi(choice); err == nil && number > 0 && number <= len(filtered) {
		return filtered[number-1]["id"].(string), "", nil
	}
	for _, project := range filtered {
		if choice == project["id"] {
			return choice, "", nil
		}
	}
	return "", "", core.Error("INIT_PROJECT_AMBIGUOUS", "choose an accessible stable project ID, names are never merged")
}
