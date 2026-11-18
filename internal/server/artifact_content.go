package server

import (
	"ael/internal/store"
	"database/sql"
	"encoding/json"
	"os"
)

// Logical artifacts keep their provenance. A project-local mapping reuses one
// immutable, single-link physical object; legacy objects default to their ID.
func (a *artifactStore) contentPath(row func(string, ...any) *sql.Row, id string) (string, error) {
	var key string
	if err := row("SELECT COALESCE((SELECT object_id FROM artifact_content WHERE artifact_id=?),?)", id, id).Scan(&key); err != nil {
		return "", err
	}
	return a.objectPath(key)
}
func (a *artifactStore) reusableContent(tx *store.Tx, hash string, size int64, mime string) (string, error) {
	var key string
	err := tx.Row("SELECT COALESCE(m.object_id,o.id) FROM artifact_objects o LEFT JOIN artifact_content m ON m.artifact_id=o.id WHERE o.status='finalized' AND o.sha256=? AND o.size=? ORDER BY o.id LIMIT 1", hash, size).Scan(&key)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	path, err := a.objectPath(key)
	if err != nil {
		return "", err
	}
	if _, err = artifactFileBytes(path, size, hash, mime); err != nil {
		return "", nil
	}
	return key, nil
}
func (a *artifactStore) otherContentHolders(tx *store.Tx, id string) (bool, error) {
	var count int
	err := tx.Row(`SELECT count(*) FROM (
 SELECT o.id FROM artifact_objects o LEFT JOIN artifact_content m ON m.artifact_id=o.id WHERE o.id<>? AND o.status IN ('finalized','purging') AND COALESCE(m.object_id,o.id)=COALESCE((SELECT object_id FROM artifact_content WHERE artifact_id=?),?)
 UNION SELECT u.artifact_id FROM artifact_uploads u LEFT JOIN artifact_content m ON m.artifact_id=u.artifact_id WHERE u.artifact_id<>? AND u.status IN ('open','purging') AND COALESCE(m.object_id,u.artifact_id)=COALESCE((SELECT object_id FROM artifact_content WHERE artifact_id=?),?))`, id, id, id, id, id, id).Scan(&count)
	return count > 0, err
}
func (a *artifactStore) clearSharedStage(id string) error {
	return a.service.Store.Write(func(tx *store.Tx) error {
		upload, err := scanUpload(tx.Row("SELECT "+uploadColumns+" FROM artifact_uploads WHERE id=?", id))
		if err != nil {
			return err
		}
		var physical string
		err = tx.Row("SELECT object_id FROM artifact_content WHERE artifact_id=?", upload.ArtifactID).Scan(&physical)
		if err == sql.ErrNoRows || upload.Status != "finalized" {
			return nil
		}
		if err != nil {
			return err
		}
		stage, err := a.uploadPath(id)
		if err != nil {
			return err
		}
		if err = os.Remove(stage); err != nil && !os.IsNotExist(err) {
			return err
		}
		// A failed pre-commit finalize may have left this upload's old target.
		// Once the mapping is durable, remove it only if nobody still uses it.
		var holders int
		err = tx.Row(`SELECT count(*) FROM (
SELECT o.id FROM artifact_objects o LEFT JOIN artifact_content m ON m.artifact_id=o.id WHERE o.status IN ('finalized','purging') AND COALESCE(m.object_id,o.id)=?
UNION SELECT u.artifact_id FROM artifact_uploads u LEFT JOIN artifact_content m ON m.artifact_id=u.artifact_id WHERE u.status IN ('open','purging') AND COALESCE(m.object_id,u.artifact_id)=?)`, upload.ArtifactID, upload.ArtifactID).Scan(&holders)
		if err != nil {
			return err
		}
		if physical != upload.ArtifactID && holders == 0 {
			own, err := a.objectPath(upload.ArtifactID)
			if err != nil {
				return err
			}
			if _, err = os.Lstat(own); err == nil {
				if _, err = artifactFileBytes(own, upload.Size, upload.SHA256, upload.MIME); err != nil {
					return err
				}
				if err = os.Remove(own); err != nil {
					return err
				}
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		return artifactSyncDirs(a.uploads, a.objects)
	})
}

// Preview physical bytes, counting a shared object only when the entire live
// holder set belongs to this batch. Deletion still rechecks holders per item.
func (a *artifactStore) reclaimableSizes(tx *store.Tx, items []artifactPruneItem) error {
	selected := []string{}
	for _, item := range items {
		id := item.ID
		if item.Kind == "upload" {
			id = item.ArtifactID
		}
		selected = append(selected, id)
	}
	raw, _ := json.Marshal(selected)
	seen := map[string]bool{}
	for i := range items {
		item := &items[i]
		item.Size = 0
		id := item.ID
		if item.Kind == "upload" {
			id = item.ArtifactID
			temp, err := a.uploadPath(item.ID)
			if err != nil {
				return err
			}
			if info, err := os.Lstat(temp); err == nil {
				item.Size += info.Size()
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		path, err := a.contentPath(tx.Row, id)
		if err != nil {
			return err
		}
		if seen[path] {
			continue
		}
		var held int
		err = tx.Row(`SELECT count(*) FROM (
 SELECT o.id FROM artifact_objects o LEFT JOIN artifact_content m ON m.artifact_id=o.id WHERE o.status IN ('finalized','purging') AND COALESCE(m.object_id,o.id)=COALESCE((SELECT object_id FROM artifact_content WHERE artifact_id=?),?)
 UNION SELECT u.artifact_id FROM artifact_uploads u LEFT JOIN artifact_content m ON m.artifact_id=u.artifact_id WHERE u.status IN ('open','purging') AND COALESCE(m.object_id,u.artifact_id)=COALESCE((SELECT object_id FROM artifact_content WHERE artifact_id=?),?)) WHERE id NOT IN (SELECT value FROM json_each(?))`, id, id, id, id, string(raw)).Scan(&held)
		if err != nil {
			return err
		}
		if held > 0 {
			continue
		}
		if info, err := os.Lstat(path); err == nil {
			item.Size += info.Size()
			seen[path] = true
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
