# OpenCode Odometer — Implementation & Architecture Compaction Report

**Date:** 2026-09-07  
**Location:** `C:\Users\ujjwal\Desktop\opencode_odo\docs\COMPACT_STATUS.md`  
**Status:** All core fixes, cross-platform plugin integration, live rate calculation, and End-to-End workflow tests implemented and verified.

---

## 1. Summary of Issues Identified & Resolved

| Component | Issue / Logical Fallacy | Resolution | Status |
| :--- | :--- | :--- | :--- |
| **OpenCode SSE Ingest** (`internal/opencode/client.go`) | `flush()` discarded events if `curType` was empty (OpenCode sends `data: {"type": ...}` without `event:` header). Also, nested payload structures (`properties.message`, direct properties) were dropped. | Added fallback JSON envelope type extraction and multi-shape payload unwrapping (`properties.info`, `properties.message`, direct). | **FIXED** |
| **History Auto-Seeding** (`internal/app/run.go`, `internal/wservice/service.go`) | `SeedHistory()` was never called by the Wails backend on startup, leaving `a.state.Seeded = false`. `PublishBudget()` aborted early when `!Seeded`, preventing `budget.json` from ever publishing. | Wired auto-seeding upon SSE stream connection and periodic polling; ensured `PublishBudget()` runs immediately after seeding and state changes. | **FIXED** |
| **Plugin Pointer Discovery** (`internal/app/pointer.go`, `main.go`) | The Go backend never wrote `~/.opencode-odometer.json` at startup, leaving the plugin unable to locate the active `budget.json` and `grace_claims.json`. | Created `WritePointer()` and called it at application initialization. | **FIXED** |
| **Plugin Cross-Platform Support** (`plugin/odometer.js`) | Hardcoded `process.platform !== "win32"` guard blocked macOS/Linux. Binary paths were restricted to Windows `.exe`. | Removed OS restriction, added multi-path binary discovery for Windows and macOS (`build/bin/`, `.app/Contents/MacOS/`, `dist/`), and standardized data paths across OSes. | **FIXED** |
| **Burn Rate Calculation** (`internal/app/app.go`, `internal/app/run.go`) | Scanning all historical records and dividing by elapsed span produced erratic rates. Python used a rolling 10-minute delta window. | Implemented thread-safe `rateWindow` sliding 10-minute delta window (`sum(deltas in 10m) * 6`), matching Python semantics and decaying to $0/hr when idle. | **FIXED** |
| **Model Key Normalization** (`internal/prices/prices.go`, `internal/app/app.go`) | Keys were blindly concatenated (`provider + "/" + model`), causing mismatch (`/anthropic/claude-3-5-sonnet`) and zero pricing. `ActiveModel` sorted by string MID. | Implemented `NormalizeKey()` and case-insensitive/suffix fallback matching. Fixed `ActiveModel()` to select the latest message by timestamp. | **FIXED** |
| **UI State Persistence** (`internal/app/controls.go`, `internal/wservice/service.go`) | Control actions (Trip reset, Mode toggle, Limit, Dock position) mutated in-memory state without persisting to `odometer_state.json`. | Added `SaveState()` across all control handlers. | **FIXED** |
| **SSE Multiline & Context Cancellation** (`internal/opencode/client.go`, `internal/wservice/service.go`) | `Subscribe` dropped multiline JSON SSE payloads when subsequent lines lacked `data:` prefix. Also, background ingest loop didn't support context cancellation, blocking server close. | Added multiline continuation handling, top-level `sessionID` fallback extraction, context cancellation (`SubscribeWithContext`, `RunContext`), and graceful server connection closing. | **FIXED** |
| **Active Session Resolution** (`internal/wservice/service.go`) | `ActiveSession()` failed to resolve when timestamps were equivalent or session was initial. | Hardened `ActiveSession()` selection logic to safely pick the active session for abort and override controls. | **FIXED** |

---

## 2. End-to-End Workflow Verification

A comprehensive end-to-end integration test suite was verified in [`internal/app/e2e_workflow_test.go`](file:///C:/Users/ujjwal/Desktop/opencode_odo/internal/app/e2e_workflow_test.go) simulating the full OpenCode environment:
1. **Mock OpenCode Server**: REST endpoints (`/global/health`, `/session`, `/session/{id}/message`, `/session/{id}/abort`) and SSE event stream (`/event`).
2. **Pointer Discovery**: Verified `~/.opencode-odometer.json` is generated on start.
3. **Seeding**: Verified historical session turns are backfilled and priced into the ledger.
4. **Live Streaming & Rate**: Streamed live turns through SSE; verified real-time cost updates, active model tracking, and burn rate spikes.
5. **Budget Thresholds**: Tested transitions from `ok` -> `warn` (at 80%) -> `over` (at 100%).
6. **Soft Grace Claims**: Simulated plugin recording a grace claim in `grace_claims.json`, verified odometer absorbs and decrements grace.
7. **Control Actions**: Verified Trip Reset, View Mode Toggle (TRIP / TOTAL), CSV Export, and Session Abort.

---

## 3. Current Repository Status

* All Go unit and integration tests (`go test ./...`) are passing with 100% success.
* Wails assets in `frontend/dist/` are updated.
* Plugin in `plugin/odometer.js` is ready for cross-platform OpenCode environments.
