package wservice

import (
	"fmt"
	"github.com/vivek9102/opencode-odometer/internal/app"
)

func (s *Service) SetCounter(view string) { s.App.SetViewMode(view); s.refresh() }
func (s *Service) SetOpenSessionRule(id string, rule app.BudgetRule) error {
	if err := s.App.SetOpenSessionRule(id, rule); err != nil {
		return err
	}
	s.refresh()
	return nil
}
func (s *Service) ResumeOpenSession(id string) error {
	if err := s.App.ResumeOpenSession(id, .50); err != nil {
		return err
	}
	s.refresh()
	return nil
}
func (s *Service) FallbackModels(id string) []app.FallbackChoice { return s.App.FallbackCandidates(id) }
func (s *Service) HasMetadataKey() bool                          { return s.App.HasMetadataKey() }
func (s *Service) SetMetadataKey(key string) error               { return s.App.SetMetadataKey(key) }
func (s *Service) RefreshMetadata() string {
	if err := s.App.RefreshMetadata(); err != nil {
		return fmt.Sprintf("Metadata unavailable: %v. Cached facts retained.", err)
	}
	if !s.App.HasMetadataKey() {
		return "Capabilities updated. Add an Artificial Analysis key for benchmark scores."
	}
	return "Metadata updated; benchmark data from Artificial Analysis."
}

// Toast height is temporary: the compact bar keeps its approved width and
// returns to 46px after dismissal. SetPeek previously changed that width.
func (s *Service) SetRuleToast(on bool) {
	if s.Ctx == nil || !s.appCompact {
		return
	}
	s.SetPeek(on)
}

// Notification text determines height; the compact bar always remains 340x46.
func (s *Service) SetRuleToastSize(height int) {
	if s.Ctx == nil || !s.appCompact {
		return
	}
	if height <= 46 {
		s.setPeekSize(false, 46)
		return
	}
	if height > 240 {
		height = 240
	}
	s.setPeekSize(true, height)
}
