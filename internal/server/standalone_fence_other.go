//go:build !darwin && !linux

package server

import "errors"

func (s *Standalone) lockAuthorization(bool) (func(), error) {
	return nil, errors.New("SERVER_AUTHORIZATION_FENCE_UNSUPPORTED")
}
