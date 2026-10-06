package app

import (
	"github.com/vivek9102/opencode-odometer/internal/opencode"
	"time"
)

type Charge struct {
	Sequence uint64  `json:"sequence"`
	ID       string  `json:"id"`
	Cost     float64 `json:"cost"`
}

// Only messages first observed live may produce a completed-charge tick.
// Existing history, duplicate finishes and old backfill are silent.
func (a *App) trackCharge(m opencode.Message, cost float64, existed bool) {
	if !a.Seeded() {
		return
	}
	a.rateMu.Lock()
	defer a.rateMu.Unlock()
	if a.liveCharges == nil {
		a.liveCharges = map[string]float64{}
		a.charged = map[string]bool{}
	}
	if a.charged[m.ID] {
		return
	}
	_, tracked := a.liveCharges[m.ID]
	age := time.Since(time.UnixMilli(m.TimeCreated))
	if !tracked && (existed || m.TimeCreated <= 0 || age < 0 || age > time.Minute) {
		return
	}
	a.liveCharges[m.ID] = cost
	if m.Finish == "" {
		return
	}
	a.charged[m.ID] = true
	delete(a.liveCharges, m.ID)
	if cost > 0 {
		a.lastCharge = Charge{Sequence: a.lastCharge.Sequence + 1, ID: m.ID, Cost: cost}
		a.charges = append(a.charges, a.lastCharge)
		if len(a.charges) > 64 {
			a.charges = a.charges[len(a.charges)-64:]
		}
	}
	// Bound both maps. Historical entries remain silent even after pruning
	// because the ledger already owns their IDs.
	if len(a.charged) > 2000 {
		a.charged = map[string]bool{m.ID: true}
	}
	if len(a.liveCharges) > 2000 {
		a.liveCharges = map[string]float64{}
	}
}
func (a *App) LastCharge() Charge { a.rateMu.Lock(); defer a.rateMu.Unlock(); return a.lastCharge }

func (a *App) RecentCharges() []Charge {
	a.rateMu.Lock()
	defer a.rateMu.Unlock()
	return append([]Charge(nil), a.charges...)
}
