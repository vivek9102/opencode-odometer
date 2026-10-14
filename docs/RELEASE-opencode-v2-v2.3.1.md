# OpenCode V2 edition — Odometer v2.3.1 preview

**For OpenCode V2 only.** Tested with **OpenCode 2.0.28 on Windows x64**.
This is a separate executable and plugin package. OpenCode V1 users should keep
using the [V1 edition, Odometer v2.2.0](https://github.com/vivek9102/opencode-odometer/releases/tag/v2.2.0).
The Odometer application version is separate from the OpenCode CLI version.

V1 source remains on `main`; V2 source is on `codex/opencode-v2`. This preview
does not replace the V1 stable/latest release. Later OpenCode V2 patches require
verification; compatibility with every provider is not established.

## Changes

- Port the existing UI, accounting, pricing and reporting to the V2 plugin API
  and session controls, with a separate V2 build and installer version guard.
- Give each conversation a stable Odometer name and its own budget. Include
  inactive internal tabs and show a shared conversation once across terminals.
- Keep names, limits, fallback state and counted spending when changing tabs
  or reopening a conversation. Preserve spending after message pruning with
  numeric original/fallback aggregates. New conversations start without limits.
- Display the native conversation title beside its model. The terminal title
  and composer label identify the active conversation's Odometer name.
- Target STOP SESSION and automatic fallback at the selected conversation,
  including inactive tabs. Interrupt, select the configured provider/model and
  automatically continue in the same conversation without a confirmation click.
- Cancel queued Odometer continuation inputs on STOP while preserving queued
  user inputs. Clearing a limit releases budget-owned model routing.
- Prepare the dock/expanded layout before resizing, resize before the ledger
  save, and guard against stale UI refreshes. Dimensions remain unchanged:
  **340 × 46** for the compact dock.

## Install or upgrade

1. Download **OpenCode_Odometer_OpenCodeV2_2.3.1_preview.exe** and its `.sha256`
   asset from this release.
2. Quit the previous Odometer instance, then run this executable once. It
   installs the V2 package, preserves accounting history/private prices, and
   backs up the legacy Odometer server plugin before removing it from discovery.
3. Restart OpenCode so its server and terminal clients load the new package.
4. Configure budgets per conversation. An existing first-preview terminal
   budget migrates to its bound conversation.

Do not run the V1 and V2 Odometer executables against the same data directory
simultaneously. If you move the executable, run it once from its new location.
Settings control automatic startup; Hide keeps Odometer running in the tray.

### If Windows blocks the executable

The executable is unsigned. After checking its source and checksum, right-click
it and choose **Properties → Unblock → Apply**, then run it again. If SmartScreen
offers it, use **More info → Run anyway**. On a managed computer, ask an
administrator to approve it if those options are unavailable. Do not disable
Windows security. Microsoft WebView2 Runtime is required.

## Validation and limits

Both V1 and V2 Go suites, V2 vet and all plugin tests passed. Browser checks
covered delayed dock/expand acknowledgements, original-size layouts, budget
controls and existing reporting interactions. An isolated OpenCode 2.0.28 server
with a localhost fake provider validated shared multi-tab conversations,
original/fallback hard caps, actual switching, automatic continuation and native
STOP SESSION. These checks made no paid provider requests. The preview was also
confirmed working by its initial user; native paint behaviour can vary by system.

An already dispatched request can overshoot. Provider errors and uncertain
interrupted actions pause continuation with a reason; existing tool permissions
still apply. Keep Odometer running for enforcement. The adapter leaves native Esc
handling intact; piped-input checks cannot reliably validate physical key presses.

See the [V2 installation and architecture guide](https://github.com/vivek9102/opencode-odometer/blob/codex/opencode-v2/docs/OPENCODE_V2.md).
