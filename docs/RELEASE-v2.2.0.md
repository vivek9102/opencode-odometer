# v2.2.0: automatic fallback and usage history

This release adds automatic budget fallback, per-TUI session controls and
date-based usage reports.

## Changes

- Configure a hard allowance for each open TUI. At exhaustion, stop paid work
  or automatically continue the same conversation on a chosen provider/model.
- Give paid fallbacks a separate stopping cap or allow them to keep going.
  Free and uncapped fallbacks display **no cap**, with spending still accounted.
- Release budget-owned routing when its limit is cleared. Active fallback
  routing explains when it overrides OpenCode's `/model` selection. Manual
  Odometer choices retain the **Use OpenCode selection** handoff.
- Use a continuation prompt that asks the fallback to preserve the task's scope,
  depth, completion criteria and completed work.
- Rename display counters to **Open sessions / Since reset / All time** and
  align exported CSV rows and totals with the selected scope.
- Add **USAGE** with daily, weekly, monthly and custom date ranges,
  model/provider breakdowns and period CSV exports. Daily archives preserve
  accounting after message pruning; unavailable older dated detail is identified.
- Clean up old, unreferenced terminal protocol files and display storage size.
- Reject stale TUI presence files when Windows reuses their process IDs,
  preserving valid quiet TUIs and their limits.
- Update documentation and screenshots. The compact dock measures 340 × 46.

An already dispatched request can overshoot its allowance. Provider failures
and uncertain interrupted actions can pause continuation with a reason;
existing tool permissions still apply. Benchmark metadata is optional and a
key is not needed for pricing, budgets or switching. OpenCode handles Plan/Build
routing.

## Install or upgrade

1. Download **OpenCode_Odometer-v2.2.0.exe** from the
   [release page](https://github.com/vivek9102/opencode-odometer/releases/tag/v2.2.0).
2. Quit the previous Odometer instance and run the new executable once. It
   installs the bundled plugins and records its location. Saved preferences,
   pricing and usage history are retained.
3. If Windows blocks the unsigned file, select **Properties → Unblock → Apply**
   from its right-click menu, then run it again. If offered, **More info → Run
   anyway** is another option. On managed computers, ask an administrator to
   approve the executable if these options are unavailable. Do not disable
   Windows security.
4. Restart OpenCode to load the updated server and TUI plugins. Newly opened
   TUIs start without limits; configure their budgets as needed.

If you move the executable, run it once from its new location. **Settings → Start
with OpenCode** controls automatic launch. **Hide** keeps the application running
in the tray; restore it from the icon near the Windows clock.

The release targets Windows x64 and requires Microsoft WebView2 Runtime. The
executable is unsigned; its accompanying `.sha256` asset supplies the checksum.

## Documentation and validation

See the [screenshot gallery](../README.md#screenshots),
[budget guide](BUDGET_RULES.md) and [pricing guide](PRICING.md).

Go tests and vet, plugin regressions, browser checks and isolated native WebView2
checks passed. Integration checks used two isolated OpenCode 1.14.39 TUIs and a
localhost fake provider to test continuation, routing release, Esc and targeted
Stop Session. These checks required no live paid provider requests or changes
to live budgets; they do not establish compatibility with every provider.
