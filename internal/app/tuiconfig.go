package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Register one plugin without rewriting other settings or JSONC comments.
func registerTUIPlugin(dir, entry string) error {
	path := filepath.Join(dir, "tui.json")
	if _, err := os.Stat(filepath.Join(dir, "tui.jsonc")); err == nil {
		path = filepath.Join(dir, "tui.jsonc")
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		raw = []byte("{}\n")
	} else if err != nil {
		return err
	}
	clean := jsoncForInspection(raw)
	var cfg map[string]json.RawMessage
	if err = json.Unmarshal(clean, &cfg); err != nil || cfg == nil {
		return fmt.Errorf("cannot safely register TUI companion in %s: invalid JSON/JSONC", path)
	}
	entry = filepath.ToSlash(entry)
	encoded, _ := json.Marshal(entry)
	if value, ok := cfg["plugin"]; ok {
		var plugins []json.RawMessage
		if json.Unmarshal(value, &plugins) != nil || len(value) == 0 || value[0] != '[' {
			return fmt.Errorf("TUI plugin setting must be an array")
		}
		for _, p := range plugins {
			var existing string
			if json.Unmarshal(p, &existing) == nil && strings.TrimPrefix(filepath.ToSlash(existing), "file:///") == strings.TrimPrefix(entry, "file:///") {
				return nil
			}
		}
		depth := 0
		for i := 0; i < len(clean); i++ {
			switch clean[i] {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			case '"':
				start := i
				i++
				for i < len(clean) {
					if clean[i] == '\\' {
						i += 2
						continue
					}
					if clean[i] == '"' {
						break
					}
					i++
				}
				var key string
				json.Unmarshal(clean[start:i+1], &key)
				if depth != 1 || key != "plugin" {
					continue
				}
				j := i + 1
				for j < len(clean) && strings.ContainsRune(" \n\r\t:", rune(clean[j])) {
					j++
				}
				if j >= len(clean) || clean[j] != '[' {
					continue
				}
				insert := append([]byte{}, encoded...)
				if len(plugins) > 0 {
					insert = append(insert, ',')
				}
				body := append(append(append([]byte{}, raw[:j+1]...), insert...), raw[j+1:]...)
				return atomicExperienceFile(path, body)
			}
		}
		return fmt.Errorf("cannot locate the TUI plugin array safely")
	}
	start := strings.IndexByte(string(clean), '{') + 1
	insert := []byte("\n  \"plugin\": [" + string(encoded) + "]")
	if len(cfg) > 0 {
		insert = append(insert, ',')
	}
	insert = append(insert, '\n')
	body := append(append(append([]byte{}, raw[:start]...), insert...), raw[start:]...)
	return atomicExperienceFile(path, body)
}

// Mask comments and trailing commas while preserving byte offsets for edits.
func jsoncForInspection(raw []byte) []byte {
	clean := append([]byte{}, raw...)
	quoted := false
	for i := 0; i < len(clean); i++ {
		if quoted {
			if clean[i] == '\\' {
				i++
				continue
			}
			if clean[i] == '"' {
				quoted = false
			}
			continue
		}
		if clean[i] == '"' {
			quoted = true
			continue
		}
		if clean[i] == '/' && i+1 < len(clean) && clean[i+1] == '/' {
			for i < len(clean) && clean[i] != '\n' {
				clean[i] = ' '
				i++
			}
			i--
			continue
		}
		if clean[i] == '/' && i+1 < len(clean) && clean[i+1] == '*' {
			clean[i] = ' '
			i++
			clean[i] = ' '
			for i+1 < len(clean) && !(clean[i] == '*' && clean[i+1] == '/') {
				clean[i] = ' '
				i++
			}
			if i+1 < len(clean) {
				clean[i] = ' '
				i++
				clean[i] = ' '
			}
		}
	}
	quoted = false
	for i := 0; i < len(clean); i++ {
		if quoted {
			if clean[i] == '\\' {
				i++
				continue
			}
			if clean[i] == '"' {
				quoted = false
			}
			continue
		}
		if clean[i] == '"' {
			quoted = true
			continue
		}
		if clean[i] == ',' {
			j := i + 1
			for j < len(clean) && strings.ContainsRune(" \t\r\n", rune(clean[j])) {
				j++
			}
			if j < len(clean) && (clean[j] == '}' || clean[j] == ']') {
				clean[i] = ' '
			}
		}
	}
	return clean
}
