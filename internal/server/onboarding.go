package server

import (
	"ael/internal/knowledge"
	"ael/internal/release"
	"ael/internal/store"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"golang.org/x/mod/semver"
	"net/http"
	"strings"
	"time"
)

type onboardingInput struct {
	ProjectID   string `json:"project_id"`
	CreateName  string `json:"create_name,omitempty"`
	OperationID string `json:"operation_id"`
	Token       string `json:"token"`
}

type onboardingClientReport struct {
	SessionID          string            `json:"session_id,omitempty"`
	Runner             string            `json:"runner,omitempty"`
	Build              string            `json:"cli_build,omitempty"`
	ClientID           string            `json:"client_id"`
	CLIVersion         string            `json:"cli_version"`
	SkillVersion       string            `json:"skill_version"`
	UpdatePolicy       string            `json:"update_policy"`
	SpecificationState string            `json:"specification_state"`
	Hooks              map[string]string `json:"hooks"`
}

func (h *standaloneHandler) onboardingClient(w http.ResponseWriter, r *http.Request, principal standalonePrincipal, projectID string) {
	if _, err := h.server.role(principal, projectID); err != nil {
		standaloneFail(w, http.StatusNotFound, "PROJECT_NOT_FOUND")
		return
	}
	service, err := h.server.projectService(projectID)
	if err != nil {
		portableError(w, err)
		return
	}
	if r.Method == http.MethodGet {
		rows, err := service.Store.DB.Query("SELECT body FROM metadata WHERE key LIKE 'client-onboarding:%' ORDER BY json_extract(body,'$.reported_at') DESC LIMIT 1000")
		if err != nil {
			portableError(w, err)
			return
		}
		defer rows.Close()
		clients := []map[string]any{}
		for rows.Next() {
			var body string
			if err = rows.Scan(&body); err != nil {
				portableError(w, err)
				return
			}
			var client map[string]any
			if json.Unmarshal([]byte(body), &client) != nil {
				portableError(w, errors.New("CANDIDATE_STORAGE_ERROR"))
				return
			}
			version, _ := client["cli_version"].(string)
			client["compatible"] = release.ValidVersion(version) && semver.Compare("v"+version, "v"+h.minimumClient) >= 0
			client["minimum_client_version"] = h.minimumClient
			clients = append(clients, client)
		}
		if err = rows.Err(); err != nil {
			portableError(w, err)
			return
		}
		standaloneReply(w, http.StatusOK, map[string]any{"clients": clients, "report_kind": "last_reported", "host_execution_state": "unknown"})
		return
	}
	if r.Method != http.MethodPost {
		standaloneFail(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	var input onboardingClientReport
	if decode(r, &input) != nil || !standaloneID.MatchString(input.ClientID) || len(input.CLIVersion) > 80 || len(input.SkillVersion) > 80 || len(input.SpecificationState) > 100 || len(input.Hooks) > 2 || len(input.SessionID) > 200 || len(input.Runner) > 64 || len(input.Build) > 200 || strings.ContainsAny(input.SessionID+input.Runner, "/\\\r\n\x00") || (input.UpdatePolicy != "notify" && input.UpdatePolicy != "auto" && input.UpdatePolicy != "pinned") {
		standaloneFail(w, http.StatusBadRequest, "ONBOARDING_INVALID")
		return
	}
	for host, status := range input.Hooks {
		if (host != "codex" && host != "claude") || (status != "pending_host_trust" && status != "unsupported_or_unavailable") {
			standaloneFail(w, http.StatusBadRequest, "ONBOARDING_INVALID")
			return
		}
	}
	data := map[string]any{"client_id": input.ClientID, "actor": principal.Username, "session_id": input.SessionID, "runner": input.Runner, "cli_build": input.Build, "cli_version": input.CLIVersion, "skill_version": input.SkillVersion, "update_policy": input.UpdatePolicy, "specification_state": input.SpecificationState, "hooks": input.Hooks, "reported_at": time.Now().UTC(), "report_kind": "last_reported"}
	raw, _ := json.Marshal(data)
	err = service.Store.Write(func(tx *store.Tx) error {
		_, err := tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", "client-onboarding:"+principal.UserID+":"+input.ClientID, string(raw))
		return err
	})
	if err != nil {
		portableError(w, err)
		return
	}
	standaloneReply(w, http.StatusOK, map[string]any{"recorded": true, "report_kind": "last_reported"})
}

// The CLI retains its random credential in private storage before this call.
// A lost response can replay the same grant without storing plaintext on Server.
func (s *Standalone) onboard(principal standalonePrincipal, input onboardingInput) (map[string]any, error) {
	input.CreateName = strings.TrimSpace(input.CreateName)
	secret, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(input.Token, "ael_"))
	if err != nil || len(secret) != 32 || !strings.HasPrefix(input.Token, "ael_") || !standaloneID.MatchString(input.ProjectID) || !standaloneID.MatchString(input.OperationID) || len(input.CreateName) > 200 {
		return nil, errors.New("ONBOARDING_INVALID")
	}
	if input.CreateName != "" && !principal.Admin {
		return nil, errors.New("FORBIDDEN")
	}
	if input.CreateName == "" {
		if _, err = s.role(principal, input.ProjectID); err != nil {
			return nil, errors.New("PROJECT_NOT_FOUND")
		}
	}
	result := map[string]any{"project_id": input.ProjectID}
	digest, _ := json.Marshal(input)
	requestHash := store.Hash(digest)
	key := "onboarding:" + principal.UserID + ":" + input.OperationID
	err = s.store.Write(func(tx *store.Tx) error {
		var saved string
		readErr := tx.Row("SELECT body FROM metadata WHERE key=?", key).Scan(&saved)
		if readErr == nil {
			if saved != requestHash {
				return errors.New("IDEMPOTENCY_CONFLICT")
			}
			var revoked int
			if err := tx.Row("SELECT revoked FROM server_tokens WHERE user_id=? AND project_id=? AND secret_hash=?", principal.UserID, input.ProjectID, standaloneHash(input.Token)).Scan(&revoked); err != nil || revoked != 0 {
				return errors.New("ONBOARDING_GRANT_REVOKED")
			}
			return nil
		}
		if readErr != sql.ErrNoRows {
			return readErr
		}
		if input.CreateName != "" {
			if _, err := tx.Exec("INSERT INTO server_projects(id,name) VALUES(?,?)", input.ProjectID, input.CreateName); err != nil {
				return errors.New("PROJECT_ID_CONFLICT")
			}
		}
		if principal.Admin {
			if _, err := tx.Exec("INSERT INTO server_members(project_id,user_id,role) VALUES(?,?,'executor') ON CONFLICT(project_id,user_id) DO NOTHING", input.ProjectID, principal.UserID); err != nil {
				return err
			}
		}
		tokenID, err := standaloneRandom("token_")
		if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO server_tokens(id,secret_hash,user_id,project_id,label) VALUES(?,?,?,?,?)", tokenID, standaloneHash(input.Token), principal.UserID, input.ProjectID, "CLI initialization"); err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?)", key, requestHash)
		return err
	})
	return result, err
}

