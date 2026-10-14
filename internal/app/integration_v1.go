//go:build !opencode_v2

package app

import _ "embed"

const IntegrationV2 = false
const IntegrationName = "OpenCode V1"
const ServerSourceName = "odometer.js"
const TerminalSourceName = "odometer-tui.tsx"

//go:embed odometer_plugin.js
var pluginSource []byte

//go:embed odometer_tui.tsx
var tuiSource []byte

var v2ModelSource []byte

func CheckOpenCodeCompatibility() error { return nil }

func registerTerminalPlugin(dir, entry string) error { return registerTUIPlugin(dir, entry) }
