package remote

import (
	"ael/internal/store"
	"bytes"
	"encoding/json"
)

// RequestDigest normalizes both typed payloads and decoded objects before
// hashing, so disk recovery and HTTP receipt lookup agree on field ordering.
func RequestDigest(payload any) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return store.Hash(canonical), nil
}
