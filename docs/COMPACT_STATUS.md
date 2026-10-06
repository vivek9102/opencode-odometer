# OpenCode Odometer implementation status

Updated 2026-10-07. Windows is the tested desktop platform.

## Accounting and budgets

- The plugin spools assistant usage and session ancestry; the app prices messages
  by ID and publishes budget verdicts. HTTP/SSE remains a fallback.
- A parent chat and its delegated children share a cap and grace allowance.
- Armed budgets, spending baselines and grace usage survive app restarts.
  Changing an enabled cap and resetting TRIP do not reset budget spending.
- Idle polling refreshes the budget heartbeat without moving its baseline.
- State writes are serialized and retry brief Windows file-sharing failures.

## Startup and display

- Plugin initialization returns before provider/session discovery, avoiding
  a dependency cycle during OpenCode startup.
- Auto-start uses the compact bottom-centre bar. Preferences control startup,
  idle dimming, optional outside-click collapse and enforcement pause.
- Hover does not resize the window. Click opens spending details, the sparkline
  and the spent/remaining toggle. Completed live charges show a brief cost tick.
- Windows tray supports show, hide, pause/resume and quit with the app icon.

## Model selection

Both model screens save a persistent choice for the target chat. Routing is
confirmed by the matching assistant response. Other chats retain their models.
To hand control back to OpenCode, clear the choice with **Use OpenCode selection**
before using `/model`. Automatic native-selection handoff is not implemented.

## Diagnostics and validation

Optional event diagnostics are disabled by default and write bounded, asynchronous
metadata summaries when enabled. Logs and runtime state must remain private.

Regression coverage includes [budget persistence](../internal/app/budget_reset_test.go),
[Windows state writes](../internal/app/state_windows_test.go),
[plugin initialization](../plugin/initialization.test.mjs),
[startup preferences](../plugin/startup.test.mjs),
[model routing](../plugin/odometer.test.mjs), and
[the core workflow](../internal/app/e2e_workflow_test.go).
Optional browser and real-OpenCode smoke scripts live in `tools/`; they use
mock services or a local fake provider and isolated test directories.

See [README](../README.md) for user flows and [CONTRIBUTING](../CONTRIBUTING.md)
for validation commands.
