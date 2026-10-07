package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vivek9102/opencode-odometer/internal/prices"
)

// Preferences are separate from the spend ledger: changing startup behaviour
// never touches budget baselines, grace claims or message accounting.
type Preferences struct {
	AutoStart    bool `json:"auto_start"`
	AutoCollapse bool `json:"auto_collapse"`
	IdleDim      bool `json:"idle_dim"`
	Remaining    bool `json:"remaining"`
	Paused       bool `json:"paused"`
}

func (a *App) experienceDir() string { return filepath.Dir(a.Contract.BudgetFile) }
func (a *App) Preferences() Preferences {
	p := Preferences{AutoStart: true, IdleDim: true}
	b, err := os.ReadFile(filepath.Join(a.experienceDir(), "preferences.json"))
	if err == nil {
		_ = json.Unmarshal(b, &p)
	}
	return p
}
func (a *App) SetPreferences(p Preferences) error {
	target := filepath.Join(a.experienceDir(), "preferences.json")
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err = atomicExperienceFile(target, b); err != nil {
		return err
	}
	a.PublishBudget()
	return nil
}
func atomicExperienceFile(target string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(target), ".odometer-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

type ModelPrice struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}
type ConfiguredModel struct {
	Key      string      `json:"key"`
	Name     string      `json:"name"`
	Category string      `json:"category"`
	Rate     *ModelPrice `json:"rate,omitempty"`
}
type ModelInventory struct {
	AbortProtocol  int               `json:"abort_protocol"`
	SwitchProtocol int               `json:"switch_protocol"`
	PID            int               `json:"pid"`
	Instance       string            `json:"instance"`
	Sessions       []string          `json:"sessions"`
	Updated        int64             `json:"updated"`
	Models         []ConfiguredModel `json:"models"`
}

