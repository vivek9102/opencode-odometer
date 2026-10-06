package wservice

import (
	"fmt"
	"github.com/vivek9102/opencode-odometer/internal/app"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const peekHeight = 178

func (s *Service) Preferences() app.Preferences { return s.App.Preferences() }
func (s *Service) SetPreferences(p app.Preferences) error {
	if s.App == nil {
		return fmt.Errorf("app not initialized")
	}
	if err := s.App.SetPreferences(p); err != nil {
		return err
	}
	s.refresh()
	return nil
}
func (s *Service) SwitchModel(key, sessionID string) (app.ModelSwitch, error) {
	if s.App == nil {
		return app.ModelSwitch{}, fmt.Errorf("app not initialized")
	}
	if sessionID == "" {
		sessionID = s.ActiveSession()
	}
	return s.App.RequestModelSwitch(sessionID, key)
}
func (s *Service) ModelSwitchStatus(id string) app.ModelSwitch { return s.App.ModelSwitchStatus(id) }
func (s *Service) PendingModelSwitch(sid string) app.ModelSwitch {
	return s.App.PendingModelSwitch(sid)
}
func (s *Service) ClearModelSwitch(sid string) error { return s.App.ClearModelSwitch(sid) }

func (s *Service) Ready() {
	if s.Ctx == nil {
		return
	}
	s.applyPosition()
	runtime.WindowShow(s.Ctx)
	// OpenCode launches with windowsHide. Windows can apply that startup flag
	// to the first ShowWindow call, so repeat it after Wails records the first
	// show. Both calls are queued in order, after positioning the hidden frame.
	runtime.WindowShow(s.Ctx)
	s.trayStop, s.trayReady = startTray(s)
	s.App.LogEvent("tray ready=%v", s.trayReady)
}
func (s *Service) HideToTray() {
	if s.Ctx == nil {
		return
	}
	s.SetPeek(false)
	if s.trayReady {
		runtime.WindowHide(s.Ctx)
	} else {
		runtime.WindowMinimise(s.Ctx)
	}
}
func (s *Service) ShowFromTray() {
	if s.Ctx == nil {
		return
	}
	runtime.WindowUnminimise(s.Ctx)
	runtime.WindowShow(s.Ctx)
	s.applyPosition()
}
func (s *Service) TogglePause() {
	p := s.App.Preferences()
	p.Paused = !p.Paused
	_ = s.SetPreferences(p)
}

// Grow upward, leaving the compact bar under the pointer. Docked previews
// use the same work-area positioning as the bar; floating previews restore
// their exact original coordinates instead of persisting a temporary move.
func (s *Service) SetPeek(on bool) {
	if s.Ctx == nil || s.peek == on || (!s.appCompact && on) {
		return
	}
	if on {
		x, y := runtime.WindowGetPosition(s.Ctx)
		s.peekPosition = [2]int{x, y}
	}
	s.peek = on
	h := 46
	if on {
		h = peekHeight
	}
	runtime.WindowSetSize(s.Ctx, 360, h)
	if s.App.DockMode() {
		s.applyDock()
	} else {
		x, y := s.peekPosition[0], s.peekPosition[1]
		if on {
			y -= peekHeight - 46
		}
		if wa, ok := primaryWorkArea(); ok {
			x -= wa.Left
			y -= wa.Top
		}
		if y < 0 {
			y = 0
		}
		runtime.WindowSetPosition(s.Ctx, x, y)
	}
	s.refresh()
}
