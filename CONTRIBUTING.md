# Contributing

Thanks for taking a look. This is a small, dependency-free project and intends
to stay that way.

## Getting set up

```powershell
git clone https://github.com/YOURNAME/opencode-odometer.git
cd opencode-odometer
python opencode_monitor.py
```

No dependencies beyond the Python standard library (`tkinter` + `sqlite3`).
PyInstaller is needed only to build the `.exe`.

## Running tests

```powershell
python -m pytest tests\ -v
# or, without pytest:
python tests\test_pricing.py
```

Tests are pure logic — no GUI, no database, no network. Please keep them that
way so they stay fast and runnable anywhere.

## Before opening a PR

- [ ] `python tests\test_pricing.py` passes
- [ ] `node --check plugin\odometer.js` passes
- [ ] No private data in the diff — see below

### Never commit

`.gitignore` covers these, but check anyway:

- `odometer_state.json` — every priced message, real session ids, real spend
- `budget.json` — live per-session costs
- `prices.local.json` — private/commercial rates
- `*.csv` — exported ledgers

A quick scan before pushing:

```powershell
git diff --cached -U0 | Select-String -Pattern "ses_[a-zA-Z0-9]{20}"
```

## Most useful contribution: macOS / Linux support

Currently Windows-only. The blockers are small and localised:

| Location | Issue |
|---|---|
| `pid_alive()` | uses `ctypes.windll.kernel32`; needs an `os.kill(pid, 0)` branch |
| `TASKBAR = 56` | Windows dock offset |
| `_set_frameless()` | `overrideredirect()` behaves differently on macOS |
| `plugin/odometer.js` | early-returns on `process.platform !== "win32"` |
| `launch()` | spawns `.exe` / `pythonw` |

The data layer already resolves XDG paths and locates `opencode.db` across
platforms, so most of the groundwork is done.

## Design principles

Worth understanding before changing enforcement or pricing:

**1. `prices.json` is authoritative.** The app never trusts a provider's cost
field and never calls a pricing API at runtime.

**2. Cache tokens are priced separately.** They were ~83% of tokens on the
traffic this was built against. Never fold them into `input`.

**3. Follow `time_updated`, not `time_created`.** OpenCode inserts assistant
rows with zero tokens and backfills usage 1.5–19s later. Watermarking on
creation time counted 5,048 of 5,336 messages as free.

**4. Messages are keyed by id; totals are recomputed.** Rows are re-read as
they update, so an accumulator would double-count.

**5. `budget.json` has exactly one writer** — the odometer. The plugin only
reads it. Grace claims go in a separate file to preserve this.

**6. `Budget.status()` is a measurement, not a decision.** It reports the true
fraction regardless of whether enforcement is on. Gate on `blocks()` or the
published `enforced` flag — never infer enforcement from state.

**7. A dead odometer must never block anyone.** If the ledger is older than
120s the plugin stops enforcing. Fail open, always.

**8. Blocking must happen before the provider is called.** `chat.message`
throws for this reason. `session.abort()` was tried and rejected — it cancels
a turn already in flight, ~12s after billing has started.

## Reporting bugs

Please include:

- OS and Python version
- Whether running from source or the `.exe`
- Relevant output from the console (the app prints `[prices]`, `[budget]`,
  `[state]` diagnostics)
- **Redact session ids and costs** if you paste ledger contents
