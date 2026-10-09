# Open TUI budgets: implementation and review

Open TUI budgets and the compact aggregate dock have been reviewed. The local review build is `build/bin/OpenCode_Odometer-preview-flows.exe`.

## Behaviour

- Each newly opened OpenCode TUI registers immediately, including the home screen before the first chat. It gets a stable name such as `keen-robin-c22b85c4`, shown on a separate row in the TUI, its terminal title and the Odometer list. The former folder prefix is unnecessary for uniqueness and is omitted from new names. Naming makes no model request.
- The list comes from live TUI reports, not the saved conversation inventory. Idle TUIs stay listed. Normal exit reports closure; a killed process or a heartbeat older than ten seconds expires. Closure removes the entry, its cap and grace bookkeeping, while retaining accounting history.
- Each entry starts without a cap. An optional cap set on the home screen binds to the first chat. Changing to another conversation resets the cap; starting another TUI gets another name and an unarmed entry.
- Enabling a cap counts subsequent spend. Editing an armed cap preserves the counted spend. Widget restart preserves caps for TUIs that remain open. TRIP reset does not reset those caps.
- Hard mode blocks the next paid request at 100%. Soft mode permits one grace turn, with a hard stop at 150%. Free models remain available. Usage arrives as OpenCode reports it, so an already-running response can exceed the cap; this is not a provider-side dollar ceiling.
- Delegated child conversations share the root cap and grace allowance. STOP asks the selected TUI to abort that root and its known children, and waits for an acknowledgement. It does not close the terminal.
- The dock stays at 340 × 46, retaining its original height and digit sizes. Healthy states show `2 sessions · all within budget` or `2 sessions · no limits`. At 75%, it shows the worst capped entry's complete name and percentage, such as `⚠ clear-fox-930e1799 82%`. At 100%, it shows that name and spent/limit amounts with a red border. Names retain the identifier suffix and wrap inside the existing width. The tooltip holds the count, budget details and full names of all open entries. There is no dock STOP button or OVER label; STOP and limit editing live in the expanded panel.
- The dock caption is independent of the panel's selected row. For example, when clear-fox does work while clear-owl is selected, an unlimited dock shows `2 sessions · no limits` instead of appearing to belong to clear-owl. Alerts identify the worst cap, and expanding an over-budget dock selects that entry. Panel selection continues to target only the footer.
- The dock totals messages for currently displayed conversations since their TUI opened, counting a shared conversation only once. Its burn rate and activity also use only those open entries; closed conversations leave the total and rate. The global panel remains independent of the dock and its selected row. Enforcement pause remains visible even when a session is over budget.
- The selected-session heading and ENABLE checkbox are separate. Checking ENABLE before entering an amount keeps the checkbox selected and asks for a positive amount. Backend refreshes preserve edited values and focus; queued saves and rapid row selection cannot apply stale checkbox states to another row.
- The expanded panel keeps the global TRIP/TOTAL odometer, statistics and model usage table. Session selection changes only the footer target. The sessions list and the global model table scroll independently, vertically.
- Single-entry and empty-list states work without inherited defaults. With no open TUI, limit and STOP controls are disabled.

## Review steps

1. Inspect `build/multi-session-ui/expanded.png` and the `docked-*.png` screenshots. These show the actual frontend with synthetic session data.
2. Close the currently running Odometer, then launch `build/bin/OpenCode_Odometer-preview-flows.exe` from File Explorer. Codex's binary-file preview does not launch an executable. Alternatively, use PowerShell: `& '.\build\bin\OpenCode_Odometer-preview-flows.exe'`. This uses the normal single-instance lock. Its first launch installs the companion and registers it in OpenCode's TUI configuration.
3. Restart OpenCode, opening two or more TUIs. Each should appear before a prompt is sent, with its name on both sides. A terminal host that ignores application titles may keep its own tab label; the name is still visible inside OpenCode.
4. Set a cap on one row, select another and confirm it has no cap. Try checking ENABLE with an empty amount, entering a cap, hard/soft modes, STOP, independent scrolling and closing one TUI. Review the one-TUI layout as well.
5. Validate the appearance and behaviour before shipping a new build.

Live OpenCode configuration and the currently running Odometer were not replaced during automated testing. The real TUI test used isolated configuration, session storage and a local fake provider; it made no paid provider calls.

## Validation

