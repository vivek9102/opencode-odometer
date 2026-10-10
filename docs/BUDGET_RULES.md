# Automatic session budget rules

This implements the supplied multi-session spec with the agreed changes: switch
at exhaustion (100%, not 95%), stop and continue through OpenCode's API, and show
configured routes with their input/output prices. It is ready for local review;
these changes have not been committed or pushed.

## Counters and budgets

The compact dock remains **340 × 46** and always shows **RUN**: usage for currently
open conversations since their TUIs opened. Duplicate TUIs do not double-count a
conversation. The expanded panel labels **RUN / TRIP / TOTAL** explicitly. Reset
Trip appears only on TRIP. Counter changes and widget restarts preserve budgets.

An open TUI starts without a limit. Aggregate spending is counted from TUI startup;
the selected allowance starts at zero when ENABLE is turned on. Changing the
amount preserves that allowance's counted spending. Disable and re-enable opens
a fresh allowance without erasing historical charges. Every open-TUI allowance is hard,
without grace turns. Closing the TUI removes its rule and limit, retaining ledger
history. Delegated child conversations share their parent's rule.

**Stop at limit** saves immediately. **Switch to a cheaper model…** opens a draft
popup: select a fallback and press **Switch at limit** to apply it. Cancel, X,
Escape and backdrop clicks discard the draft. A paid fallback has its own **Extra
budget** and **After that: Stop / Keep going**
choice. Keep going explicitly allows subsequent fallback requests after that
allowance is spent. A confirmed free route has no paid cap. Unknown SDK prices do
not establish that a route is free.

The five dock states are healthy, unlimited, near (75%), fallback and stopped.
The worst session drives the caption, independently of the footer selection.
Because the dock retains its approved width, the stopped label and spent/limit
share one line; its readable name and identifier occupy the other. Long fallback
captions clamp to two lines, with full names/model details in the tooltip. There
is no dock STOP button. Clicking expands with the worst entry selected.

The expanded panel retains **800 × 780**, bounded by the desktop work area.
The session list and global model table scroll independently. The model picker
is a separate bounded popup; the header and utility footer remain visible.

## Automatic continuation

1. The original allowance reaches 100%; new model/tool steps are blocked.
2. The owning OpenCode plugin lets running tools settle, aborts the original
   conversation and known children, and confirms idle status.
3. It inspects completed/interrupted tool actions and reports the original
   assistant message IDs. Odometer then activates the separate fallback stage.
4. It checks context/attachment compatibility and estimates the next request's
   uncached input cost, allowing space for tool schemas and some output.
5. It asynchronously submits **one** continuation message to the **same conversation**, explicitly
   selecting the configured fallback. The prompt asks it to preserve completed
   work and verify uncertain actions before repeating them.
6. It correlates the first successful model step to that continuation and verifies
   its provider/model before reporting the switch, without waiting for the entire
   task to finish. A task
   already completed needs no extra response; the fallback is selected for the
   next message instead.

Generation IDs, claimed commands and persistent acknowledgements prevent duplicate
continuations and switch loops. Manual STOP clears the generation and blocks
continuation. Unavailable models, known insufficient context, unsupported
attachments, unaffordable estimates, unsettled/interrupted tools and timeouts
leave the session paused. Commands with uncertain delivery are not retried.
Missing context metadata does not pause a configured fallback: OpenCode and the
provider validate the request when it is submitted. Automatic fallback is refused
when two open TUIs share one conversation. A tool
rejected by the budget before-hook is known not to have executed and can be retried;
an interrupted tool without that proof remains uncertain and pauses continuation.

Raising or clearing the original limit restores its model and opens a fresh
fallback allowance when original spending is below the new cap, preserving
historical charges. Lowering a limit or confirming a fallback re-evaluates
immediately. There are no Resume or Undo controls. **STOP SESSION** requests
acknowledged cancellation like Esc, keeps the allowance and model intact, and
shows the short “OpenCode stopped the chat” message. A later explicit user
message releases the cancellation marker; it does not bypass an exhausted cap.
Stop/switch toasts remain for about 12 seconds and dismiss back to the small dock.

This is a post-usage policy, not a provider dollar ceiling. An already dispatched
request can overshoot, and the affordability check is an estimate rather than a
tokenizer/provider quote. Existing tool permissions still apply. Legacy bridge
heartbeat expiry remains unchanged: after a stale/missing verdict, enforcement
is unavailable. Keep Odometer running when relying on these limits.

