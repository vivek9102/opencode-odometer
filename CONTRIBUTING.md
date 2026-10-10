# Contributing

OpenCode Odometer is a Go/Wails desktop app. Windows is the tested platform;
the core packages and OpenCode plugin are portable.

## Getting started

```powershell
git clone https://github.com/vivek9102/opencode-odometer.git
cd opencode-odometer
go run .
```

Use the Go version in `go.mod` and the Wails v2 CLI for a packaged build.
Close Odometer before running `build_exe.bat`. The script synchronizes embedded
assets and builds the executable with the application icon.

`frontend/dist` is hand-written source, embedded in the binary. There is no
frontend bundler. Keep `plugin/odometer.js` identical to
`internal/app/odometer_plugin.js`; an asset synchronization test checks this.

## Required checks

```powershell
go test -count=1 ./...
go vet ./...
go build ./...
node --check plugin/odometer.js
node --check plugin/event-diagnostics.js
node --check frontend/dist/main.js
node --check frontend/dist/experience.js
node --check frontend/dist/sessions.js
node --check frontend/dist/budget-rules.js
node --test plugin/*.test.mjs
git diff --check
```

Core tests use temporary files, mocks or local HTTP servers. They do not need
real provider credentials or inference requests. CI runs the Go checks,
JavaScript syntax checks, plugin regressions and tracked-data privacy checks.

## Optional smoke checks

- `node tools/experience-ui-smoke.cjs` uses Playwright with mock Wails services.
  Make Playwright available through `NODE_PATH` if needed; set
  `PLAYWRIGHT_CHROMIUM_EXECUTABLE` to a local browser executable to override
  Playwright's bundled Chromium.
- Set `OPENCODE_SMOKE_EXE` to an installed OpenCode executable, then run
  `node tools/opencode-startup-smoke.cjs` or
  `node tools/model-switch-smoke.cjs`. These launch isolated test instances;
  model routing uses a local fake provider, without paid inference.
- The diagnostic benchmark requires `OPENCODE_DIAGNOSTICS_BASELINE` pointing
  to the older synchronous logger. Run `node tools/event-diagnostics-bench.mjs`
  with synthetic events to compare it against disabled logging and summaries.

Smoke outputs stay under ignored `build/` directories. `OPENCODE_SMOKE_DIR`
overrides the real-OpenCode scripts' output directory. Do not point it at live
OpenCode or Odometer data.

## Accounting and routing rules

- Price cache reads and writes separately from ordinary input and output.
  Explicit private/free rates take precedence; provider-reported costs are
  used only according to the accounting precedence documented in the README.
- Messages are keyed by ID and updated as token usage arrives. Replayed history
  must not count twice or produce new live-charge animations.
- Parent and delegated child sessions share budget spend. Changing
  an enabled cap, resetting TRIP or restarting must preserve that period.
- Only Odometer writes `budget.json`. Open-TUI policies are hard with no grace.
  Strict model steps wait for preceding usage to be accounted before dispatch.
  The legacy bridge's heartbeat expiry still disables enforcement.
- Automatic fallback uses a generation-scoped stop/prepare/arm/continue
  handshake. Manual STOP cancels that generation. Never retry a continuation
  after uncertain delivery, or infer free pricing from a zero SDK price.
- Plugin initialization must return its hooks before awaiting SDK discovery.
- A saved model choice belongs to one chat and remains until replaced or
  cleared. Confirm it using the matching assistant response. OpenCode `/model`
  does not automatically clear an Odometer choice; preserve the explicit
  **Use OpenCode selection** handoff.

## Keep private data out of commits

Never publish runtime ledgers, budgets, event spools, inventories, switch
requests, logs, CSV exports, private prices, credentials or personal provider
configuration. Use generic provider names and synthetic IDs in fixtures.
Review the staged file list and diff before pushing:

```powershell
git diff --cached --stat
git diff --cached --check
git diff --cached -U0 | Select-String -Pattern "ses_[a-zA-Z0-9]{20}"
```

Only the icon, Windows manifest and Windows version metadata under `build/`
are source inputs. Other build contents and smoke artifacts are ignored.

## Reporting bugs

Include the OS, OpenCode version, whether using source or a packaged executable,
reproduction steps, and a short redacted log excerpt. Redact session IDs,
message content, costs, provider endpoints and credentials.
