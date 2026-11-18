package server

import (
	"ael/internal/store"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

type artifactStorageItem struct {
	Retention      []string `json:"retention_reasons,omitempty"`
	SHA256         string   `json:"sha256"`
	ContentID      string   `json:"content_id"`
	Shared         bool     `json:"shared_content"`
	ID             string   `json:"id"`
	ArtifactID     string   `json:"artifact_id"`
	Kind           string   `json:"kind"`
	TaskID         string   `json:"task_id"`
	TaskTitle      string   `json:"task_title"`
	Filename       string   `json:"filename"`
	MIME           string   `json:"mime"`
	Size           int64    `json:"size"`
	Stored         int64    `json:"stored_bytes"`
	Reserved       int64    `json:"reserved_bytes"`
	Status         string   `json:"status"`
	Uploader       string   `json:"uploader"`
	Source         string   `json:"source"`
	Created        string   `json:"created_at"`
	CanCancel      bool     `json:"can_cancel"`
	OperationID    string   `json:"operation_id,omitempty"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
}

type artifactTaskStorage struct {
	TaskID string `json:"task_id"`
	Title  string `json:"title"`
	Bytes  int64  `json:"bytes"`
	Count  int    `json:"count"`
}

type artifactStorageBreakdown struct {
	Kind  string `json:"kind"`
	Count int    `json:"logical_count"`
	Bytes int64  `json:"physical_bytes"`
}
type artifactInventory struct {
	Types             []artifactStorageBreakdown `json:"file_types"`
	Sources           []artifactStorageBreakdown `json:"sources"`
	LogicalBytes      int64                      `json:"logical_bytes"`
	SharedSavings     int64                      `json:"shared_savings_bytes"`
	DuplicatePhysical int64                      `json:"duplicate_physical_bytes"`
	Stats             artifactStats              `json:"stats"`
	Items             []artifactStorageItem      `json:"items"`
	Tasks             []artifactTaskStorage      `json:"tasks"`
	Total             int                        `json:"total"`
	Limit             int                        `json:"limit"`
	Offset            int                        `json:"offset"`
	UntrackedBytes    int64                      `json:"untracked_bytes"`
}

func (a *artifactStore) inventory(principal standalonePrincipal, canWrite bool, kind, query string, limit, offset int) (artifactInventory, error) {
	result := artifactInventory{Items: []artifactStorageItem{}, Tasks: []artifactTaskStorage{}, Limit: limit, Offset: offset}
	if (kind != "files" && kind != "uploads") || limit < 1 || limit > 100 || offset < 0 || len(query) > 500 {
		return result, errors.New("ARTIFACT_INVALID")
	}
	conn, err := a.service.Store.DB.Conn(context.Background())
	if err != nil {
		return result, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		return result, err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	tx := &store.Tx{Conn: conn}
	err = func() error {
		_, objects, err := artifactDirectorySizes(a.objects)
		if err != nil {
			return err
		}
		_, uploads, err := artifactDirectorySizes(a.uploads)
		if err != nil {
			return err
		}
		result.Stats, err = a.usageFromSizes(tx, objects, uploads)
		if err != nil {
			return err
		}
		uploadIDs := []string{}
		for id := range uploads {
			uploadIDs = append(uploadIDs, id)
		}
		uploadJSON, _ := json.Marshal(uploadIDs)
		rows, err := tx.Conn.QueryContext(context.Background(), `
WITH visible_objects AS MATERIALIZED (SELECT o.*,COALESCE(m.object_id,o.id) AS content_id FROM artifact_objects o LEFT JOIN artifact_content m ON m.artifact_id=o.id WHERE o.status IN ('finalized','purging')),
visible_uploads AS MATERIALIZED (SELECT * FROM artifact_uploads WHERE status IN ('open','purging') OR id IN (SELECT value FROM json_each(?))),
task_titles AS MATERIALIZED (SELECT id,json_extract(body,'$.definition.title') AS title,status FROM tasks WHERE id IN (SELECT task_id FROM visible_objects UNION SELECT task_id FROM visible_uploads))
SELECT a.id,a.id,'files',a.task_id,coalesce(t.title,a.task_id),a.filename,a.mime,a.size,a.status,a.uploader,a.source,a.created,'','','',a.sha256,a.content_id,EXISTS(SELECT 1 FROM portable_context_sources s WHERE s.artifact_id=a.id),EXISTS(SELECT 1 FROM artifact_references r WHERE r.artifact_id=a.id),coalesce(t.status,'')
FROM visible_objects a LEFT JOIN task_titles t ON t.id=a.task_id
UNION ALL
SELECT a.id,a.artifact_id,'uploads',a.task_id,coalesce(t.title,a.task_id),a.filename,a.mime,a.size,a.status,a.uploader,a.source,a.created,a.owner_id,a.operation_id,a.idempotency_key,a.sha256,COALESCE(m.object_id,a.artifact_id),0,0,coalesce(t.status,'')
FROM visible_uploads a LEFT JOIN task_titles t ON t.id=a.task_id LEFT JOIN artifact_content m ON m.artifact_id=a.artifact_id ORDER BY 3,1`, string(uploadJSON))
		if err != nil {
			return err
		}
		defer rows.Close()
		groups := map[string]*artifactTaskStorage{}
		var accounted int64
		seen := map[string]bool{}
		types, sources := map[string]*artifactStorageBreakdown{}, map[string]*artifactStorageBreakdown{}
		hashes := map[string]map[string]int64{}

		query = strings.ToLower(strings.TrimSpace(query))
		for rows.Next() {
			var item artifactStorageItem
			var owner, operation, key, taskStatus string
			var contextRef, taskRef bool
			if err := rows.Scan(&item.ID, &item.ArtifactID, &item.Kind, &item.TaskID, &item.TaskTitle, &item.Filename, &item.MIME, &item.Size, &item.Status, &item.Uploader, &item.Source, &item.Created, &owner, &operation, &key, &item.SHA256, &item.ContentID, &contextRef, &taskRef, &taskStatus); err != nil {
				return err
			}
			if contextRef {
				item.Retention = append(item.Retention, "现行或历史规范快照引用")
			}
			if taskRef {
				item.Retention = append(item.Retention, "其他任务存在显式引用")
			}
			if taskStatus == "open" {
				item.Retention = append(item.Retention, "所属任务未结束")
			}
			item.Stored = objects[item.ContentID]
			item.Shared = item.ContentID != item.ArtifactID
			if item.Kind == "files" {
				result.LogicalBytes += item.Size
				if hashes[item.SHA256] == nil {
					hashes[item.SHA256] = map[string]int64{}
				}
				hashes[item.SHA256][item.ContentID] = item.Stored
				if seen[item.ContentID] {
					result.SharedSavings += item.Stored
					item.Stored = 0
				}
				seen[item.ContentID] = true
				category := item.MIME
				source := "other"
				switch {
				case strings.Contains(item.Source, "context") || strings.Contains(item.Source, "knowledge"):
					source = "context snapshot"
				case strings.HasPrefix(item.Source, "authority-import:"):
					source = "import history"
				case strings.Contains(item.Source, "runner"):
					source = "execution evidence"
				}
				for _, group := range []struct {
					m map[string]*artifactStorageBreakdown
					k string
				}{{types, category}, {sources, source}} {
					if group.m[group.k] == nil {
						group.m[group.k] = &artifactStorageBreakdown{Kind: group.k}
					}
					group.m[group.k].Count++
					group.m[group.k].Bytes += item.Stored
				}
			}
			if item.Kind == "uploads" {
				item.Stored = uploads[item.ID]
				if item.Status == "open" || item.Status == "purging" {
					if !item.Shared {
						item.Reserved = max(0, item.Size-max(item.Stored, objects[item.ContentID]))
					}
					item.CanCancel = canWrite && (principal.Admin || owner == principal.UserID)
				}
				if item.CanCancel {
					item.OperationID, item.IdempotencyKey = operation, key
				}
			}
			accounted += item.Stored
			if item.Kind == "uploads" && !principal.Admin && owner != principal.UserID {
				continue
			}
			if item.Stored+item.Reserved == 0 && item.Kind != "files" && !(item.Shared && item.Status == "open") {
				continue
			}
			if groups[item.TaskID] == nil {
				groups[item.TaskID] = &artifactTaskStorage{TaskID: item.TaskID, Title: item.TaskTitle}
			}
			groups[item.TaskID].Bytes += item.Stored + item.Reserved
			groups[item.TaskID].Count++
			if item.Kind == kind && strings.Contains(strings.ToLower(item.Filename+" "+item.TaskID+" "+item.TaskTitle), query) {
				result.Items = append(result.Items, item)
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, paths := range hashes {
			var sum, largest int64
			for _, size := range paths {
				sum += size
				largest = max(largest, size)
			}
			result.DuplicatePhysical += max(0, sum-largest)
		}
		for _, v := range types {
			result.Types = append(result.Types, *v)
		}
		for _, v := range sources {
			result.Sources = append(result.Sources, *v)
		}
		sort.Slice(result.Types, func(i, j int) bool { return result.Types[i].Kind < result.Types[j].Kind })
		sort.Slice(result.Sources, func(i, j int) bool { return result.Sources[i].Kind < result.Sources[j].Kind })
		result.UntrackedBytes = max(0, result.Stats.Used-accounted)
		for _, task := range groups {
			result.Tasks = append(result.Tasks, *task)
		}
		return nil
	}()
	sort.Slice(result.Tasks, func(i, j int) bool {
		if result.Tasks[i].Bytes == result.Tasks[j].Bytes {
			return result.Tasks[i].TaskID < result.Tasks[j].TaskID
		}
		return result.Tasks[i].Bytes > result.Tasks[j].Bytes
	})
	sort.Slice(result.Items, func(i, j int) bool {
		left, right := result.Items[i], result.Items[j]
		if left.Stored+left.Reserved == right.Stored+right.Reserved {
			return left.ID < right.ID
		}
		return left.Stored+left.Reserved > right.Stored+right.Reserved
	})
	result.Total = len(result.Items)
	start := min(offset, result.Total)
	result.Items = result.Items[start:min(start+limit, result.Total)]
	return result, err
}
