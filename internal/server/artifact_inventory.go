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
	ID             string `json:"id"`
	ArtifactID     string `json:"artifact_id"`
	Kind           string `json:"kind"`
	TaskID         string `json:"task_id"`
	TaskTitle      string `json:"task_title"`
	Filename       string `json:"filename"`
	MIME           string `json:"mime"`
	Size           int64  `json:"size"`
	Stored         int64  `json:"stored_bytes"`
	Reserved       int64  `json:"reserved_bytes"`
	Status         string `json:"status"`
	Uploader       string `json:"uploader"`
	Source         string `json:"source"`
	Created        string `json:"created_at"`
	CanCancel      bool   `json:"can_cancel"`
	OperationID    string `json:"operation_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type artifactTaskStorage struct {
	TaskID string `json:"task_id"`
	Title  string `json:"title"`
	Bytes  int64  `json:"bytes"`
	Count  int    `json:"count"`
}

type artifactInventory struct {
	Stats          artifactStats         `json:"stats"`
	Items          []artifactStorageItem `json:"items"`
	Tasks          []artifactTaskStorage `json:"tasks"`
	Total          int                   `json:"total"`
	Limit          int                   `json:"limit"`
	Offset         int                   `json:"offset"`
	UntrackedBytes int64                 `json:"untracked_bytes"`
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
	if _, err = conn.ExecContext(context.Background(), "BEGIN"); err != nil {
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
		objectIDs, uploadIDs := []string{}, []string{}
		for id := range objects {
			objectIDs = append(objectIDs, id)
		}
		for id := range uploads {
			uploadIDs = append(uploadIDs, id)
		}
		objectJSON, _ := json.Marshal(objectIDs)
		uploadJSON, _ := json.Marshal(uploadIDs)
		rows, err := tx.Conn.QueryContext(context.Background(), `
WITH visible_objects AS MATERIALIZED (SELECT * FROM artifact_objects WHERE id IN (SELECT value FROM json_each(?))),
visible_uploads AS MATERIALIZED (SELECT * FROM artifact_uploads WHERE status IN ('open','purging') OR id IN (SELECT value FROM json_each(?))),
task_titles AS MATERIALIZED (SELECT id,json_extract(body,'$.definition.title') AS title FROM tasks WHERE id IN (SELECT task_id FROM visible_objects UNION SELECT task_id FROM visible_uploads))
SELECT a.id,a.id,'files',a.task_id,coalesce(t.title,a.task_id),a.filename,a.mime,a.size,a.status,a.uploader,a.source,a.created,'','',''
FROM visible_objects a LEFT JOIN task_titles t ON t.id=a.task_id
UNION ALL
SELECT a.id,a.artifact_id,'uploads',a.task_id,coalesce(t.title,a.task_id),a.filename,a.mime,a.size,a.status,a.uploader,a.source,a.created,a.owner_id,a.operation_id,a.idempotency_key
FROM visible_uploads a LEFT JOIN task_titles t ON t.id=a.task_id`, string(objectJSON), string(uploadJSON))
		if err != nil {
			return err
		}
		defer rows.Close()
		groups := map[string]*artifactTaskStorage{}
		var accounted int64
		query = strings.ToLower(strings.TrimSpace(query))
		for rows.Next() {
			var item artifactStorageItem
			var owner, operation, key string
			if err := rows.Scan(&item.ID, &item.ArtifactID, &item.Kind, &item.TaskID, &item.TaskTitle, &item.Filename, &item.MIME, &item.Size, &item.Status, &item.Uploader, &item.Source, &item.Created, &owner, &operation, &key); err != nil {
				return err
			}
			item.Stored = objects[item.ID]
			if item.Kind == "uploads" {
				item.Stored = uploads[item.ID]
				if item.Status == "open" || item.Status == "purging" {
					item.Reserved = max(0, item.Size-item.Stored)
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
			if item.Stored+item.Reserved == 0 {
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
