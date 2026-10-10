Automatic budget fallback now interrupts the original turn, selects the configured provider/model and continues the same conversation without a confirmation click. Each open TUI has its own allowance; new TUIs start without a limit.

### Changes

- Compact per-session hard limits, immediate Stop Session, and a cheaper-model picker with separate fallback spending. Free and Keep going fallbacks clearly show no stopping cap.
- Budget routing is released when its limit is cleared. Active fallback routing explains when OpenCode's `/model` selection is overridden. Deliberate manual Odometer choices remain until replaced or cleared with **Use OpenCode selection**.
- Continuation preserves the requested scope, depth and completion criteria instead of encouraging an early summary.
- **Open sessions / Since reset / All time** replace RUN/TRIP/TOTAL. CSV rows and totals use the selected counter's scope.
- **USAGE** shows Today, this week, month or custom dates, daily consumption, model/provider totals and downloadable period CSVs. Daily archives preserve reporting after message pruning; unavailable older dated detail is identified.
- Conservative cleanup of old, unreferenced protocol acknowledgements, with storage sizes visible in Usage. Existing telemetry/log limits remain unchanged.
- Fixed extra session rows caused by Windows reusing old process IDs. Valid quiet TUIs and their limits remain intact.
- Updated documentation and screenshots. The dock remains 340 × 46; no width increase.

An already dispatched request can overshoot its allowance. Provider failures and uncertain interrupted actions pause continuation with a clear message; existing tool permissions still apply. Artificial Analysis benchmarks are optional and do not require a key for budgets, prices or switching. OpenCode continues to handle Plan/Build routing.

### Install or upgrade

1. Download **OpenCode_Odometer-v2.2.0.exe** below. Quit the previous Odometer instance before running it.
2. Run the executable once to install its bundled plugin and remember its location. Saved preferences, pricing and usage history are retained.
3. If Windows blocks the unsigned file, right-click it → **Properties → Unblock → Apply**, then run it again. If offered, **More info → Run anyway** is another option. Do not disable Windows security. If your managed computer does not offer these options, ask your administrator to approve the executable.
4. Restart **OpenCode** once to load the updated server and TUI plugins. This opens new TUI entries with no default limits; configure their limits again as needed.

If you move the executable, run it once from the new location. **Settings → Start with OpenCode** controls automatic launch. **Hide** keeps it running in the tray; restore it from the icon near the Windows clock, including the hidden-icons area.

**Platform:** Windows x64. Requires Microsoft WebView2 Runtime, normally included with Windows 11 and current Windows 10. The executable is unsigned. The `.sha256` asset contains its SHA-256 checksum.

### Screenshots and validation

See the [README gallery](https://github.com/vivek9102/opencode-odometer#screenshots) and [budget and usage documentation](https://github.com/vivek9102/opencode-odometer/blob/main/docs/BUDGET_RULES.md).

Go tests and vet, plugin regressions, browser checks and isolated native WebView2 checks passed. Two isolated OpenCode 1.14.39 TUIs were checked with a localhost fake provider for fallback continuation, model release, Esc and targeted Stop Session. No live paid provider requests or live budget mutations were used for validation.