- Full Go suite: budgeting, lifecycle cleanup, restart, historical backfill exclusion, validation, protected pruning, plugin installation and preserved JSON/JSONC registration. Dock accounting checks cover shared conversations, child messages, closed conversations and open-only burn rate.
- Plugin suite: existing enforcement, switching/startup behaviour and the new presence transport.
- New browser smoke: all four requested dock flows at 340 × 46, visible identifiers, complete captions fitting within the dock, the clear-fox/clear-owl reproduction, stable focus/row identity, rapid selection, empty-amount checkbox handling, delayed and overlapping saves, session-specific footer calls, worst-entry selection on expansion, green/amber/red/no-limit and paused docks, identical readable names, both scroll regions, singleton and empty states. Dock spend, rate and activity stay independent of the global panel's values. Long budget amounts cannot overlap the expand button.
- Existing browser smoke: model picker, pricing review, STOP success/failure and outside-click collapse at the new window dimensions.
- Actual OpenCode 1.14.39 test: two home-screen TUIs, unique title names, chat binding, paid request rejection before the fake provider, independent enforcement, increasing a limit, and STOP cancelling only one of two concurrent streams. Normal TUI exit reports closure.
- The real renderer layout was inspected at 80 × 24: the home name occupies row 16, below shortcuts on row 14; the chat name occupies row 16, above model metadata on row 20 and shortcuts on row 22. The label fits within the viewport in both layouts. The harness saves `home-layout.json` and `session-layout.json` in its isolated run directory.
- Static checks and the Wails preview build are recorded in `build/multi-session-*.log`.

Two TUIs displaying the same underlying OpenCode conversation share that conversation's execution and messages. Its stricter active cap is published, and cancellation affects that shared conversation. Fully independent enforcement requires separate conversations, as in the two-TUI test.

Before a TUI has used a model, OpenCode's current TUI API does not expose the selected model to this companion. Its row shows `idle · no model used yet` until model metadata is available.

## Files changed

The latest dock refinement changes `frontend/dist/sessions.js` (four-state captions and selection independence), `frontend/dist/style.css` (wrapping and spacing within the same dimensions), `tools/multi-session-ui-smoke.cjs` (flow, identity, reproduction and geometry checks), and this document plus `README.md`. It makes no changes to budget enforcement or the OpenCode companion. The browser suites and preview build were rerun for this refinement; the Go and real TUI results below cover the earlier backend/companion changes.

| File | Purpose |
|---|---|
| `plugin/odometer-tui.tsx` | Home/chat name rows with dedicated vertical space, terminal title, heartbeat, root ancestry and targeted STOP acknowledgement. The chat wrapper retains the native Prompt and its controls. |
| `plugin/tui-presence.js` | Unique readable names and atomic presence/closure reports. |
| `internal/app/odometer_tui.tsx`, `internal/app/tui_presence.js` | Embedded copies shipped in the executable. |
| `internal/app/open_sessions.go` | Live inventory, lifecycle expiry, per-entry caps, spend accounting, sorting and root/child verdicts. Deduplicated dock totals and open-only burn rate. |
| `internal/app/app.go` | Persist live caps, activate open-session mode and publish its enforcement contract. Protect active spend during saves. |
| `internal/app/abort.go` | Route STOP to the selected TUI and verify its acknowledgement. |
| `internal/app/plugininstall.go`, `internal/app/tuiconfig.go` | Install and explicitly register the TUI companion while preserving existing settings and JSONC comments. |
| `internal/opencode/process.go` | Reuse portable process-liveness checks for TUI expiry. |
| `internal/ledger/store.go` | Keep records required by open budgets during history pruning. |
| `internal/wservice/service.go` | Expose open rows, stable selection, aggregate spend and session-specific controls; target model comparisons to the selected entry. Set new window dimensions. |
| `main.go` | Start in open-session mode with a 340 × 46 compact window. |
| `bindings_mode.go`, `runtime_mode.go` | Generate bindings without the live single-instance lock or installation/accounting side effects. |
| `frontend/dist/sessions.js` | Stable session rows, selection/footer targeting, draft/pending checkbox handling, aggregate dock and worst-entry edge. |
| `frontend/dist/main.js` | Connect the new controls and expansion; keep breaches inline. |
| `frontend/dist/index.html`, `frontend/dist/style.css` | Panel/dock structure, independent vertical scrolling, header toggles and footer/button layout. |
| `frontend/wailsjs/go/models.ts`, `frontend/wailsjs/go/wservice/Service.js`, `frontend/wailsjs/go/wservice/Service.d.ts` | Generated bindings for the new backend payload and methods. |
| `internal/app/open_sessions_test.go` | Independent caps, lifecycle cleanup, historical data, restart, pruning and input validation. |
| `internal/wservice/open_sessions_test.go` | Snapshot aggregation, independent footer targeting and selection fallback when a TUI closes. |
| `internal/app/tuiconfig_test.go` | Safe and idempotent registration for JSON/JSONC, plus malformed-config protection. |
| `internal/app/plugininstall_test.go`, `internal/app/embedsync_test.go` | Companion installation and source/embed consistency. |
| `plugin/presence.test.mjs` | Names, startup reports, heartbeat stability and independent closure. |
| `tools/multi-session-ui-smoke.cjs` | Browser interaction checks and review screenshots. |
| `tools/multi-session-tui-smoke.cjs` | Repeatable isolated tests against actual OpenCode TUIs and a fake provider. |
| `tools/experience-ui-smoke.cjs` | Existing regression checks updated to the new window sizes. |
| `build_exe.bat` | Synchronize the new embedded companion sources during ordinary builds. |
| `.gitignore` | Exclude runtime TUI reports and STOP request files. |
| `README.md`, `docs/MULTI_SESSION.md` | Feature description, review procedure, validation and file summary. |