## Model facts and privacy

Capabilities/context come from [models.dev](https://models.dev); actual OpenCode
inventory facts override generic capabilities. Coding/intelligence scores and
measured speed come from [Artificial Analysis](https://artificialanalysis.ai/).
Benchmarks join only exact model/version identifiers or explicit canonical
mappings. Display names do not override a configuration mismatch. The fallback
picker shows the same description and input/output prices as the model popup;
it does not display ratings, benchmark scores or a benchmark-based ordering.

The picker requires availability in the owning instance,
known pricing and a cheaper route at an equal input/output token mix, matching
the model popup. Each paid card shows both rates; the actual cost depends on the
token mix. Search, cost and provider filters
narrow the configured routes; specialised models and explicitly unsupported tool
models are disabled. Missing tool metadata does not disable an
available chat route. Automatic continuation still checks known context and attachment
compatibility before submitting anything. Route pricing comes from the existing local
pricing/OpenCode configuration, not the benchmark service.

Startup reads cached metadata, then refreshes in the background. An outage retains
cached facts and does not block startup. **Settings** accepts an optional Artificial
Analysis key and offers **Refresh metadata**. Alternatively set
`ARTIFICIAL_ANALYSIS_API_KEY` (takes precedence over the saved key).

The saved key is private local JSON (`metadata_settings.json`), not encrypted
credential storage. It is never returned to the frontend, sent to models.dev,
included in the public metadata cache, or exported to CSV. Runtime keys, caches
and continuation files are ignored by Git.

## Validation and review

Automated checks cover original/fallback attribution, late usage, widget restart,
Trip reset, free/paid Keep going, manual STOP, failed commands, shared-chat refusal,
insufficient increases, closure cleanup, exact metadata matching/cache retention,
and key separation. Browser checks cover counters, filters, rule saving, independent
scrolling, all dock states, dimensions, draft cancellation and toast stop actions.

The real OpenCode 1.14.39 integration tests use isolated storage/configuration and a
local fake provider. It completed a read tool, interrupted the original model,
continued once on the fallback in the same conversation, preserved the tool result,
suppressed duplicate delivery and left another chat on its own model. A second
case exhausted the verdict before a tool began and verified the fallback executed
it once. Normal follow-up message ordering was also checked. This does
not establish behaviour for every provider or destructive tool.

Review the demo [compact limit section](screenshots/expanded-rules.png),
[fallback popup](screenshots/fallback-picker.png),
[fallback dock](screenshots/docked-fallback.png) and
[stopped toast](screenshots/docked-stopped-toast.png). Launch the separate
`build/bin/OpenCode_Odometer-budget-ui-preview.exe` after closing the existing
widget; restart OpenCode to load its upgraded plugin. The build itself does not
replace the running app or install anything.

Repeat the isolated integration test with:

```powershell
$env:OPENCODE_SMOKE_EXE = 'path\to\opencode.exe'
$env:OPENCODE_SMOKE_DIR = Join-Path (Get-Location) 'build/automatic-fallback-smoke'
$env:OPENCODE_SMOKE_AUTO_FALLBACK = '1'
node tools/model-switch-smoke.cjs
# Optional second case: publish exhaustion before a tool is executed.
$env:OPENCODE_SMOKE_AT_TOOL_LIMIT = '1'
$env:OPENCODE_SMOKE_DIR = Join-Path (Get-Location) 'build/automatic-fallback-tool-limit'
node tools/model-switch-smoke.cjs
```

Artificial Analysis authentication/live scores still need validation with a user
key. Browser images use synthetic data; native WebView2 appearance remains for
your review. No paid inference or live-session cancellation was used in testing.

## Change map

### Follow-up fixes for local review

| Files | Fix |
|---|---|
| `internal/app/open_sessions.go` | ENABLE starts the allowance at zero; amount edits keep its baseline. Temporary blank routes, resolved parent IDs and delayed heartbeats do not delete a live TUI's limit. Confirmed different conversations and closed/dead TUIs still clear it. |
| `internal/app/model_metadata.go` | Missing tool metadata no longer disables an available chat route. Explicit lack of tools, specialised routes and unknown pricing still prevent selection. Ratings remain exact-match facts. |
| `frontend/dist/budget-rules.js` | Show only the configured-model count, without promising a quality order. |
| `frontend/dist/experience.js`, `internal/wservice/service.go`, `internal/wservice/experience.go` | Reconcile the native dimensions on focus/compact resize and tray restore; unchanged dimensions are left alone. |
| `internal/app/open_sessions_test.go`, `budget_rules_test.go`, `tools/multi-session-ui-smoke.cjs` | Regressions for pre-enable spending, fresh periods, restart, temporary routing, parent resolution, delayed heartbeats, model eligibility, docking/typing and restore events. |

The current review build is `build/bin/OpenCode_Odometer-budget-fixes-v6-preview.exe`.
These fixes do not reset the live saved budgets or replace the running executable.
Review native taskbar minimise/restore and a live follow-up message before committing.

The first follow-up preview exposed two event-ordering bugs, reproduced and fixed
in v2. Native resize could arrive before the new layout snapshot; recovery now
uses `ReconcileWindowLayout`, which repairs the rectangle without setting compact
state. Entering an amount before clicking ENABLE used to enable on blur, then
immediately disable on checkbox click; unchecked amounts now remain drafts until
ENABLE is clicked. The browser suite covers both native event ordering and both
amount/checkbox entry orders. Service bindings expose the new reconciliation API.

The isolated native WebView2 check passed amount-first ENABLE, three Expand/DOCK
cycles, automatic collapse while minimised, restoration to 340 × 46, and a final
expanded view with the $0.20 allowance still enabled in saved state. It uses the
same app/UI source with test-only debugging instrumentation; actual live inference
was not needed. Go service tests/vet and both browser suites also passed.

`tools/native-window-smoke.cjs` also tests a native WebView2 app with synthetic TUI
presence and isolated data, plugins, temp lock and WebView profile. Its debugging
connection requires a private test runtime that passes
`OPENCODE_NATIVE_SMOKE_BROWSER_ARGS` and `OPENCODE_NATIVE_SMOKE_WEBVIEW_DIR` to
Wails' Chromium options; the review build retains the normal runtime. Run it with `OPENCODE_NATIVE_SMOKE_EXE` pointing
to that test executable and Playwright available on `NODE_PATH`.

The remaining reset on typing came from the TUI prompt wrapper, rather than
docking or saving the checkbox. [OpenTUI slot renderers](https://opentui.com/docs/plugins/solid/)
receive `(context, props)`; our wrapper read the first argument as props. This
passed an undefined session ID to the native prompt, so submitting created a
different conversation (discarding the previous conversation's allowance), and
Esc had no conversation to interrupt. Both companion copies now use the second
argument. Budget, STOP SESSION and layout behavior are unchanged by this fix.

`tools/multi-session-tui-smoke.cjs` now submits through the native prompt as well
as the SDK, checks that follow-ups retain their conversation, sends the two Esc
key events expected by OpenCode, and retains its independent-budget and targeted
STOP checks. Earlier SDK-only submissions bypassed the defective wrapper.
The pre-fix source reproduced the conversation change against real OpenCode
1.14.39 with a localhost fake model. The companion is loaded at TUI startup:
after launching the v3 widget, restart OpenCode to load it, then reopen the saved
conversation. An already erased allowance must be enabled again explicitly.

Post-fix validation passed on two isolated OpenCode 1.14.39 TUIs: native prompt
follow-ups retained the chat ID; the first Esc kept the turn busy and the second
produced `MessageAbortedError`; STOP cancelled only its selected TUI while the
other stream continued; per-session paid blocking and normal-close reporting
still passed. No paid provider was used. The focused Go packages, seven plugin
regressions, multi-session browser suite, syntax checks and v3 Wails build also
passed. Runtime artifacts remain under ignored `build/` paths.

### Automatic fallback and model cards (v4)

The fallback popup now uses the existing model popup's card renderer: provider,
FREE/CHEAPER badge, description and input/output rates. It has no ratings or
benchmark link. Free + cheaper remains the default; All configured also shows
unavailable choices disabled with an explanation. Paid eligibility now uses the
same equal-input/output comparison as the model popup, including when the
original model is temporarily absent from inventory.

Private CompanyHub routes can omit context capacity. That missing value used to
prevent continuation before any request reached the provider. Unknown capacity
now permits an automatic attempt on the configured fallback; a known insufficient
capacity or actual provider failure still pauses with a reason. No confirmation
window is opened during switching. The model-selection popup is only needed when
configuring or changing the fallback.

Windows can briefly lock a bridge status file while the widget reads it. The
plugin now retries only atomic file publication for those sharing violations.
Model submissions and tool actions are never retried by this mechanism. It
rechecks cancellation immediately before submitting the continuation.

Isolated OpenCode 1.14.39 tests passed for both paid and free fallbacks with
missing context metadata. The paid case resumed a tool rejected before execution;
the free case preserved an already completed tool while cancelling the original
stream. Both used the same conversation, confirmed the selected model, sent one
continuation, suppressed duplicate commands and left another chat untouched.
The providers were localhost fixtures, so these tests spent no API credit and did
not change live sessions. Plugin tests also cover transient Windows file locks,
manual STOP, known insufficient context and fallback affordability. Browser
checks cover card layout, prices, filters, draft cancellation and existing model
popup behavior.

| Files | v4 change |
|---|---|
| `frontend/dist/budget-rules.js`, `index.html`, `style.css` | Reuse existing model cards, remove ratings, expose configured-model filter and retain compact layout. |
| `internal/app/model_metadata.go`, `budget_rules_test.go` | Align paid fallback eligibility with model popup pricing and regress missing-original inventory. |
| `plugin/odometer.js`, `internal/app/odometer_plugin.js`, `plugin/continuation.test.mjs` | Continue with unknown context metadata; retry transient bridge publication and preserve cancellation/duplicate protection. |
| `tools/model-switch-smoke.cjs`, `tools/multi-session-ui-smoke.cjs` | Paid/free automatic continuation and matching model-card interaction checks. |
| `docs/BUDGET_RULES.md` | Record behavior, causes, file map and validation limits. |

### TUI ownership discovery (v5)

The live `bright-panda-792c6485` inventory advertised continuation protocol 1 and
included `companyhub/gemini-2.5-flash`; the installed plugin matched v4. Its saved
failure was the combined discovery error before a command was issued. The
available snapshots do not establish which discovery condition failed at that
instant. Source inspection found a reproducible gap: ownership depended on a
100-entry recently observed chat list, while open-TUI presence already identifies
the actual process. Saved-history backfill and unrelated chat activity can omit
the open conversation or make another process appear to own it.

`internal/app/budget_rules.go` now addresses the owning TUI process directly and
allows up to 15 seconds for its discovery snapshot to become available, keeping
the original paid requests blocked throughout. The plugin reads its own open-TUI
presence and pins those conversations outside historical eviction. The exact v4
pre-dispatch discovery failure can recover automatically after update. Manual
STOP and uncertain/submitted continuation failures never receive this recovery.
Limits and counted spending are preserved.

Go regressions cover omitted chat history, another process advertising the same
chat, delayed discovery, bounded expiry and recovery of the saved v4 failure.
The plugin regression covers over 100 unrelated chats and an automatic
continuation addressed to the pinned open conversation. The isolated real
OpenCode smoke test uses `OPENCODE_SMOKE_HISTORY_CHURN=1` to create 105 unrelated
conversations before automatic fallback. It uses a localhost fake provider.

The real OpenCode test passed: the pinned conversation remained owned after the
105 new chats, the budget-blocked tool resumed on the selected fallback in the
same conversation, one continuation was submitted, duplicate commands were
suppressed and another chat retained its original model. Focused Go/service
tests, all seven plugin regressions, syntax checks and the v5 Wails build passed.

### Unclaimed command and repeated timeout (v6)

The next live report was traced to an unclaimed `continue-39736-...json` command:
there was no continuation acknowledgement and no fallback submission. The widget
was v5, but the owning OpenCode process and plugin instance were still the ones
started before the update. Its fresh protocol-1 inventory omitted the open chat.
The installed file was updated; the already loaded plugin was not. Restarting
only Odometer does not reload a plugin inside an existing OpenCode process.

The corrected plugin now advertises continuation protocol 2, and Odometer checks
for that version before queuing automatic continuation. This prevents the older
ownership implementation from appearing ready. Fully exit and restart OpenCode
after launching v6, reopen the saved conversation, then configure its new TUI's
budget and fallback. Subsequent limit transitions need no confirmation click.

The repeating toast was a separate condition bug: the timeout test still matched
`stage == switching` after `stopped == true`, generating a fresh event each poll.
The timeout now emits once. Tests verify repeated polls preserve its event and
generation, and that protocol 1 cannot receive a new continuation command.

Files: `internal/app/budget_rules.go`, `internal/app/budget_rules_test.go`,
`plugin/odometer.js`, `internal/app/odometer_plugin.js`,
`plugin/continuation.test.mjs` and this document. UI and budget amounts are unchanged.

Focused Go app/service tests, all seven plugin regressions, syntax checks,
`git diff --check` and the v6 Wails build passed. The review executable's copied
SHA256 was verified. No live continuation was submitted during this diagnosis.

### Visible continuation and compact layout (v7)

The Gemini report was checked against the saved conversation, without sending
any live request: CompanyHub `gemini-3.8-flash` completed seven assistant steps
and finished successfully. However, the isolated two-TUI regression reproduced
a real discrepancy: the server plugin's in-process client could cancel/submit
in a different live scope from the native TUI client. Its fallback response was
stored and charged, while the terminal retained its original running/interrupted
view. A confirmed response alone did not prove that the owning terminal updated.

Cancellation, idle checks and automatic submission now go through the owning
TUI's client. Each command is addressed to its presence ID and conversation,
checks the current budget generation and STOP state, and has a bounded
acknowledgement wait. A missing acknowledgement pauses instead of resubmitting.
Headless OpenCode retains its SDK path. An old companion must be restarted;
its presence capability is checked before using the new transport.

The companion reveals each new automatic continuation once using the native
prompt's scroll callback. The Odometer prompt header shows the effective
`Routing: <model> [provider]`; OpenCode's own default-model label is separate
from the route enforced by the plugin. Subsequent paid messages remain routed
to the configured fallback, and each session has its own fallback cap.

The dock remains 340 by 46 CSS pixels. Fallback captions have two individual
single lines, the stopped amount has a larger dedicated line, and the per-charge
amount appears higher in the bar. The expanded input explicitly says `ORIGINAL $`
when the progress bar represents the separately labelled fallback budget.
Compact notifications keep the bar at 46 pixels and grow the native window only
to the measured notification height; dismissal restores the compact rectangle.
Confirmed acknowledgements no longer overwrite a later stopped status.
Provider authentication failures are reported as paused/authentication failed,
without suggesting that raising the budget repairs a key.

Validation: focused Go app/service/model-metadata tests, plugin continuation
regressions, browser flows and native WebView2 checks passed. Two actual isolated
OpenCode 1.14.39 TUIs, using a localhost fake provider, both visibly continued
on the fallback. One exhausted fallback was blocked before the provider while
the other continued; native Esc, targeted STOP, conversation binding and normal
close still passed. Native checks covered measured notification sizing,
dismissal, three expand/dock cycles, minimise/restore and persisted limits.
No live provider inference or live budget changes were made during validation.

Launch the v7 review executable before fully restarting OpenCode so both its
server plugin and TUI companion load the update. Automatic transitions then
require no confirmation click. Existing saved conversations can be reopened.

| Files | Change |
|---|---|
| `internal/app/budget_rules.go`, `open_sessions.go`, `app.go`, `abort.go`, `run.go` | Persist per-TUI choices; attribute original/fallback usage; process transitions; cancel current activity; immediately re-evaluate limit edits; preserve historical spend. |
| `internal/app/model_metadata.go`, `experience.go`, `internal/modelmeta/` | Cache public model facts, store the private optional key, join exact benchmarks, combine owning-instance availability/capabilities and route pricing. |
| `internal/plugin/contract.go`, `plugin/odometer.js`, `internal/app/odometer_plugin.js` | Publish strict verdicts/usage fingerprints; synchronise accounting before each model/tool step; stop/prepare/arm/continue; deduplicate and confirm model selection. |
| `internal/wservice/budget_rules.go`, `service.go`, `experience.go`, `internal/app/controls.go` | Rule/picker/metadata/resume APIs; RUN/TRIP/TOTAL; expanded sizing and compact toast positioning. |
| `frontend/dist/budget-rules.js`, `sessions.js`, `main.js`, `index.html`, `style.css` | Compact footer and draft popup, filters, paid/free controls, labelled counters, five-state dock, fallback bars, toasts and independent scrolling. |
| `internal/app/budget_rules_test.go`, `open_sessions_test.go`, `internal/modelmeta/catalog_test.go`, `internal/wservice/open_sessions_test.go`, `plugin/continuation.test.mjs` | Core and plugin regression coverage. |
| `tools/model-switch-smoke.cjs`, `multi-session-ui-smoke.cjs` | Real OpenCode cancellation/continuation and browser interaction/layout checks. |
| `.gitignore`, `.github/workflows/ci.yml`, `CONTRIBUTING.md`, `README.md`, `docs/` | Protect runtime/key files, add syntax checks, document the new behaviour and review evidence. |
| `frontend/wailsjs/` | Wails-generated service and model bindings. |
