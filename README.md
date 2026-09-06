# OpenCode Odometer

A live, always-on-top spend meter for [OpenCode](https://opencode.ai) — with a
per-session budget that can actually stop a turn before it costs you money.

```
 $ 0 0 3 1 . 4 7 2 9      $2.14/hr   claude-opus-4-5
```

It polls OpenCode's own SQLite database once a second, prices every assistant
message from a local table you control, and shows the running total in a
compact draggable bar. Click it to expand into a full per-model breakdown.

> **Windows only** for now. The launcher plugin and the frameless bar rely on
> Win32 behaviour. The core is stdlib Python and should port to macOS/Linux
> without much work — see [Contributing](#contributing).

---

## Why not just use `opencode stats`?

For most people, `opencode stats` is fine — use it.

This exists for three cases it does not cover:

| | `opencode stats` | Odometer |
|---|---|---|
| **Live display** | run a command | always-on-screen bar, updates every 1s |
| **Spend limits** | none | per-session cap that blocks the turn |
| **Providers that report no cost** | shows `$0.00` | prices locally from your own table |

That last row is the reason this was built. Some providers — internal company
gateways, resellers, self-hosted proxies — return **no cost field at all**. On
the setup this was written for, `opencode stats` reported **$0.04** against a
real spend of **~$322**, because 79% of traffic went through such a provider.
If that is your situation, the built-in number is not slightly wrong, it is
meaningless. See [Private pricing](#private-pricing).

---

## Features

- **Live odometer** — mechanical-style counter, TRIP (resettable) and TOTAL.
- **Cache-aware pricing** — `input`, `output`, `cache_read` and `cache_write`
  are priced separately. This matters more than it sounds: on real data cache
  reads were **83% of all tokens**. Pricing only input+output understates the
  true cost by roughly **8x**.
- **Session budget** — warn at 80%, block at 100%. Counts only spend *from the
  moment you enable it*, so switching it on never retroactively locks a session.
- **Free-model savings** — models ending `-free` or containing `sovereign` are
  detected automatically, cost nothing, and accrue a "would have cost" figure.
- **1,100+ models priced** out of the box, generated from
  [models.dev](https://models.dev).
- **Private price overlay** for internal/reseller rates that never touches git.
- **Read-only and safe** — the OpenCode database is snapshot-copied before
  reading. The app never writes to it and cannot corrupt your sessions.
- **CSV export** of every priced message.

---

## Install

**Requirements:** Windows, Python 3.9+ (only if running from source), and
OpenCode installed.

```powershell
git clone https://github.com/vivek9102/opencode-odometer.git
cd opencode-odometer
python opencode_monitor.py
```

### Build a standalone .exe

```powershell
pip install pyinstaller
.\build_exe.bat
```

Produces `dist\OpenCode_Odometer.exe` (~10 MB, no Python needed).

### Auto-start with OpenCode

Copy the plugin into your OpenCode config:

```powershell
Copy-Item plugin\odometer.js "$env:USERPROFILE\.config\opencode\plugins\"
```

It launches the odometer when OpenCode starts and enforces the budget. If the
repo is not in a default location, point the plugin at it:

```powershell
setx OPENCODE_ODOMETER_HOME "C:\path\to\opencode-odometer"
```

---

## Usage

| Action | How |
|---|---|
| Expand / collapse | Click the bar, or `Esc` to collapse |
| Move it | Drag the bar anywhere |
| Re-dock | Right-click → Dock, or `F2` to cycle |
| TRIP / TOTAL | Button in expanded view, or right-click menu |
| Export CSV | Button in expanded view |

### Setting a session limit

1. Expand the window
2. Tick **SESSION LIMIT**
3. Type a dollar amount and press `Enter`
4. Pick an enforcement **mode**

Only spend *after* you tick the box counts toward the cap, so enabling a limit
never retroactively blocks a session you have already spent money on.

### Enforcement modes

One threshold is too blunt: "slightly over on a task I'm about to finish" and
"an agent loop is burning money while I'm away" deserve different treatment.

| Mode | At 80% | At 100% | At 150% |
|---|---|---|---|
| `warn` | toast | toast, never blocks | toast |
| `soft` *(default)* | toast | **one grace turn**, then block | hard stop, no grace |
| `hard` | toast | block immediately | block |

**`soft` is the default** because a hard failure at exactly 100% loses the
prompt you just typed. The grace turn lets a nearly-finished task land, warns
you clearly, and blocks everything after it. The 150% ceiling exists so grace
can never be abused by a runaway loop.

Blocks happen **before** the request is sent, so a refused turn costs nothing.

When you are blocked, a panel shows what spent the money and lists cheaper
models — **click any of them to copy the model id** — then offers: allow more,
double the limit, or turn enforcement off.

> **Model switching is advice only.** No OpenCode plugin hook can reassign a
> model mid-turn — `chat.message` exposes it as read-only, and `chat.params`
> only allows temperature/topP/topK/maxOutputTokens. Silently downgrading a
> model mid-task would also produce confusing output, so the Odometer copies
> the id and lets you switch with `Ctrl+P → Switch Model`.

### Escape hatches

| | |
|---|---|
| `OPENCODE_ODOMETER_NOBLOCK=1` | keep tracking, stop blocking |
| `OPENCODE_ODOMETER=0` | disable the plugin entirely |
| Odometer not running | ledger goes stale after 120s → blocking auto-disables |

A crashed odometer can never lock you out of OpenCode. This is tested
explicitly.

---

## Pricing

> Full details in **[docs/PRICING.md](docs/PRICING.md)** — pulling from
> models.dev, custom/private rates, reseller markups, invoice verification.

`prices.json` is authoritative — the app always trusts it. Figures are **USD
per 1,000,000 tokens**.

Regenerate from models.dev at any time:

```powershell
python tools\gen_prices.py
```

To change a price, edit `prices.json` and click **RELOAD PRICES**. All history
is re-priced retroactively.

### Private pricing

If your provider does not report cost — an internal gateway, a reseller, a
proxy — put those rates in a **local overlay** instead of editing the shipped
table. It is merged on top at startup and is gitignored, so regenerating never
clobbers it and your internal rates never reach GitHub.

```powershell
Copy-Item prices.local.example.json `
  "$env:LOCALAPPDATA\OpenCodeOdometer\prices.local.json"
```

Then edit it. An overlay can also override `reference_model`, the paid model
used to value free-model savings.

> **Tip:** resellers often apply a flat markup. Comparing 13 models against
> upstream list prices on one internal hub showed a consistent **1.10x**, which
> made generating the whole table trivial rather than hand-typing 54 rows.

---

## Configuration

| Env var | Purpose |
|---|---|
| `OPENCODE_ODOMETER_DIR` | where state/budget/config live |
| `OPENCODE_ODOMETER_HOME` | where the app is installed (for the plugin) |
| `OPENCODE_DB` | path to `opencode.db` if not in the default place |
| `OPENCODE_ODOMETER_MAX_MESSAGES` | ledger cap, default `20000` |

**Data directory** (`%LOCALAPPDATA%\OpenCodeOdometer\` on Windows):

| File | |
|---|---|
| `odometer_state.json` | every priced message, totals, window position |
| `budget.json` | the contract between app and plugin |
| `grace_claims.json` | transient; plugin records a used grace turn here |
| `prices.local.json` | your private overlay, if any |

`budget.json` has a single writer (the odometer) so the plugin can never race
it. Grace turns are the one thing the plugin must record, so it appends to a
separate `grace_claims.json` which the odometer folds in on its next poll.

Nothing writable is stored next to the executable, so it works fine installed
under `Program Files`.

---

## How it works

```
opencode.db ──snapshot──> temp copy ──read──> poll_once() every 1s
                                                    │
prices.json + prices.local.json ──> PriceBook ──────┤
                                                    ▼
                                          messages{id: record}
                                                    │
                                              recompute()
                                                    │
                                    ┌───────────────┴──────────────┐
                                    ▼                              ▼
                              UI + odometer              budget.json ──> plugin
```

Three decisions worth knowing about:

**Follow `time_updated`, not `time_created`.** OpenCode inserts an assistant
row immediately with zero tokens, then writes real usage back 1.5–19s later.
An earlier version watermarked on creation time and read each row once — at
insert, when tokens were still zero. It counted 5,048 of 5,336 messages as free.

**Messages are keyed by id and totals recomputed.** Because rows are re-read as
they update, an accumulator would double-count. Instead records are replaced by
id and everything is re-derived, making re-reads idempotent.

**Pricing lives in the app, not the plugin.** The plugin reads a precomputed
verdict (`"state": "over"`) and never prices anything. One source of truth, and
the plugin stays ~140 lines.

---

## Tests

```powershell
python -m pytest tests\ -v
```

Covers pricing maths (including cache), free detection, overlay merging,
reference-model fallback, and the budget state machine.

---

## Limitations

- **Windows only** — the plugin early-returns elsewhere.
- **Model switching is advice only** — no hook can reassign a model.
- **Blocking surfaces as an error** in the CLI, softened by a toast first.
- **Savings are indicative**, benchmarked against a reference model rather than
  a real invoice.
- **Reseller markups are inferred** if you generate an overlay from a ratio;
  spot-check against a real invoice.

---

## Contributing

macOS/Linux support is the most useful contribution. The blockers are small and
localised:

- `pid_alive()` uses `ctypes.windll.kernel32` → needs an `os.kill(pid, 0)` branch
- `TASKBAR = 56` is a Windows dock offset
- `overrideredirect()` behaves differently on macOS
- the plugin's `process.platform !== "win32"` guard

Issues and PRs welcome.

## License

MIT — see [LICENSE](LICENSE).
