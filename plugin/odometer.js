/**
 * OpenCode Odometer - auto-launch + session budget enforcement
 * ------------------------------------------------------------
 * 1. Starts the cost odometer as a detached background process on boot.
 *    The odometer holds a single-instance lock, so multiple OpenCode
 *    windows will not spawn duplicates.
 *
 * 2. Enforces the per-session spend limit. The odometer does all pricing
 *    and writes its verdict to budget.json; this plugin only reads that
 *    verdict and refuses to start a new turn when a session is over.
 *    Keeping the decision on the odometer side means pricing logic lives
 *    in exactly one place.
 *
 * Disable everything:  set OPENCODE_ODOMETER=0
 * Disable blocking:    set OPENCODE_ODOMETER_NOBLOCK=1
 */

import { spawn } from "node:child_process"
import { existsSync, readFileSync, writeFileSync, renameSync } from "node:fs"
import { join } from "node:path"
import { homedir } from "node:os"

// Where the odometer is installed. Override with OPENCODE_ODOMETER_HOME.
const ODO_DIR =
  process.env.OPENCODE_ODOMETER_HOME ||
  [
    join(homedir(), ".local", "share", "opencode-odometer"),
    join(homedir(), "opencode-odometer"),
    join(homedir(), "Desktop", "opencode_odo"),
  ].find((p) => existsSync(p)) ||
  join(homedir(), "opencode-odometer")

const EXE = join(ODO_DIR, "dist", "OpenCode_Odometer.exe")
const PY = join(ODO_DIR, "opencode_monitor.py")

// The odometer writes budget.json to its data dir (LOCALAPPDATA on Windows,
// XDG_DATA_HOME elsewhere). Older/portable installs keep it beside the app,
// so check both. OPENCODE_ODOMETER_DIR overrides the data location.
const DATA_DIR =
  process.env.OPENCODE_ODOMETER_DIR ||
  (process.platform === "win32"
    ? join(process.env.LOCALAPPDATA || homedir(), "OpenCodeOdometer")
    : join(process.env.XDG_DATA_HOME || join(homedir(), ".local", "share"), "opencode-odometer"))

// The odometer writes a pointer file to a fixed location on every start,
// naming its real data dir. Reading it means a moved/overridden DATA_DIR can
// never leave us reading a stale ledger from a path that no longer updates.
const POINTER_FILE = join(homedir(), ".opencode-odometer.json")

function pointer() {
  try {
    if (!existsSync(POINTER_FILE)) return null
    return JSON.parse(readFileSync(POINTER_FILE, "utf8"))
  } catch {
    return null
  }
}

// Re-resolved per read: the odometer may start after us and move its data dir.
function budgetPaths() {
  const p = pointer()
  const paths = []
  if (p?.budget) paths.push(p.budget)
  if (p?.data_dir) paths.push(join(p.data_dir, "budget.json"))
  paths.push(join(DATA_DIR, "budget.json"))
  paths.push(join(ODO_DIR, "dist", "budget.json"))
  paths.push(join(ODO_DIR, "budget.json"))
  return [...new Set(paths)]
}

function graceFile() {
  const p = pointer()
  return p?.grace || join(p?.data_dir || DATA_DIR, "grace_claims.json")
}

let launched = false

function launch() {
  if (launched) return
  launched = true

  let cmd, args
  if (existsSync(EXE)) {
    cmd = EXE
    args = []
  } else if (existsSync(PY)) {
    cmd = "pythonw"
    args = [PY]
  } else {
    return
  }

  try {
    const child = spawn(cmd, args, {
      cwd: existsSync(EXE) ? join(ODO_DIR, "dist") : ODO_DIR,
      detached: true,
      stdio: "ignore",
      windowsHide: true,
    })
    child.unref()
  } catch {
    /* the odometer is optional - never block OpenCode because of it */
  }
}

const STALE_AFTER = 120 // seconds

/**
 * Read the freshest budget ledger available.
 *
 * Deliberately scans ALL candidate paths and picks the most recently written
 * one, rather than taking the first that exists. A leftover budget.json from
 * an older install would otherwise win forever and, being stale, silently
 * disable enforcement for good.
 */
function readBudget() {
  let best = null
  for (const p of budgetPaths()) {
    try {
      if (!existsSync(p)) continue
      const doc = JSON.parse(readFileSync(p, "utf8"))
      const updated = doc.updated || 0
      if (!best || updated > best.updated) best = { doc, updated, path: p }
    } catch {
      /* unreadable or mid-write - try the next candidate */
    }
  }
  if (!best) return null

  // Stale ledger means the odometer is not running. Never keep blocking on
  // data nobody is maintaining - a dead odometer must not lock anyone out.
  if (Date.now() / 1000 - best.updated > STALE_AFTER) return null
  return best.doc
}

/**
 * Record that this session consumed a grace turn.
 *
 * budget.json has a single writer (the odometer), so we append to a small
 * side file instead. The odometer folds these claims into its state on the
 * next poll and stops advertising grace for the session.
 *
 * Returns false if the claim could not be persisted - the caller then blocks
 * rather than granting grace it cannot account for.
 */
