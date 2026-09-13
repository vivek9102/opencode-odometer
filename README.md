# OpenCode Odometer

A live, always-on-top spend meter for [OpenCode](https://opencode.ai) — with a
per-session budget that can actually stop a turn before it costs you money.

```
 $ 0 0 3 1 . 4 7 2 9      $2.14/hr   claude-opus-4-5
```

A companion plugin feeds it every assistant message from inside OpenCode. It
prices each one from a local table you control and shows the running total in a
compact draggable bar. Click to expand into a full per-model breakdown.

**Single executable.** No runtime to install — it seeds its own price table and
installs its own plugin on first run.

> **Windows** is the tested platform. The Go core is portable and
> `internal/lock` already has a Unix implementation; the gaps are in window
> placement and packaging. See [Contributing](#contributing).

---

## Why not just use `opencode stats`?

For most people, `opencode stats` is fine — use it.

This exists for three cases it does not cover:

| | `opencode stats` | Odometer |
|---|---|---|
| **Live display** | run a command | always-on-screen bar, updates continuously |
| **Spend limits** | none | per-session cap that blocks the turn |
| **Providers that report no cost** | shows `$0.00` | prices locally from your own table |

That last row is the reason this was built. Some providers — internal company
gateways, resellers, self-hosted proxies — return **no cost field at all**. On
the setup this was written for, sampling 233 messages with real token usage
found **every single one reported `cost: 0`**, despite large cache reads. If
that is your situation, the built-in number is not slightly wrong, it is
meaningless. See [Private pricing](#private-pricing).

---

## Features

- **Live odometer** — mechanical-style counter, TRIP (resettable) and TOTAL.
- **Cache-aware pricing** — `input`, `output`, `cache_read` and `cache_write`
  priced separately. This matters more than it sounds: on real traffic cache
  reads were **83% of all tokens**, and pricing only input+output understates
  true cost by roughly **8×**.
- **Per-session budget**, tiered — `warn` / `soft` / `hard`. Counts only spend
  *from the moment you enable it*, so switching it on never retroactively locks
  a session in progress.
- **Blocks before the provider is called** — the plugin refuses the turn in
  `chat.message` and `tool.execute.before`, so a runaway agent loop is stopped
  rather than merely reported.
- **Auto-updating prices** — ~1,090 models from [models.dev](https://models.dev),
  refreshed in the background. A snapshot is compiled into the binary as an
  offline floor.
- **Private price overlay** for internal/reseller rates that never touches git.
- **Free-model savings** — models ending `-free` or containing `sovereign` cost
  nothing and accrue a "would have cost" figure.
- **Unknown-model prompt** — a model with no rate contributes $0 and is
  invisible to the budget, so the app surfaces it and offers the best public
  match (or a family-median fallback). Cache prices inherit from that public
  match unless you explicitly customize them. Saved prices remain editable.
- **Explainable hybrid accounting** — an explicit local/free setting wins,
  then a non-zero OpenCode event cost, then exact catalog pricing, then a
  deterministic cross-provider estimate. Each ledger record keeps its source.
- **CSV export** of every priced message.

---

## Install

### From a release

1. Download `OpenCode_Odometer.exe` from
   [Releases](https://github.com/vivek9102/opencode-odometer/releases).
2. Run it. On first launch it writes its price table and installs the plugin
   into `~/.config/opencode/plugins/`. It also remembers that exact executable,
   so it may remain in Downloads, Desktop, or any other folder. If you move it
   later, run it once from the new location to update the pointer.
3. **Restart OpenCode** so it loads the plugin.

Windows SmartScreen will warn on an unsigned binary — *More info → Run anyway*.

Requires the [WebView2 runtime](https://developer.microsoft.com/microsoft-edge/webview2/),
present by default on Windows 11 and current Windows 10.

### From source

```powershell
git clone https://github.com/vivek9102/opencode-odometer.git
cd opencode-odometer
go run .
```

For a packaged build you also need the [Wails v2 CLI](https://wails.io):

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@latest
.\build_exe.bat
```

`build_exe.bat` syncs embedded assets, regenerates the icon, and builds to
`build\bin\`. It fails loudly if the Wails CLI is missing rather than falling
back to `go build`, which silently produces a binary with no icon.

---

## How it works

OpenCode usually serves its API **in-process**, so there is no TCP port to
attach to. The plugin runs *inside* OpenCode, receives every assistant message,
and appends it to a spool file the odometer tails.

```
OpenCode
  └── plugin/odometer.js ──appends──> events.jsonl ──tailed──> Odometer
                                                                  │
                                          prices.json ────────────┤
                                          prices.local.json ──────┤
                                                                  ▼
                                                    ledger ──> budget.json
                                                                  │
                            plugin reads verdict <────────────────┘
```

An HTTP/SSE client is retained as a fallback for setups that do expose a port.
`stream disconnected: dial tcp 127.0.0.1:4096` in the log is **normal**, not a
fault — it means the in-process path is in use.

`budget.json` has exactly one writer (the odometer). When the plugin grants a
grace turn it records that in a separate `grace_claims.json`, which the
odometer absorbs and deletes — a one-way mailbox that avoids a write race. The
claim is written atomically and **fails closed**: if it cannot be persisted the
plugin blocks rather than granting grace it cannot account for.

---

## Pricing

Two files, and the distinction matters:

| File | What it is | On refresh |
|---|---|---|
| `prices.json` | Public catalogue from models.dev | **Overwritten** |
| `prices.local.json` | Your private/reseller rates | **Never touched** |

Refreshing public rates can therefore never destroy private ones.

Every figure is **USD per 1,000,000 tokens**.

### Private pricing

If your provider is not on models.dev — an internal gateway, a reseller, a
self-hosted proxy — put its rates in `prices.local.json`. Copy
`prices.local.example.json` to start. It is gitignored and merged on top of the
public table at load.

```json
{
  "models": {
    "yourprovider/some-model": {
      "name": "Some Model",
      "input": 2.50,
      "output": 10.00,
      "cache_read": 0.25,
      "cache_write": 3.13
    }
  }
}
```

The UI also prompts for any model it sees with no rate, pre-filled with the
median of that model's family.

### Refresh lifecycle

Startup reads the local table — **no network call**, so the UI never waits and
an offline machine still works. A background refresh runs shortly after and
every 6h thereafter, updating anything older than 24h. **UPDATE CATALOG**
forces it with visible success or failure. Catalog changes apply to future
messages only; completed totals do not move when a price file is refreshed.

Prices do move: a sample comparison found ~2% of entries changed within four
days, which is why the embedded snapshot is a floor rather than the source of
truth.

---

## Budget enforcement

**The limit applies per session, not globally.** Three chats with a $5 limit
means $15 of total headroom — it is a per-chat cap, not a shared pot. The bar
tracks the active session, since that is what will actually block.

| Mode | Behaviour |
|---|---|
| `warn` | Never blocks. Toast only. |
| `soft` *(default)* | Blocks at 100%, grants **one grace turn**, hard stop at 1.5×. |
| `hard` | Blocks at 100% immediately, no grace. |

Colours: green → amber (80%) → **orange** (over, still recoverable) → red (past
hard stop). Orange versus red is the useful distinction — whether raising the
limit can still rescue the session.

> A limit set while mode is `warn` blocks nothing. The budget label says
> "(warn only)" so this is visible rather than assumed.

---

## Controls

| Action | How |
|---|---|
| Expand / collapse | Double-click bar, or <kbd>Esc</kbd> to collapse |
| Move | Drag the bar |
| Cycle dock position | <kbd>F2</kbd> or `DOCK` |
| TRIP / TOTAL | Click the odometer or `TRIP / TOTAL` |
| Reset trip | `RESET TRIP` |
| Download catalog for future usage | `UPDATE CATALOG` |
| Review or update unknown-provider prices | `MODEL PRICES` / `PRICE TO CONFIRM` |
| Cheaper alternatives | `CHEAPER MODELS` |
| Abort the active session | `STOP SESSION` |
| Export ledger | `EXPORT CSV` |
| Quit / minimise | Right-click the bar |

---

## Configuration

| Variable | Effect |
|---|---|
| `OPENCODE_ODOMETER_DIR` | Override the data directory |
| `OPENCODE_ODOMETER_POINTER` | Override the plugin discovery pointer |
| `OPENCODE_ODOMETER_EXE` | Override the executable used for plugin auto-start |
| `OPENCODE_PLUGIN_DIR` | Override where the plugin installs |
| `OPENCODE_URL` | OpenCode server URL (default `http://127.0.0.1:4096`) |
| `OPENCODE_ODOMETER=0` | Disable the plugin and auto-launch entirely |
| `OPENCODE_ODOMETER_NOBLOCK=1` | Keep tracking, never refuse a turn |

Data lives in `%LOCALAPPDATA%\OpenCodeOdometer\` on Windows,
`~/Library/Application Support/OpenCodeOdometer/` on macOS, and
`$XDG_DATA_HOME/OpenCodeOdometer/` on Linux.

---

## Troubleshooting

The log is `opencode_odometer_events.log` in the data directory.

| Symptom | Cause |
|---|---|
| Everything reads `$0.00`, log shows `unknown=true` | No price table loaded |
| Cost stops moving | Check the discovery pointer (below) |
| `stream disconnected: dial tcp ...:4096` | **Normal** — in-process API |
| Window never appears | WebView2 runtime missing |
| Nothing happens on launch | Another instance holds the lock |

If cost stops updating, the plugin may be writing somewhere the odometer is not
reading. Check `~/.opencode-odometer.json` — its `data_dir` should match the
directory the app is using.

---

## Contributing

```powershell
go test ./...
go vet ./...
node --check plugin\odometer.js
node --check frontend\dist\main.js
```

See [CONTRIBUTING.md](CONTRIBUTING.md).

**Most useful contribution: macOS / Linux support.** The Go core is portable
and `internal/lock` already has a Unix build; the remaining work is window
placement, dock offsets, and packaging.

Two things to know before changing pricing or enforcement:

- **`frontend/dist` is source, not build output.** It is hand-written and
  embedded via `//go:embed`. There is no bundler.
- **Embedded assets are duplicated.** `//go:embed` cannot reach outside its
  package, so `plugin/odometer.js` and `prices.json` are copied into
  `internal/app/` and `internal/prices/`. `build_exe.bat` syncs them and a test
  fails the build if they drift.

---

## License

MIT — see [LICENSE](LICENSE).
