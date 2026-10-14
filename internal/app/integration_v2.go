//go:build opencode_v2

package app

import (
	_ "embed"
	"fmt"
	"regexp"
)

const IntegrationV2 = true
const IntegrationName = "OpenCode V2"
const ServerSourceName = "odometer-v2.js"
const TerminalSourceName = "odometer-v2-tui.tsx"

//go:embed odometer_v2_plugin.js
var pluginSource []byte

//go:embed odometer_v2_tui.tsx
var tuiSource []byte

//go:embed v2_models.js
var v2ModelSource []byte

//go:embed v2_sessions.js
var v2SessionSource []byte

// Never replace a V1 installation with V2 files based on an unknown version.
func CheckOpenCodeCompatibility() error {
	out, err := probeOpenCodeVersion()
	if err != nil {
		return fmt.Errorf("cannot verify OpenCode V2 before installing plugins: %w", err)
	}
	return validateV2Version(out)
}

func validateV2Version(version string) error {
	if !regexp.MustCompile(`(?:^|\s|v)2\.\d+\.\d+(?:\s|$|[-+])`).MatchString(version) {
		return fmt.Errorf("this executable is for OpenCode V2; detected %q", version)
	}
	return nil
}

func registerTerminalPlugin(dir, entry string) error { return registerCLIPlugin(dir, entry) }
