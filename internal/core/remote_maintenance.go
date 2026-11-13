package core

import (
	"ael/internal/store"
	"strings"
	"time"
)

// RemoteMaintenanceInterruptTx revokes API responsibility while preserving the
// old writer's unknown host state and all accepted results.
func (s *Service) RemoteMaintenanceInterruptTx(tx *store.Tx, id, actor, reason string) ([]string, error) {
	if !s.RemoteAuthority || strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" {
		return nil, Error("MAINTENANCE_INVALID", "central operator and reason required")
	}
	task, err := load(tx, id)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for index := range task.Attempts {
		attempt := &task.Attempts[index]
		if attempt.Status != "running" || !strings.HasPrefix(attempt.Workspace, "remote:") {
			continue
		}
		markInterrupted(&task, attempt, reason)
		attempt.Lease = time.Time{}
		ids = append(ids, attempt.ID)
	}
	if len(ids) == 0 {
		return ids, nil
	}
	task.Blocker.Recovery = "外部执行者核对原责任与工作区，明确停工或独立工作区后使用 runner recover"
	task.Blocker.Next = "维护恢复后重新读取当前版本，保留旧结果并明确处理未知宿主状态"
	if err = s.save(tx, &task); err != nil {
		return nil, err
	}
	if err = tx.Event(id, "server_maintenance_interrupted", map[string]any{"actor": actor, "reason": reason, "attempts": ids, "host_execution_state": "unknown"}); err != nil {
		return nil, err
	}
	return ids, nil
}