// Prefer live plugin inventories (one per OpenCode process). A global config
// fallback keeps custom models visible until OpenCode loads the upgraded plugin.
func (a *App) configuredModels() []ConfiguredModel {
	entries := map[string]ConfiguredModel{}
	files, _ := filepath.Glob(filepath.Join(a.experienceDir(), "models-*.json"))
	files = append(files, filepath.Join(a.experienceDir(), "models.json"))
	for _, file := range files {
		var inv ModelInventory
		raw, err := os.ReadFile(file)
		if err != nil || json.Unmarshal(raw, &inv) != nil {
			continue
		}
		if inv.Updated > 0 && time.Now().Unix()-inv.Updated > 30 {
			continue
		}
		for _, m := range inv.Models {
			if strings.Contains(m.Key, "/") {
				entries[m.Key] = m
			}
		}
	}
	if len(entries) == 0 {
		home, _ := os.UserHomeDir()
		cfgRoot := os.Getenv("XDG_CONFIG_HOME")
		if cfgRoot == "" {
			cfgRoot = filepath.Join(home, ".config")
		}
		paths := []string{filepath.Join(cfgRoot, "opencode", "opencode.json"), filepath.Join(home, ".opencode", "opencode.json")}
		if custom := os.Getenv("OPENCODE_CONFIG"); custom != "" {
			paths = append(paths, custom)
		}
		for _, path := range paths {
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var cfg struct {
				Provider map[string]struct {
					Models map[string]struct {
						Name string      `json:"name"`
						Cost *ModelPrice `json:"cost"`
					} `json:"models"`
				} `json:"provider"`
				Disabled []string `json:"disabled_providers"`
				Enabled  []string `json:"enabled_providers"`
			}
			if json.Unmarshal(raw, &cfg) != nil {
				continue
			}
			for provider, p := range cfg.Provider {
				allowed := len(cfg.Enabled) == 0
				for _, id := range cfg.Enabled {
					if id == provider {
						allowed = true
					}
				}
				for _, id := range cfg.Disabled {
					if id == provider {
						allowed = false
					}
				}
				if !allowed {
					continue
				}
				for id, m := range p.Models {
					key := provider + "/" + id
					entries[key] = ConfiguredModel{Key: key, Name: m.Name, Rate: m.Cost}
				}
			}
		}
	}
	out := make([]ConfiguredModel, 0, len(entries))
	for _, m := range entries {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func modelCategory(key string) string {
	k := strings.ToLower(key)
	switch {
	case strings.Contains(k, "embed"), strings.Contains(k, "e5-"), strings.Contains(k, "rerank"):
		return "embedding"
	case strings.Contains(k, "image"), strings.Contains(k, "ocr"):
		return "image"
	case strings.Contains(k, "asr"), strings.Contains(k, "realtime"), strings.Contains(k, "audio"), strings.Contains(k, "tts"):
		return "audio"
	default:
		return "chat"
	}
}

// freeModelKeys publishes the price book's classification to budget hooks.
func (a *App) freeModelKeys() []string {
	var keys []string
	for _, m := range a.configuredModels() {
		r := a.Prices.Entry(m.Key)
		if r.Free && !r.Unknown && (m.Category == "chat" || m.Category == "" && modelCategory(m.Key) == "chat") {
			keys = append(keys, m.Key)
		}
	}
	sort.Strings(keys)
	return keys
}

// ModelChoices lists configured models, including nano, Gemma and specialised
// models. Cheaper compares both input and output at an equal token mix; the UI
// states this basis rather than promising savings for an unknown future turn.
func (a *App) ModelChoices(current string) []Alternative {
	models := a.configuredModels()
	rateFor := func(m ConfiguredModel) prices.Rate {
		r := a.Prices.Entry(m.Key)
		if m.Rate != nil && !a.Prices.IsLocal(m.Key) && !r.Free && (m.Rate.Input > 0 || m.Rate.Output > 0) {
			r = prices.Rate{Input: m.Rate.Input, Output: m.Rate.Output, CacheRead: m.Rate.CacheRead, CacheWrite: m.Rate.CacheWrite, Source: "OpenCode provider"}
		}
		return r
	}
	base := rateFor(ConfiguredModel{Key: current})
	for _, m := range models {
		if m.Key == current {
			base = rateFor(m)
		}
	}
	baseCost := base.Input + base.Output
	out := make([]Alternative, 0, len(models))
	for _, m := range models {
		r := rateFor(m)
		provider, _, _ := strings.Cut(m.Key, "/")
		name := m.Name
		if name == "" {
			name = strings.TrimPrefix(m.Key, provider+"/")
		}
		category := m.Category
		if category == "" {
			category = modelCategory(m.Key)
		}
		x := Alternative{Key: m.Key, Name: name, Provider: provider, Input: r.Input, Output: r.Output, Free: r.Free, Unknown: r.Unknown, Estimated: strings.HasPrefix(r.Source, "estimate from "), Source: r.Source, Category: category, Current: m.Key == current}
		if baseCost > 0 && !base.Unknown && !r.Unknown {
			x.Ratio = (r.Input + r.Output) / baseCost
			x.Cheaper = x.Ratio < 1
			x.Comparable = true
		}
		if x.Free {
			x.Cheaper = true
		}
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Free != b.Free {
			return a.Free
		}
		if a.Unknown != b.Unknown {
			return !a.Unknown
		}
		if a.Input+a.Output != b.Input+b.Output {
			return a.Input+a.Output < b.Input+b.Output
		}
		return a.Key < b.Key
	})
	return out
}

type ModelSwitch struct {
	ID         string `json:"id"`
	SessionID  string `json:"session_id"`
	Key        string `json:"key"`
	Status     string `json:"status"`
	Detail     string `json:"detail"`
	Issued     int64  `json:"issued"`
	MessageID  string `json:"message_id,omitempty"`
	Persistent bool   `json:"persistent,omitempty"`
}

func (a *App) RequestModelSwitch(sid, key string) (ModelSwitch, error) {
	if sid == "" {
		return ModelSwitch{}, fmt.Errorf("no active chat; start a chat in OpenCode first")
	}
	if strings.ContainsAny(sid, "/\\.") {
		return ModelSwitch{}, fmt.Errorf("invalid chat ID")
	}
	var available, compatible bool
	files, _ := filepath.Glob(filepath.Join(a.experienceDir(), "models-*.json"))
	sort.Slice(files, func(i, j int) bool { return files[i] > files[j] })
	for _, file := range files {
		var inv ModelInventory
		b, _ := os.ReadFile(file)
		if json.Unmarshal(b, &inv) != nil || time.Now().Unix()-inv.Updated > 30 {
			continue
		}
		compatible = compatible || inv.SwitchProtocol >= 3
		for _, m := range inv.Models {
			if m.Key == key && inv.SwitchProtocol >= 3 {
				if m.Category != "" && m.Category != "chat" {
					return ModelSwitch{}, fmt.Errorf("this specialised model cannot run a coding chat")
				}
				available = true
				break
			}
		}
		if available {
			break
		}
	}
	if !compatible {
		return ModelSwitch{}, fmt.Errorf("restart OpenCode once to load the model-switch update; no selection was saved")
	}
	if !available {
		return ModelSwitch{}, fmt.Errorf("this model is unavailable in the running OpenCode instance")
	}
	// Publish the free-model classification before making a selection visible
	// to a prompt, including providers that appeared since the last poll.
	a.PublishBudget()
	target := filepath.Join(a.experienceDir(), "switch-session-"+sid+".json")
	var previous ModelSwitch
	old, _ := os.ReadFile(target)
	_ = json.Unmarshal(old, &previous)
	req := ModelSwitch{ID: fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano()), SessionID: sid, Key: key, Persistent: true, Status: "queued", Detail: "Selected for this chat, starting with your next message. Stays active until you choose another model or Follow OpenCode.", Issued: time.Now().Unix()}
	b, _ := json.Marshal(req)
	// Publish acknowledgment before the request: a fast prompt must never have
	// its applied/confirmed status overwritten by the queued status.
	if err := atomicExperienceFile(filepath.Join(a.experienceDir(), "switch-status-"+req.ID+".json"), b); err != nil {
		return ModelSwitch{}, err
	}
	if err := atomicExperienceFile(target, b); err != nil {
		return ModelSwitch{}, err
	}
	if previous.ID != "" && previous.SessionID == sid && !strings.ContainsAny(previous.ID, "/\\.") {
		previous.Status, previous.Detail = "cancelled", "Replaced by a newer selection."
		old, _ = json.Marshal(previous)
		_ = atomicExperienceFile(filepath.Join(a.experienceDir(), "switch-status-"+previous.ID+".json"), old)
	}
	return req, nil
}
func (a *App) ModelSwitchStatus(id string) ModelSwitch {
	var status ModelSwitch
	if id == "" || strings.ContainsAny(id, "/\\.") {
		return status
	}
	b, _ := os.ReadFile(filepath.Join(a.experienceDir(), "switch-status-"+id+".json"))
	_ = json.Unmarshal(b, &status)
	return status
}