func (h *standaloneHandler) onboarding(w http.ResponseWriter, r *http.Request, principal standalonePrincipal, method string) {
	if r.Method != http.MethodPost || method != "session" {
		standaloneFail(w, http.StatusForbidden, "FORBIDDEN")
		return
	}
	var input onboardingInput
	if decode(r, &input) != nil {
		standaloneFail(w, http.StatusBadRequest, "ONBOARDING_INVALID")
		return
	}
	result, err := h.server.onboard(principal, input)
	if err != nil {
		portableError(w, err)
		return
	}
	standaloneReply(w, http.StatusOK, result)
}

func (h *standaloneHandler) onboardingConfiguration(w http.ResponseWriter, r *http.Request, principal standalonePrincipal, projectID string) {
	if r.Method != http.MethodGet {
		standaloneFail(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	project, err := h.server.project(principal, projectID)
	if err != nil {
		standaloneFail(w, http.StatusNotFound, "PROJECT_NOT_FOUND")
		return
	}
	service, err := h.server.projectService(projectID)
	if err != nil {
		portableError(w, err)
		return
	}
	policy, err := service.Policy()
	if err != nil {
		portableError(w, err)
		return
	}
	if _, err = h.server.portable(projectID, service); err != nil {
		portableError(w, err)
		return
	}
	checks := []map[string]any{}
	for _, check := range policy.Checks {
		checks = append(checks, map[string]any{"id": check.ID, "kind": check.Kind, "method": check.Method, "required": check.Required, "covers": check.Covers})
	}
	rows, err := service.Store.DB.Query("SELECT s.context_id,c.task_id,c.candidate_id,s.id,s.path,s.sha256,s.status,s.kind FROM portable_context_sources s JOIN portable_contexts c ON c.id=s.context_id ORDER BY s.id,s.sha256 LIMIT 1000")
	if err != nil {
		portableError(w, err)
		return
	}
	defer rows.Close()
	sources := []map[string]string{}
	for rows.Next() {
		var contextID, taskID, candidateID, id, path, hash, status, kind string
		if err = rows.Scan(&contextID, &taskID, &candidateID, &id, &path, &hash, &status, &kind); err != nil {
			portableError(w, err)
			return
		}
		if _, err := store.SafePath(service.Root, path); err != nil || !knowledge.PortableSourcePath(path) {
			continue
		}
		sources = append(sources, map[string]string{"context_id": contextID, "task_id": taskID, "candidate_id": candidateID, "id": id, "path": path, "sha256": hash, "status": status, "kind": kind})
	}
	if err = rows.Err(); err != nil {
		portableError(w, err)
		return
	}
	state := "awaiting_sources"
	if len(sources) > 0 {
		state = "readable_snapshots_require_checkout_verification"
	}
	policyBytes, _ := json.Marshal(policy)
	standaloneReply(w, http.StatusOK, map[string]any{"project": project, "api_protocol": 1, "skill_protocol": 1, "checks": checks, "policy_sha256": store.Hash(policyBytes), "critical_knowledge": policy.CriticalKnowledge, "knowledge": sources, "specification_state": state, "configuration_source": "server_project_policy_and_finalized_context_index", "tasks_require_candidate_and_context": true, "server_version": h.version, "server_build": h.build, "cli_output_protocol": 1})
}
