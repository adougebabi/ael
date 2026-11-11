//go:build !darwin && !linux

package server

import "errors"

func standaloneLocalFilesystem(string) error {
	return errors.New("SERVER_LOCAL_FILESYSTEM_UNSUPPORTED")
}

func standaloneSafeDatabaseFile(string) error {
	return errors.New("SERVER_LOCAL_FILESYSTEM_UNSUPPORTED")
}

func standaloneNoACL(string) error { return errors.New("SERVER_LOCAL_FILESYSTEM_UNSUPPORTED") }
