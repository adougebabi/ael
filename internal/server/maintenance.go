package server

import (
	"ael/internal/store"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maintenanceKey = "server:maintenance"

type MaintenanceState struct {
	Schema    int        `json:"schema"`
	Mode      string     `json:"mode"`
	Epoch     int        `json:"epoch"`
	Deadline  *time.Time `json:"deadline,omitempty"`
	Reason    string     `json:"reason,omitempty"`
	Actor     string     `json:"actor,omitempty"`
	Updated   time.Time  `json:"updated,omitempty"`
	Operation string     `json:"operation,omitempty"`
	InputHash string     `json:"input_hash,omitempty"`
	Revoked   int        `json:"revoked_responsibilities"`
}

type maintenanceInput struct {
	Action        string     `json:"action"`
	Reason        string     `json:"reason"`
	Deadline      *time.Time `json:"deadline,omitempty"`
	RequestKey    string     `json:"request_key"`
	ExpectedState string     `json:"expected_state,omitempty"`
}

func (state MaintenanceState) revision() string {
	body, _ := json.Marshal(state)
	return store.Hash(body)
}

func (s *Standalone) Maintenance() (MaintenanceState, error) {
	state := MaintenanceState{Schema: 1, Mode: "active", Epoch: 1}
	var body string
	err := s.store.DB.QueryRow("SELECT body FROM metadata WHERE key=?", maintenanceKey).Scan(&body)
	if err == sql.ErrNoRows {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if json.Unmarshal([]byte(body), &state) != nil || state.Schema != 1 || state.Epoch < 1 {
		return state, errors.New("MAINTENANCE_STATE_INVALID")
	}
	switch state.Mode {
	case "active", "draining", "quiescing", "quiesced":
		return state, nil
	}
	return state, errors.New("MAINTENANCE_STATE_INVALID")
}

func writeMaintenance(tx *store.Tx, state MaintenanceState) error {
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body", maintenanceKey, string(body))
	return err
}

func (s *Standalone) changeMaintenance(principal standalonePrincipal, input maintenanceInput) (map[string]any, error) {
	if !principal.Admin || !standaloneID.MatchString(input.RequestKey) || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 1000 {
		return nil, errors.New("MAINTENANCE_INVALID")
	}
	if input.ExpectedState != "" && !artifactDigest.MatchString(input.ExpectedState) {
		return nil, errors.New("MAINTENANCE_INVALID")
	}
	if input.Action != "drain" && input.Action != "quiesce" && input.Action != "resume" {
		return nil, errors.New("MAINTENANCE_INVALID")
	}
	raw, _ := json.Marshal(struct {
		Actor string
		Input maintenanceInput
	}{principal.UserID, input})
	hash := store.Hash(raw)
	receiptKey := "server:maintenance-op:" + input.RequestKey
	var receipt struct {
		Hash  string           `json:"hash"`
		State MaintenanceState `json:"state"`
	}
	var prior string
	err := s.store.DB.QueryRow("SELECT body FROM metadata WHERE key=?", receiptKey).Scan(&prior)
	if err == nil {
		if json.Unmarshal([]byte(prior), &receipt) != nil || receipt.Hash != hash {
			return nil, errors.New("IDEMPOTENCY_CONFLICT")
		}
		current, err := s.Maintenance()
		return map[string]any{"state": receipt.State, "current": current, "current_revision": current.revision(), "replayed": true}, err
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	state, err := s.Maintenance()
	if err != nil {
		return nil, err
	}
	if state.Mode == "quiescing" && (input.Action != "quiesce" || state.Operation != input.RequestKey || state.InputHash != hash) {
		return nil, errors.New("MAINTENANCE_RECOVERY_REQUIRED")
	}
	// A matching interrupted quiesce retains its original conditional input.
	if state.Mode != "quiescing" && input.ExpectedState != "" && input.ExpectedState != state.revision() {
		return nil, errors.New("MAINTENANCE_STATE_CONFLICT")
	}
	now := time.Now().UTC()
	switch input.Action {
	case "drain":
		if state.Mode != "active" && state.Mode != "draining" {
			return nil, errors.New("MAINTENANCE_STATE_CONFLICT")
		}
		if input.Deadline == nil || !input.Deadline.After(now) {
			return nil, errors.New("MAINTENANCE_DEADLINE_REQUIRED")
		}
		state.Mode, state.Deadline = "draining", input.Deadline
	case "quiesce":
		if state.Mode == "active" {
			return nil, errors.New("MAINTENANCE_DRAIN_REQUIRED")
		}
		if state.Mode != "quiesced" {
			if state.Mode != "quiescing" {
				state.Mode, state.Epoch, state.Operation, state.InputHash = "quiescing", state.Epoch+1, input.RequestKey, hash
				state.Actor, state.Reason, state.Updated = principal.Username, input.Reason, now
				if err = s.store.Write(func(tx *store.Tx) error { return writeMaintenance(tx, state) }); err != nil {
					return nil, err
				}
			}
			count, err := s.interruptMaintenanceResponsibilities(principal.Username, input.Reason, state.Epoch)
			if err != nil {
				return nil, err
			}
			state.Revoked = count
		}
		state.Mode = "quiesced"
	case "resume":
		if state.Mode == "quiescing" {
			return nil, errors.New("MAINTENANCE_RECOVERY_REQUIRED")
		}
		state.Mode, state.Deadline = "active", nil
	}
	state.Actor, state.Reason, state.Updated, state.Operation, state.InputHash = principal.Username, input.Reason, now, "", ""
	receipt.Hash, receipt.State = hash, state
	body, _ := json.Marshal(receipt)
	if err = s.store.Write(func(tx *store.Tx) error {
		if err := writeMaintenance(tx, state); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?)", receiptKey, string(body)); err != nil {
			return err
		}
		return tx.Event("", "server_maintenance", map[string]any{"actor": principal.Username, "action": input.Action, "epoch": state.Epoch, "reason": input.Reason, "host_execution_state": "unknown"})
	}); err != nil {
		return nil, err
	}
	return map[string]any{"state": state, "current": state, "current_revision": state.revision(), "replayed": false}, nil
}

func (s *Standalone) interruptMaintenanceResponsibilities(actor, reason string, epoch int) (int, error) {
	projects, err := s.projects(standalonePrincipal{Admin: true})
	if err != nil {
		return 0, err
	}
	total := 0
	for _, project := range projects {
		service, err := s.projectService(project.ID)
		if err != nil {
			return total, err
		}
		if _, err = s.portable(project.ID, service); err != nil {
			return total, err
		}
		ids := []string{}
		rows, err := service.Store.DB.Query("SELECT id FROM tasks ORDER BY id")
		if err != nil {
			return total, err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return total, err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return total, err
		}
		revoked := 0
		if err = service.Store.Write(func(tx *store.Tx) error {
			epochKey := "server:maintenance-epoch:" + strconv.Itoa(epoch)
			var prior string
			priorErr := tx.Row("SELECT body FROM metadata WHERE key=?", epochKey).Scan(&prior)
			if priorErr == nil {
				return json.Unmarshal([]byte(prior), &revoked)
			}
			if priorErr != sql.ErrNoRows {
				return priorErr
			}
			for _, id := range ids {
				attempts, err := service.RemoteMaintenanceInterruptTx(tx, id, actor, reason)
				if err != nil {
					return err
				}
				for _, attempt := range attempts {
					if _, err = tx.Exec("UPDATE remote_runner_attempts SET state='interrupted' WHERE attempt_id=?", attempt); err != nil {
						return err
					}
					if _, err = tx.Exec("INSERT INTO remote_workspace_holds(repository,workspace_id,attempt_id,task_id,state) SELECT r.repository,r.workspace_id,a.attempt_id,a.task_id,'unknown' FROM remote_runner_attempts a JOIN remote_runners r ON r.id=a.runner_id WHERE a.attempt_id=? ON CONFLICT(repository,workspace_id,attempt_id) DO UPDATE SET state='unknown'", attempt); err != nil {
						return err
					}
					revoked++
				}
			}
			body, _ := json.Marshal(revoked)
			if _, err := tx.Exec("INSERT INTO metadata(key,body) VALUES(?,?)", epochKey, string(body)); err != nil {
				return err
			}
			return tx.Event("", "server_maintenance_epoch", map[string]any{"actor": actor, "reason": reason, "revoked": revoked})
		}); err != nil {
			return total, err
		}
		total += revoked
	}
	return total, nil
}

func (h *standaloneHandler) maintenanceAPI(w http.ResponseWriter, r *http.Request, p standalonePrincipal, method string) {
	if !p.Admin || method != "session" {
		standaloneFail(w, http.StatusForbidden, "FORBIDDEN")
		return
	}
	if r.Method == http.MethodGet {
		state, err := h.server.Maintenance()
		if err != nil {
			standaloneFail(w, 500, "SERVER_ERROR")
			return
		}
		standaloneReply(w, 200, struct {
			MaintenanceState
			Revision string `json:"revision"`
		}{state, state.revision()})
		return
	}
	if r.Method != http.MethodPost {
		standaloneFail(w, 405, "METHOD_NOT_ALLOWED")
		return
	}
	var input maintenanceInput
	if decode(r, &input) != nil {
		standaloneFail(w, 400, "MAINTENANCE_INVALID")
		return
	}
	result, err := h.server.changeMaintenance(p, input)
	if err != nil {
		standaloneFail(w, 409, err.Error())
		return
	}
	standaloneReply(w, 200, result)
}

func (h *standaloneHandler) maintenanceAllows(r *http.Request) error {
	if r.Method == http.MethodGet || r.URL.Path == "/api/v1/admin/maintenance" || r.URL.Path == "/api/v1/auth/logout" {
		return nil
	}
	state, err := h.server.Maintenance()
	if err != nil {
		return err
	}
	if state.Mode == "active" {
		return nil
	}
	if state.Mode == "draining" && (state.Deadline == nil || time.Now().Before(*state.Deadline)) && !strings.HasSuffix(r.URL.Path, "/claim") {
		return nil
	}
	return errors.New("SERVER_MAINTENANCE")
}

func (state MaintenanceState) publicNotice() map[string]any {
	return map[string]any{"mode": state.Mode, "epoch": state.Epoch, "deadline": state.Deadline}
}