function claimGrace(sessionID) {
  try {
    const target = graceFile()
    let claims = {}
    if (existsSync(target)) {
      try {
        claims = JSON.parse(readFileSync(target, "utf8")) || {}
      } catch {
        claims = {}
      }
    }
    claims[sessionID] = (claims[sessionID] || 0) + 1
    // write-then-rename so the odometer never reads a partial file
    const tmp = `${target}.${process.pid}.tmp`
    writeFileSync(tmp, JSON.stringify(claims), "utf8")
    renameSync(tmp, target)
    return true
  } catch {
    return false
  }
}

export const OdometerPlugin = async ({ client }) => {
  if (process.env.OPENCODE_ODOMETER === "0") return {}
  if (process.platform !== "win32") return {}

  launch()

  let warned = new Set()
  let blockNotified = new Set()

  async function toast(title, message, variant) {
    try {
      await client.tui.showToast({ body: { title, message, variant } })
    } catch {
      /* toast is best effort - never let UI failure affect the turn */
    }
  }

  return {
    event: async ({ event }) => {
      if (event.type === "session.created") launch()
      // a session that drops back under its cap can warn/block again later
      if (event.type === "session.idle") {
        const doc = readBudget()
        const s = doc?.sessions?.[event.properties?.sessionID]
        if (s && s.state === "ok") {
          warned.delete(event.properties.sessionID)
          blockNotified.delete(event.properties.sessionID)
        }
      }
    },

    "chat.message": async (input) => {
      if (process.env.OPENCODE_ODOMETER_NOBLOCK === "1") return

      const doc = readBudget()
      if (!doc || !doc.enabled) return

      const s = doc.sessions?.[input.sessionID]
      if (!s) return

      const sid = input.sessionID
      const pct = Math.round(s.fraction * 100)
      const mode = s.mode || doc.mode || "soft"

      // The limit switch being off means "do not interrupt me at all".
      // `warn` MODE is different: it is on, it just never blocks.
      const limitOn = doc.enabled !== false
      if (!limitOn) return

      // 80% - warn once, in every mode
      if (s.state === "warn" && !warned.has(sid)) {
        warned.add(sid)
        await toast(
          "Budget warning",
          `Session at $${s.cost.toFixed(3)} of $${s.limit} (${pct}%)`,
          "warning",
        )
      }

      if (s.state !== "over") return

      // `state` is a pure measurement and is reported even when the limit is
      // switched off, so enforcement must be gated explicitly. When the
      // odometer says `enforced: false`, observe but never refuse.
      if (s.enforced === false) return

      // `warn` mode observes but never refuses a turn.
      if (mode === "warn" || doc.block_when_exceeded === false) {
        if (!blockNotified.has(sid)) {
          blockNotified.add(sid)
          await toast(
            "Budget exceeded (warn only)",
            `$${s.cost.toFixed(2)} of $${s.limit} (${pct}%). Not blocking.`,
            "warning",
          )
        }
        return
      }

      // `soft` mode: spend one grace turn instead of failing outright, so a
      // nearly-finished task can land and the typed prompt is not lost.
      // Never granted past the hard ceiling - that is the runaway-loop guard.
      if (mode === "soft" && !s.past_hard_stop && (s.grace_remaining || 0) > 0) {
        if (claimGrace(sid)) {
          await toast(
            "Budget reached - one grace turn",
            `$${s.cost.toFixed(2)} of $${s.limit}. This turn is allowed; the ` +
              `next will be blocked. Raise the limit in the Odometer to continue.`,
            "warning",
          )
          return
        }
        // claim failed to persist: fall through and block rather than
        // silently granting unlimited grace.
      }

      const reason = s.past_hard_stop
        ? `Hard stop at ${s.hard_stop_at}x limit`
        : "Budget limit reached"
      const detail =
        `$${s.cost.toFixed(2)} of $${s.limit} spent. Raise the limit in the ` +
        `Odometer window, or set OPENCODE_ODOMETER_NOBLOCK=1 to bypass.`

      // Always toast: this is the part the user actually reads.
      if (!blockNotified.has(sid)) {
        blockNotified.add(sid)
        await toast(
          s.past_hard_stop ? "Hard stop reached" : "Session budget reached",
          detail,
          "error",
        )
      }

      // Prefer a real abort over a thrown Error. `session.abort` is the same
      // path the Esc key uses, so OpenCode renders it as a normal cancellation
      // instead of an unhandled-plugin-exception stack trace.
      let aborted = false
      try {
        await client.session.abort({ path: { id: sid } })
        aborted = true
      } catch {
        try {
          // older/newer SDKs name the path param differently
          await client.session.abort({ path: { sessionID: sid } })
          aborted = true
        } catch {
          /* fall through to throwing */
        }
      }

      if (aborted) {
        // Give the abort a moment to land, then leave quietly. The turn is
        // already cancelled; throwing on top would re-introduce the ugly error.
        await new Promise((r) => setTimeout(r, 50))
        return
      }

      // Fallback only: abort unavailable. Mimic OpenCode's own cancellation
      // error so the TUI treats it as an abort rather than a plugin crash.
      const err = new Error(`${reason} - ${detail}`)
      err.name = "MessageAbortedError"
      err.data = { message: `${reason} - ${detail}` }
      throw err
    },
  }
}
