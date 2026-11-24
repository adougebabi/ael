package remote

import (
	"ael/internal/store"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// A marker in per-worktree Git metadata does not travel in a clone. Linking
// the completed file also lets simultaneous first commands elect one identity.
func CheckoutInstance(root string) (string, error) {
	dir, err := portableGit(root, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", errors.New("CLONE_GIT_CHECKOUT_REQUIRED: use the cloned Git worktree")
	}
	path, err := store.SafePath(strings.TrimSpace(string(dir)), "ael-checkout-id")
	if err != nil {
		return "", err
	}
	read := func() (string, error) {
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if len(raw) != 32 {
			return "", errors.New("CLONE_CHECKOUT_ID_INVALID")
		}
		if _, err = hex.DecodeString(string(raw)); err != nil {
			return "", errors.New("CLONE_CHECKOUT_ID_INVALID")
		}
		return string(raw), nil
	}
	if id, err := read(); !os.IsNotExist(err) {
		return id, err
	}
	var raw [16]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ael-checkout-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.WriteString(hex.EncodeToString(raw[:]))
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err = os.Link(tmp.Name(), path); err != nil && !os.IsExist(err) {
		return "", err
	}
	return read()
}
