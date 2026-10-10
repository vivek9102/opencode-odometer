package app

import (
	"encoding/json"
	"fmt"
	"github.com/vivek9102/opencode-odometer/internal/modelmeta"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (a *App) MetadataCatalog() *modelmeta.Catalog {
	a.metadataMu.Lock()
	defer a.metadataMu.Unlock()
	if a.metadata == nil {
		a.metadata = modelmeta.New(filepath.Join(a.experienceDir(), "model_metadata.json"))
	}
	return a.metadata
}
func (a *App) metadataKey() string {
	if key := os.Getenv("ARTIFICIAL_ANALYSIS_API_KEY"); key != "" {
		return key
	}
	var saved struct {
		Key string `json:"api_key"`
	}
	b, _ := os.ReadFile(filepath.Join(a.experienceDir(), "metadata_settings.json"))
	_ = json.Unmarshal(b, &saved)
	return saved.Key
}
func (a *App) HasMetadataKey() bool { return a.metadataKey() != "" }
func (a *App) SetMetadataKey(key string) error {
	b, _ := json.Marshal(map[string]string{"api_key": strings.TrimSpace(key)})
	return atomicExperienceFile(filepath.Join(a.experienceDir(), "metadata_settings.json"), b)
}
func (a *App) RefreshMetadata() error { return a.MetadataCatalog().Refresh(a.metadataKey()) }
func (a *App) MaybeRefreshMetadata() {
	if a.MetadataCatalog().NeedsRefresh(a.HasMetadataKey()) {
		if err := a.RefreshMetadata(); err != nil {
			a.LogEvent("model metadata refresh unavailable; cached facts retained")
		}
	}
}

type FallbackChoice struct {
	Alternative
	Metadata modelmeta.Metadata `json:"metadata"`
	Tags     []string           `json:"tags"`
	Eligible bool               `json:"eligible"`
	Reason   string             `json:"reason,omitempty"`
}

func (a *App) FallbackChoices(id string) []FallbackChoice {
	result := []FallbackChoice{}
	for _, m := range a.FallbackCandidates(id) {
		if m.Eligible {
			result = append(result, m)
		}
	}
	return result
}

// Include configured routes for accurate counts and disabled specialised cards.
// Eligibility remains a backend decision, and confirmation validates it again.
func (a *App) FallbackCandidates(id string) []FallbackChoice {
	rows := a.OpenSessions()
	var row OpenSessionRow
	for _, r := range rows {
		if r.ID == id {
			row = r
		}
	}
	if row.ID == "" {
		return []FallbackChoice{}
	}
	original := row.OriginalModel
	if original == "" {
		original = row.Model
	}
	configured := map[string]ConfiguredModel{}
	for _, m := range a.configuredModels() {
		configured[m.Key] = m
	}
	// Only the actual owning instance may provide automatic fallbacks.
	files, _ := filepath.Glob(filepath.Join(a.experienceDir(), "models-*.json"))
	owned := map[string]bool{}
	for _, file := range files {
		var inv ModelInventory
		b, _ := os.ReadFile(file)
		if json.Unmarshal(b, &inv) != nil || time.Now().Unix()-inv.Updated > 30 {
			continue
		}
		owns := inv.PID == row.PID
		for _, sid := range inv.Sessions {
			owns = owns || a.ChatRoot(sid) == row.SessionID && row.SessionID != ""
		}
		if owns {
			for _, m := range inv.Models {
				owned[m.Key] = true
				configured[m.Key] = m
			}
		}
	}
	choices := a.ModelChoices(original)
	result := []FallbackChoice{}
	for _, m := range choices {
		if !owned[m.Key] {
			continue
		}
		// Match the model popup's equal-input/output comparison. ModelChoices
		// also resolves the original price when it is absent from the inventory;
		// searching only the returned list used to treat that price as zero.
		cheaper := m.Cheaper
		cfg := configured[m.Key]
		meta := a.MetadataCatalog().Lookup(m.Key, m.Name, cfg.CanonicalModelID)
		if cfg.ToolCall != nil {
			meta.ToolCall = cfg.ToolCall
		}
		if cfg.Reasoning != nil {
			meta.Reasoning = cfg.Reasoning
		}
		if cfg.Context > 0 {
			meta.Context = cfg.Context
		}
		if len(cfg.InputModalities) > 0 {
			meta.InputModalities = cfg.InputModalities
		}
		// Absence of metadata is not proof that a configured chat route lacks
		// tools. Reject an explicit incompatibility, not an unrated alias.
		toolsUnsupported := meta.ToolCall != nil && !*meta.ToolCall
		eligible := !m.Current && m.Category == "chat" && !m.Unknown && cheaper && !toolsUnsupported
		reason := ""
		if !eligible {
			switch {
			case m.Category != "chat":
				reason = "Specialised model; cannot continue a chat."
			case m.Current:
				reason = "This is the original model."
			case m.Unknown:
				reason = "Pricing is not confirmed."
			case !cheaper:
				reason = "This configured route is not cheaper."
			default:
				reason = "This model does not support tools."
			}
		} else if meta.ToolCall == nil {
			reason = "Tool support metadata is unavailable."
		}
		tags := []string{}
		if meta.ToolCall != nil && *meta.ToolCall {
			tags = append(tags, "tools")
		}
		if meta.Reasoning != nil && *meta.Reasoning {
			tags = append(tags, "reasoning")
		}
		if meta.Coding != nil {
			tags = append(tags, "coding")
		}
		if meta.Speed != nil {
			tags = append(tags, "speed measured")
		}
		if m.Free {
			tags = append(tags, "free")
		}
		result = append(result, FallbackChoice{Alternative: m, Metadata: meta, Tags: tags, Eligible: eligible, Reason: reason})
	}
	sort.Slice(result, func(i, j int) bool {
		score := func(m FallbackChoice) float64 {
			if m.Metadata.Coding != nil {
				return *m.Metadata.Coding
			}
			if m.Metadata.Intelligence != nil {
				return *m.Metadata.Intelligence
			}
			return -1
		}
		x, y := score(result[i]), score(result[j])
		if x != y {
			return x > y
		}
		return result[i].Input+result[i].Output < result[j].Input+result[j].Output
	})
	return result
}
func (a *App) ValidateFallbackContext(id string, tokens int64) error {
	for _, r := range a.OpenSessions() {
		if r.ID == id {
			for _, m := range a.FallbackChoices(id) {
				if m.Key == r.FallbackModel && m.Metadata.Context > 0 && tokens > m.Metadata.Context {
					return fmt.Errorf("conversation exceeds fallback context capacity")
				}
			}
		}
	}
	return nil
}
