package server

import (
	"ael/internal/store"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
)

func validCloneSecret(secret, prefix string) bool {
	raw, ok := strings.CutPrefix(secret, prefix)
	b, err := base64.RawURLEncoding.DecodeString(raw)
	return ok && err == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == raw
}

// The repository credential authenticates only to this project's enrollment
// endpoint. Worker tokens and accounts are private to the enrolling checkout.
func (h *standaloneHandler) cloneAccess(w http.ResponseWriter, r *http.Request, p standalonePrincipal, method, project, action string) {
	if r.Method != http.MethodPost {
		standaloneFail(w, 405, "METHOD_NOT_ALLOWED")
		return
	}
	if action == "clone-grants" {
		if method != "session" || !p.Admin {
			standaloneFail(w, 403, "FORBIDDEN")
			return
		}
		var input struct{ Grant string }
		if decode(r, &input) != nil || !validCloneSecret(input.Grant, "join_") {
			standaloneFail(w, 400, "CLONE_GRANT_INVALID")
			return
		}
		if _, err := h.server.role(p, project); err != nil {
			standaloneFail(w, 404, "PROJECT_NOT_FOUND")
			return
		}
		_, err := h.server.store.DB.Exec("INSERT INTO server_clone_grants(secret_hash,project_id) VALUES(?,?) ON CONFLICT(secret_hash) DO NOTHING", standaloneHash(input.Grant), project)
		var stored string
		if err != nil || h.server.store.DB.QueryRow("SELECT project_id FROM server_clone_grants WHERE secret_hash=?", standaloneHash(input.Grant)).Scan(&stored) != nil || stored != project {
			standaloneFail(w, 409, "CLONE_GRANT_CONFLICT")
			return
		}
		standaloneReply(w, 200, map[string]bool{"granted": true})
		return
	}
	if method != "clone" || p.ProjectID != project {
		standaloneFail(w, 403, "FORBIDDEN")
		return
	}
	var input struct{ Token string }
	if decode(r, &input) != nil || !validCloneSecret(input.Token, "ael_") {
		standaloneFail(w, 400, "CLONE_TOKEN_INVALID")
		return
	}
	err := h.server.store.Write(func(tx *store.Tx) error {
		var registered, worker string
		var revoked int
		err := tx.Row("SELECT project_id,revoked,user_id FROM server_tokens WHERE secret_hash=?", standaloneHash(input.Token)).Scan(&registered, &revoked, &worker)
		if err == nil {
			if registered != project || revoked != 0 || !strings.HasPrefix(worker, "runner_") {
				return errors.New("CLONE_TOKEN_CONFLICT")
			}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		user, err := standaloneRandom("runner_")
		if err != nil {
			return err
		}
		// No password verifier exists for this non-interactive account.
		if _, err = tx.Exec("INSERT INTO server_users(id,username,salt,password_hash,rounds,admin) VALUES(?,?,randomblob(16),X'',?,0)", user, strings.ToLower(user), standalonePasswordRounds); err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO server_members(project_id,user_id,role) VALUES(?,?,'executor')", project, user); err != nil {
			return err
		}
		id, err := standaloneRandom("token_")
		if err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO server_tokens(id,secret_hash,user_id,project_id,label) VALUES(?,?,?,?,?)", id, standaloneHash(input.Token), user, project, "Clone runner")
		return err
	})
	if err != nil {
		standaloneFail(w, 409, "CLONE_ENROLLMENT_FAILED")
		return
	}
	standaloneReply(w, 200, map[string]bool{"enrolled": true})
}
