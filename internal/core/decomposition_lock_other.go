//go:build !darwin && !linux

package core

func (s *Service) lockPlan(parent string) (func(), error) {
	return nil, Error("PLAN_LOCK_UNSUPPORTED", "plan application requires a supported local locking implementation")
}
