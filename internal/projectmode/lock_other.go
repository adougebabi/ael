//go:build !darwin && !linux

package projectmode

import "errors"

func Lock(string, bool) (func(), error) { return nil, errors.New("PROJECT_MODE_LOCK_UNSUPPORTED") }
