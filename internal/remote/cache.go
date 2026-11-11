package remote

import (
	"ael/internal/securityfs"
	"ael/internal/store"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type cacheRecord struct {
	Schema     int            `json:"schema"`
	Key        string         `json:"key"`
	ObservedAt string         `json:"observed_at"`
	Data       map[string]any `json:"data"`
}

func cacheFile(config string, profile Profile, binding Binding, suffix string, create bool) (string, string, error) {
	key := store.Hash([]byte(profile.URL + "\n" + profile.Token + "\n" + binding.ProjectID + "\n" + suffix))
	dir, err := store.SafePath(config, "cache")
	if err != nil {
		return "", "", err
	}
	if create {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return "", "", err
		}
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || securityfs.NoACL(dir) != nil {
		return "", "", errors.New("REMOTE_CACHE_UNAVAILABLE")
	}
	path, err := store.SafePath(config, filepath.Join("cache", key+".json"))
	return path, key, err
}

func readCache(config string, profile Profile, binding Binding, suffix string) (cacheRecord, error) {
	var result cacheRecord
	path, key, err := cacheFile(config, profile, binding, suffix, false)
	if err != nil {
		return result, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || securityfs.NoACL(path) != nil || securityfs.SingleLinkRegular(path) != nil {
		return result, errors.New("REMOTE_CACHE_UNAVAILABLE")
	}
	f, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 2<<20))
	decoder.UseNumber()
	if err = decoder.Decode(&result); err != nil || result.Schema != 1 || result.Key != key || result.ObservedAt == "" || result.Data == nil {
		return cacheRecord{}, errors.New("REMOTE_CACHE_INVALID")
	}
	return result, nil
}

func writeCache(config string, profile Profile, binding Binding, suffix, observed string, data map[string]any) error {
	path, key, err := cacheFile(config, profile, binding, suffix, true)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(path); err == nil {
		if _, err = readCache(config, profile, binding, suffix); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	raw, err := json.Marshal(cacheRecord{Schema: 1, Key: key, ObservedAt: observed, Data: data})
	if err != nil {
		return err
	}
	return store.Atomic(path, append(raw, '\n'), 0600)
}
