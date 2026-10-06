//go:build !windows

package wservice

func startTray(s *Service) (func(), bool) { return func() {}, false }
