//go:build !windows

package wservice

// workArea is the usable desktop rectangle, i.e. the monitor minus any
// taskbar/panel.
type workArea struct {
	Left, Top, Right, Bottom int
}

func (w workArea) width() int  { return w.Right - w.Left }
func (w workArea) height() int { return w.Bottom - w.Top }

// primaryWorkArea is unavailable outside Windows; callers fall back to the
// screen size reported by Wails minus a panel margin.
func primaryWorkArea() (workArea, bool) { return workArea{}, false }

// windowRect is Windows-only; elsewhere the requested position is trusted.
func (s *Service) windowRect() (left, top int, ok bool) { return 0, 0, false }
