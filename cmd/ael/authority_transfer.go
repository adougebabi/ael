package main

import (
	"ael/internal/bootstrap"
	"ael/internal/core"
	"ael/internal/projectmode"
	"ael/internal/remote"
	"ael/internal/store"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func authorityTransferCommand(cmd string, a args) (any, error) {
	allowed := map[string]bool{"project": true, "source-id": true, "out": true, "plan": true, "approved-digest": true, "ack-drained": true, "json": true, "verbose": true, "no-color": true, "from": true, "config-dir": true, "profile": true, "remote-project": true, "operation-id": true, "idempotency-key": true}
	for flag, value := range a.Flags {
		if !allowed[flag] {
			return nil, core.Error("UNKNOWN_FLAG", flag)
		}
		if booleanFlag(flag) && value != "true" && value != "false" {
			return nil, core.Error("INVALID_FLAG_VALUE", flag)
		}
	}
	if cmd == "transfer scan" {
		return bootstrap.ScanTransfer(a.get("project", "."), a.get("source-id", ""))
	}
	if cmd == "transfer import" || cmd == "transfer verify" || cmd == "transfer commit" || cmd == "transfer rollback-freeze" {
		return remoteTransferCommand(cmd, a)
	}
	if cmd == "transfer cutover" {
		return cutoverTransferCommand(a)
	}
	if cmd == "transfer rollback" {
		return rollbackTransferCommand(a)
	}
	var plan bootstrap.TransferPlan
	if err := input(a.get("plan", ""), &plan); err != nil {
		return nil, core.Error("TRANSFER_PLAN_INVALID", "read the exact scan/plan JSON file")
	}
	if cmd == "transfer plan" {
		current, err := bootstrap.ScanTransfer(plan.Root, plan.Archive.Source)
		if err != nil {
			return nil, err
		}
		if current.Digest != plan.Digest {
			return nil, core.Error("TRANSFER_PLAN_STALE", "source changed since the scan")
		}
		out := a.get("out", "")
		if !filepath.IsAbs(out) {
			return nil, core.Error("TRANSFER_OUT_REQUIRED", "provide an absolute new --out plan file")
		}
		body, err := json.MarshalIndent(current, "", "  ")
		if err != nil {
			return nil, err
		}
		file, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		_, writeErr := file.Write(append(body, '\n'))
		if writeErr == nil {
			writeErr = file.Sync()
		}
		closeErr := file.Close()
		if writeErr != nil {
			return nil, writeErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		return map[string]any{"digest": current.Digest, "plan": out, "tasks": len(current.Archive.Tasks), "unresolved_responsibilities": current.Active, "workspace_locks": current.Locks, "pending_projections": current.Pending}, nil
	}
	if cmd == "transfer export" {
		if a.get("approved-digest", "") != plan.Digest || len(plan.Digest) != 64 {
			return nil, core.Error("TRANSFER_APPROVAL_REQUIRED", "review the plan and provide its exact --approved-digest")
		}
		if a.get("out", "") == "" {
			return nil, core.Error("TRANSFER_OUT_REQUIRED", "provide a private export directory")
		}
		return bootstrap.ExportTransfer(plan, a.get("out", ""), a.has("ack-drained"))
	}
	return nil, core.Error("UNKNOWN_COMMAND", cmd)
}

func remoteTransferCommand(cmd string, a args) (any, error) {
	from := a.get("from", "")
	plan, err := bootstrap.VerifyTransferExport(from)
	if err != nil {
		return nil, err
	}
	config, err := remoteConfigDir(a)
	if err != nil {
		return nil, err
	}
	project := a.get("project", ".")
	binding := remote.Binding{Schema: 1, Profile: a.get("profile", ""), ProjectID: a.get("remote-project", ""), APIProtocol: remote.APIProtocol, SkillProtocol: remote.SkillProtocol}
	body, _ := json.Marshal(plan.Archive)
	digest := store.Hash(body)
	id := "import-" + digest[:32]
	call := func(method, path string, payload any) (map[string]any, error) {
		return agentCommunicationCall(config, project, binding, method, path, payload)
	}
	if cmd == "transfer import" {
		payload := map[string]any{"archive": plan.Archive, "digest": digest, "operation_id": a.get("operation-id", "transfer-stage-"+digest[:32]), "idempotency_key": a.get("idempotency-key", "transfer-stage-"+digest[:32])}
		result, err := call(http.MethodPost, "/imports", payload)
		if err != nil {
			return nil, err
		}
		if result["id"] != id || result["digest"] != digest {
			return nil, core.Error("TRANSFER_REMOTE_MISMATCH", "central import differs from the export")
		}
		current, err := call(http.MethodGet, "/imports/"+id, nil)
		if err != nil {
			return nil, err
		}
		if current["state"] == "active" || current["state"] == "verified" {
			return remoteOutput(current), nil
		}
		if current["state"] != "importing" {
			return nil, core.Error("IMPORT_ABORTED", "this transfer is frozen or rolled back; retain its history and reconcile the next plan")
		}
		for _, file := range plan.Archive.Files {
			if file.Status != "available" || file.Size == 0 {
				continue
			}
			mapped := ""
			for _, task := range plan.Archive.Tasks {
				if task.OriginalID == file.TaskID {
					mapped = task.MappedID
					break
				}
			}
			state, err := call(http.MethodGet, "/tasks/"+mapped, nil)
			if err != nil {
				return nil, err
			}
			revision, ok := state["revision"].(string)
			if !ok {
				return nil, core.Error("TRANSFER_REMOTE_MISMATCH", "central task revision is absent")
			}
			path, err := store.SafePath(from, filepath.Join("objects", file.SHA256))
			if err != nil {
				return nil, err
			}
			data, err := os.ReadFile(path)
			if err != nil || store.Hash(data) != file.SHA256 {
				return nil, core.Error("TRANSFER_OBJECT_CONFLICT", "local object changed before upload")
			}
			key := transferObjectKey(id, mapped, file.SHA256)
			mime, name := core.HistoricalArtifactType(data)
			flags := map[string]string{"task": mapped, "expected-revision": revision, "source": "authority-import:" + id, "import-id": id, "operation-id": key, "idempotency-key": key, "mime": mime}
			if _, err = retainAgentUpload(args{Flags: flags}, config, project, binding, name, data, nil); err != nil {
				return nil, err
			}
		}
		result["files_uploaded"] = true
		return remoteOutput(result), nil
	}
	if cmd != "transfer rollback-freeze" {
		if _, err = call(http.MethodGet, "/imports/"+id, nil); err != nil {
			return nil, err
		}
	}
	bindings := []map[string]string{}
	if cmd == "transfer verify" {
		mapping := map[string]string{}
		for _, task := range plan.Archive.Tasks {
			mapping[task.OriginalID] = task.MappedID
		}
		for _, file := range plan.Archive.Files {
			if file.Status != "available" || file.Size == 0 {
				continue
			}
			mapped := mapping[file.TaskID]
			key := transferObjectKey(id, mapped, file.SHA256)
			status, err := call(http.MethodGet, "/artifacts/uploads/by-key/"+key, nil)
			if err != nil {
				return nil, err
			}
			artifact, ok := status["artifact_id"].(string)
			if !ok || status["status"] != "finalized" {
				return nil, core.Error("TRANSFER_UPLOAD_INCOMPLETE", "resume the original import before verification")
			}
			bindings = append(bindings, map[string]string{"task_id": mapped, "ref": file.Ref, "artifact_id": artifact})
		}
	}
	action := "verify"
	if cmd == "transfer commit" {
		action = "commit"
	}
	if cmd == "transfer rollback-freeze" {
		action = "rollback-freeze"
	}
	key := "transfer-" + action + "-" + digest[:32]
	payload := map[string]any{"digest": digest, "operation_id": a.get("operation-id", key), "idempotency_key": a.get("idempotency-key", key), "bindings": bindings}
	if action == "rollback-freeze" {
		payload["ack_drained"] = a.has("ack-drained")
	}
	result, err := call(http.MethodPost, "/imports/"+id+"/"+action, payload)
	return remoteOutput(result), err
}

func rollbackTransferCommand(a args) (any, error) {
	if !a.has("ack-drained") {
		return nil, core.Error("TRANSFER_DRAIN_REQUIRED", "coordinate current external executors before rollback")
	}
	plan, err := bootstrap.VerifyTransferExport(a.get("from", ""))
	if err != nil {
		return nil, err
	}
	root, err := bootstrap.Canonical(a.get("project", "."))
	if err != nil {
		return nil, err
	}
	config, err := remoteConfigDir(a)
	if err != nil {
		return nil, err
	}
	binding := remote.Binding{Schema: 1, Profile: a.get("profile", ""), ProjectID: a.get("remote-project", ""), APIProtocol: remote.APIProtocol, SkillProtocol: remote.SkillProtocol}
	body, _ := json.Marshal(plan.Archive)
	digest := store.Hash(body)
	id := "import-" + digest[:32]
	unlock, err := projectmode.Lock(root, true)
	if err != nil {
		return nil, err
	}
	defer unlock()
	state, err := remote.Call(config, root, binding, http.MethodGet, "/imports/"+id, nil, nil)
	if err != nil {
		return nil, err
	}
	if state["id"] != id || state["digest"] != digest || (state["state"] != "rollback_frozen" && state["state"] != "rolled_back") || state["online"] != true || state["authoritative"] != true {
		return nil, core.Error("TRANSFER_ROLLBACK_NOT_FROZEN", "freeze the unchanged central import before releasing Local authority")
	}
	current, exists, hash, err := remote.ReadBindingSnapshot(root)
	if err != nil {
		return nil, err
	}
	if exists && current != binding {
		return nil, core.Error("TRANSFER_BINDING_CONFLICT", "current project binding differs")
	}
	if !exists {
		body, _ := json.Marshal(binding)
		hash = store.Hash(append(body, '\n'))
	}
	if err = bootstrap.ReleaseFrozenTransfer(plan, root, hash); err != nil {
		return nil, err
	}
	if exists {
		_, stillExists, currentHash, err := remote.ReadBindingSnapshot(root)
		if err != nil || !stillExists || currentHash != hash {
			return nil, core.Error("TRANSFER_BINDING_CONFLICT", "binding changed during rollback; central authority remains frozen")
		}
		path, err := store.SafePath(root, ".ael/remote.json")
		if err != nil {
			return nil, err
		}
		if err = os.Remove(path); err != nil {
			return nil, err
		}
	}
	key := "transfer-rollback-complete-" + digest[:32]
	result, err := agentCommunicationCall(config, root, binding, http.MethodPost, "/imports/"+id+"/rollback-complete", map[string]any{"digest": digest, "operation_id": key, "idempotency_key": key, "ack_drained": true})
	if err != nil {
		return nil, core.Error("TRANSFER_ROLLBACK_PENDING", "Local authority released and central frozen; retry the exact rollback to record its receipt")
	}
	result["local_authority"] = "active"
	if err = cancelPendingTransferUploads(config, root, binding, plan.Archive, id); err != nil {
		return nil, core.Error("TRANSFER_ROLLBACK_CLEANUP_PENDING", "Local authority is active and central is rolled back; retry the exact rollback to finish pending upload cleanup")
	}
	return remoteOutput(result), nil
}

func cancelPendingTransferUploads(config, project string, binding remote.Binding, archive core.AuthorityArchive, id string) error {
	mapping := map[string]string{}
	for _, task := range archive.Tasks {
		mapping[task.OriginalID] = task.MappedID
	}
	seen := map[string]bool{}
	for _, file := range archive.Files {
		if file.Status != "available" || file.Size == 0 {
			continue
		}
		key := transferObjectKey(id, mapping[file.TaskID], file.SHA256)
		if seen[key] {
			continue
		}
		seen[key] = true
		status, err := agentCommunicationCall(config, project, binding, http.MethodGet, "/artifacts/uploads/by-key/"+key, nil)
		if err != nil {
			if strings.HasPrefix(err.Error(), "UPLOAD_NOT_FOUND:") || strings.HasPrefix(err.Error(), "ARTIFACT_NOT_FOUND:") {
				continue
			}
			return err
		}
		if status["status"] != "open" && status["status"] != "purging" {
			continue
		}
		upload, ok := status["upload_id"].(string)
		if !ok {
			return core.Error("REMOTE_RESPONSE_INVALID", "pending import upload ID is absent")
		}
		if _, err = agentCommunicationCall(config, project, binding, http.MethodPost, "/artifacts/uploads/"+upload+"/cancel", map[string]string{"operation_id": key, "idempotency_key": key}); err != nil {
			return err
		}
	}
	return nil
}

func transferObjectKey(id, task, digest string) string {
	body, _ := json.Marshal([]string{id, task, digest})
	return "transfer-file-" + store.Hash(body)[:32]
}

func cutoverTransferCommand(a args) (any, error) {
	if !a.has("ack-drained") {
		return nil, core.Error("TRANSFER_DRAIN_REQUIRED", "confirm the source remains externally coordinated")
	}
	plan, err := bootstrap.VerifyTransferExport(a.get("from", ""))
	if err != nil {
		return nil, err
	}
	config, err := remoteConfigDir(a)
	if err != nil {
		return nil, err
	}
	root := a.get("project", ".")
	profile, target := a.get("profile", ""), a.get("remote-project", "")
	body, _ := json.Marshal(plan.Archive)
	digest := store.Hash(body)
	id := "import-" + digest[:32]
	wanted := remote.Binding{Schema: 1, Profile: profile, ProjectID: target, APIProtocol: remote.APIProtocol, SkillProtocol: remote.SkillProtocol}
	verify := func() error {
		if err := bootstrap.VerifyFrozenTransfer(plan, root); err != nil {
			return err
		}
		state, err := remote.Call(config, root, wanted, http.MethodGet, "/imports/"+id, nil, nil)
		if err != nil {
			return err
		}
		if state["id"] != id || state["digest"] != digest || state["state"] != "active" || state["online"] != true || state["authoritative"] != true {
			return core.Error("TRANSFER_NOT_COMMITTED", "central import must be verified, committed and read live")
		}
		return nil
	}
	binding, err := remote.ConnectTransferred(config, root, profile, target, nil, verify)
	if err != nil {
		return nil, err
	}
	return map[string]any{"mode": "remote", "project_id": binding.ProjectID, "profile": binding.Profile, "local_authority": "frozen"}, nil
}
