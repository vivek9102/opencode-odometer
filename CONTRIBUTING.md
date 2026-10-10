# Contributing

OpenCode Odometer is a Go/Wails desktop application with an OpenCode companion
plugin and a hand-written JavaScript frontend. Windows is the tested desktop
platform. Contributions to portability, accounting, integrations and documentation
are welcome.

## Development setup

Use the Go version specified in `go.mod`.

```powershell
git clone https://github.com/vivek9102/opencode-odometer.git
cd opencode-odometer
go run .
```

Running the application can install its companion plugins and register its
executable location. Use isolated configuration and data directories when
testing changes that should not affect an existing installation.

For a packaged executable, install the Wails v2 CLI version used by `go.mod`,
then run:

```powershell
wails build
```

The Windows build includes the icon, manifest and version metadata from
`build/`. `build_exe.bat` also synchronizes assets, regenerates the icon and
installs the server plugin locally. It attempts to stop the standard executable
before building; use it only when those local installation effects are intended.

## Source layout

| Path | Purpose |
|---|---|
| `internal/app` | Accounting orchestration, budgets, routing and persistence |
| `internal/ledger` | Message accounting, counters and daily archives |
| `internal/prices` | Catalog lookup, private rates and estimates |
| `internal/wservice` | Wails bindings, window layout and tray integration |
| `internal/opencode`, `internal/spool`, `internal/plugin` | OpenCode transport and budget protocol |
| `plugin` | Server hooks, TUI integration and presence transport |
| `frontend/dist` | JavaScript, HTML and CSS embedded in the executable |
| `frontend/wailsjs` | Generated Wails bindings |
| `tools` | Browser and isolated OpenCode smoke checks |

`frontend/dist` is source, not generated build output; there is no frontend
bundler. Go embedding requires these source copies to stay synchronized:

| Canonical source | Embedded copy |
|---|---|
| `plugin/odometer.js` | `internal/app/odometer_plugin.js` |
| `plugin/odometer-tui.tsx` | `internal/app/odometer_tui.tsx` |
| `plugin/tui-presence.js` | `internal/app/tui_presence.js` |
| `prices.json` | `internal/prices/seed_prices.json` |

Synchronization tests detect drift. Regenerate Wails bindings when exported
service methods or their data types change.

## Required checks

```powershell
go test -count=1 . ./internal/...
go vet . ./internal/...
go build . ./internal/...
node --check plugin/odometer.js
node --check plugin/event-diagnostics.js
node --check frontend/dist/main.js
node --check frontend/dist/experience.js
node --check frontend/dist/sessions.js
node --check frontend/dist/budget-rules.js
node --check frontend/dist/usage.js
node --test plugin/*.test.mjs
git diff --check
```

The explicit Go package scope avoids including temporary source copies under
ignored build directories. Core tests use temporary files, mocks or local HTTP
servers; they do not need provider credentials. CI also checks for tracked
runtime data and private identifiers.

## Browser and integration checks

`tools/experience-ui-smoke.cjs` and `tools/multi-session-ui-smoke.cjs` use
Playwright and synthetic Wails services. Make Playwright available to Node,
using `NODE_PATH` if needed. `PLAYWRIGHT_CHROMIUM_EXECUTABLE` can select an installed
browser instead of Playwright's bundled Chromium.

```powershell
node tools/experience-ui-smoke.cjs
node tools/multi-session-ui-smoke.cjs
```

For isolated OpenCode integration checks, set `OPENCODE_SMOKE_EXE` to an installed
OpenCode executable. The harnesses use a localhost fake provider and separate
configuration/session storage. Do not point their output directories at live
OpenCode or Odometer data.

```powershell
$env:OPENCODE_SMOKE_EXE = 'C:\path\to\opencode.exe'
node tools/opencode-startup-smoke.cjs
node tools/model-switch-smoke.cjs
$env:OPENCODE_SMOKE_AUTO_FALLBACK = '1'
node tools/model-switch-smoke.cjs
$env:OPENCODE_SMOKE_TTY = '1'
node tools/multi-session-tui-smoke.cjs
```

`OPENCODE_SMOKE_DIR` overrides the integration output directory. Smoke artifacts
stay under ignored `build/` paths by default. Integration results with a fake
provider do not prove capacity or compatibility with every real provider.

`tools/native-window-smoke.cjs` checks native WebView2 layout using an isolated,
instrumented test executable selected by `OPENCODE_NATIVE_SMOKE_EXE`. It requires
the test runtime's browser-debugging hooks; it cannot attach to a normal release
executable. Keep instrumentation out of release builds.

## Accounting and routing invariants

- Account for cache reads and writes separately. Preserve the cost-source
  precedence described in [the pricing guide](docs/PRICING.md).
- Update messages by ID as usage arrives. Replayed history must not count twice
  or trigger new live-charge animations.
- Enabled-limit edits, display-counter resets and widget restarts preserve
  allowance spending. Disabling and re-enabling starts a fresh allowance.
- Parent and delegated child conversations share the budget. Only Odometer
  writes `budget.json`; session policies have no grace turn. An expired verdict
  disables enforcement, so heartbeat publication must continue during idle periods.
- Automatic fallback uses generation-scoped cancellation and continuation.
  Never retry a continuation after uncertain delivery. Manual STOP suppresses
  continuation of the cancelled task.
- Plugin initialization returns hooks before waiting for SDK discovery.
- Manual saved model choices belong to one chat. Budget-owned routes must be
  released with their allowance, without creating a persistent original-model pin.
- Archive daily accounting before pruning message details. Exports must use
  consistent row/footer scopes. Do not invent dates for undated legacy totals.

See [the budget guide](docs/BUDGET_RULES.md) for user-visible behaviour and
limitations.

## Optional event diagnostics

`plugin/event-diagnostics.js` is a separate troubleshooting plugin, disabled by
default. Copy it into OpenCode's plugin directory and set
`OPENCODE_EVENT_DIAGNOSTICS=1` before starting OpenCode. Remove an older full-event
logger if one is installed to avoid duplicate logging.

It records metadata summaries rather than prompt/response text, coalesces updates,
flushes asynchronously and rotates at 1 MiB with one previous file. Summaries
still contain session/message identifiers, model names and usage; keep them
private. Unflushed entries can be lost on exit. Accounting uses a separate spool.

`OPENCODE_EVENT_DIAGNOSTICS_FILE` overrides the default
`~/.opencode/test-events.log`. The benchmark script
`tools/event-diagnostics-bench.mjs` uses synthetic events and requires
`OPENCODE_DIAGNOSTICS_BASELINE` to identify a comparison logger.

## Privacy and screenshots

Do not commit runtime ledgers, budgets, event spools, inventories, routing commands,
logs, CSV exports, private prices, keys or personal provider configuration.
Use generic providers, synthetic identifiers and demo spending in fixtures and
screenshots. The browser smoke writes image candidates under
`build/multi-session-ui`; inspect them before copying into `docs/screenshots`.

Before committing, review the staged file list, diff and privacy checks:

```powershell
git diff --cached --stat
git diff --cached --check
git diff --cached -U0 | Select-String -Pattern 'ses_[a-zA-Z0-9]{20}'
```

Only the icon, Windows manifest and version metadata under `build/` are tracked
source inputs. Other build contents and smoke artifacts remain ignored.

## Reporting bugs

Include OS and OpenCode versions, packaged/source installation details,
reproduction steps, expected/actual behaviour and a short redacted log excerpt.
Remove session IDs, conversation content, costs, provider endpoints and
credentials before sharing artifacts.