func (a *App) PendingModelSwitch(sid string) ModelSwitch {
	var pending ModelSwitch
	if sid == "" || strings.ContainsAny(sid, "/\\.") {
		return pending
	}
	b, _ := os.ReadFile(filepath.Join(a.experienceDir(), "switch-session-"+sid+".json"))
	_ = json.Unmarshal(b, &pending)
	if status := a.ModelSwitchStatus(pending.ID); status.ID != "" {
		return status
	}
	return pending
}

func (a *App) ClearModelSwitch(sid string) error {
	if sid == "" || strings.ContainsAny(sid, "/\\.") {
		return fmt.Errorf("invalid chat ID")
	}
	previous := a.PendingModelSwitch(sid)
	err := os.Remove(filepath.Join(a.experienceDir(), "switch-session-"+sid+".json"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if previous.ID != "" {
		previous.Status, previous.Detail = "cancelled", "This chat now follows OpenCode's model selection."
		b, _ := json.Marshal(previous)
		return atomicExperienceFile(filepath.Join(a.experienceDir(), "switch-status-"+previous.ID+".json"), b)
	}
	return nil
}

// RecentSpend returns thirty 20-second buckets; it shares live-only samples
// with the burn-rate calculation, so historical backfill cannot spike the plot.
func (a *App) RecentSpend() []float64 {
	out := make([]float64, 30)
	now := time.Now()
	a.rateMu.Lock()
	defer a.rateMu.Unlock()
	for _, pt := range a.rateWindow {
		age := now.Sub(pt.Time)
		if age >= 0 && age < 10*time.Minute {
			out[29-int(age/(20*time.Second))] += pt.Cost
		}
	}
	return out
}

// Suppress relaunch only in currently running OpenCode instances. A fresh
// OpenCode launch still follows the user's persisted startup preference.
func (a *App) SuppressRelaunch() error {
	var instances []string
	files, _ := filepath.Glob(filepath.Join(a.experienceDir(), "models-*.json"))
	for _, file := range files {
		var inv ModelInventory
		b, _ := os.ReadFile(file)
		if json.Unmarshal(b, &inv) == nil && time.Now().Unix()-inv.Updated <= 30 && inv.Instance != "" {
			instances = append(instances, inv.Instance)
		}
	}
	b, _ := json.Marshal(map[string]any{"instances": instances})
	return atomicExperienceFile(filepath.Join(a.experienceDir(), "launch-suppressed.json"), b)
}
