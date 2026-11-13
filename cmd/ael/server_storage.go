package main

import (
	"ael/internal/core"
	"ael/internal/server"
	"strings"
)

func serverStorageCommand(cmd string, a args) (any, error) {
	allowed := map[string]bool{"data-dir": true, "out": true, "from": true, "ack-stopped": true, "json": true, "verbose": true, "no-color": true}
	for flag, value := range a.Flags {
		if !allowed[flag] {
			return nil, core.Error("UNKNOWN_FLAG", flag)
		}
		if booleanFlag(flag) && value != "true" && value != "false" {
			return nil, core.Error("INVALID_FLAG_VALUE", flag)
		}
		if !booleanFlag(flag) && value == "true" {
			return nil, core.Error("FLAG_VALUE_REQUIRED", flag)
		}
	}
	var result any
	var err error
	switch cmd {
	case "server backup":
		if a.get("out", "") == "" {
			return nil, core.Error("SERVER_BACKUP_TARGET_REQUIRED", "provide an absolute --out new directory")
		}
		result, err = server.BackupStandalone(a.get("data-dir", ""), a.get("out", ""), a.has("ack-stopped"))
	case "server restore":
		if a.get("from", "") == "" {
			return nil, core.Error("SERVER_BACKUP_SOURCE_REQUIRED", "provide --from backup directory")
		}
		result, err = server.RestoreStandalone(a.get("from", ""), a.get("data-dir", ""))
	case "server migrate":
		result, err = server.MigrateStandalone(a.get("data-dir", ""), a.has("ack-stopped"))
	case "server verify-backup":
		result, _, err = server.VerifyServerBackup(a.get("from", ""))
	default:
		return nil, core.Error("UNKNOWN_COMMAND", cmd)
	}
	if err != nil {
		code := strings.SplitN(err.Error(), ":", 2)[0]
		if !strings.HasPrefix(code, "SERVER_") || strings.ContainsAny(code, " \n\r\x00") {
			code = "SERVER_STORAGE_FAILED"
		}
		return nil, core.Error(code, "server storage operation incomplete; preserve its backup and recovery record")
	}
	return result, nil
}
