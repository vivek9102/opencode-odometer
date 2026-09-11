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
import {
  existsSync,
  readFileSync,
  writeFileSync,
  renameSync,
  appendFileSync,
  mkdirSync,
  statSync,
  rmSync,
} from "node:fs"
import { join } from "node:path"
import { homedir, tmpdir } from "node:os"

// Where the odometer is installed. Override with OPENCODE_ODOMETER_HOME.
const ODO_DIR =
  process.env.OPENCODE_ODOMETER_HOME ||
  [
    join(homedir(), ".local", "share", "opencode-odometer"),
    join(homedir(), "opencode-odometer"),
    join(homedir(), "Desktop", "opencode_odo"),
  ].find((p) => existsSync(p)) ||
  join(homedir(), "opencode-odometer")

const CANDIDATE_EXES = [
  join(ODO_DIR, "build", "bin", "OpenCode_Odometer.exe"),
  join(ODO_DIR, "build", "bin", "OpenCode_Odometer.app", "Contents", "MacOS", "OpenCode_Odometer"),
  join(ODO_DIR, "build", "bin", "OpenCode_Odometer"),
  join(ODO_DIR, "dist", "OpenCode_Odometer.exe"),
  join(ODO_DIR, "dist", "OpenCode_Odometer"),
  join(ODO_DIR, "odometer.exe"),
  join(ODO_DIR, "OpenCode_Odometer.exe"),
  join(ODO_DIR, "OpenCode_Odometer"),
]

const EXE = CANDIDATE_EXES.find((p) => existsSync(p)) || CANDIDATE_EXES[0]
const PY = join(ODO_DIR, "opencode_monitor.py")

// The odometer writes budget.json to its data dir (LOCALAPPDATA on Windows,
// Application Support on macOS, XDG_DATA_HOME elsewhere).
const DATA_DIR =
  process.env.OPENCODE_ODOMETER_DIR ||
  (process.platform === "win32"
    ? join(process.env.LOCALAPPDATA || homedir(), "OpenCodeOdometer")
    : process.platform === "darwin"
    ? join(homedir(), "Library", "Application Support", "OpenCodeOdometer")
    : join(process.env.XDG_DATA_HOME || join(homedir(), ".local", "share"), "OpenCodeOdometer"))

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

// The odometer runs as a separate process and cannot know which port this
// OpenCode instance bound to (the default is 4096, but it increments when
// taken, so a hardcoded port silently reads nothing). We are handed the real
// `serverUrl`, so publish it for the odometer to discover.
const SERVER_POINTER_FILE = join(homedir(), ".opencode-odometer-server.json")

function publishServerUrl(serverUrl) {
  if (!serverUrl) return
  try {
    const url = String(serverUrl).replace(/\/+$/, "")
    // Skip a rewrite when nothing changed, so we do not churn the file on
    // every plugin load.
    try {
      if (existsSync(SERVER_POINTER_FILE)) {
        const cur = JSON.parse(readFileSync(SERVER_POINTER_FILE, "utf8"))
        if (cur?.server_url === url && cur?.pid === process.pid) return
      }
    } catch {
      /* unreadable - fall through and rewrite */
    }

    const body = JSON.stringify(
      {
        _comment:
          "Written by the OpenCode Odometer plugin so the odometer can find " +
          "this OpenCode server. Safe to delete.",
        server_url: url,
        pid: process.pid,
        updated: Math.floor(Date.now() / 1000),
      },
      null,
      2,
    )
    // write-then-rename so the odometer never reads a partial file
    const tmp = `${SERVER_POINTER_FILE}.${process.pid}.tmp`
    writeFileSync(tmp, body, "utf8")
    renameSync(tmp, SERVER_POINTER_FILE)
  } catch {
    /* discovery is best effort - never break OpenCode over it */
  }
}

// Re-resolved per read: the odometer may start after us and move its data dir.
function budgetPaths() {
  const p = pointer()
  const paths = []
  if (p?.budget) paths.push(p.budget)
  if (p?.data_dir) paths.push(join(p.data_dir, "budget.json"))
  paths.push(join(DATA_DIR, "budget.json"))
  paths.push(join(ODO_DIR, "build", "bin", "budget.json"))
  paths.push(join(ODO_DIR, "dist", "budget.json"))
  paths.push(join(ODO_DIR, "budget.json"))
  return [...new Set(paths)]
}

function graceFile() {
  const p = pointer()
  return p?.grace || join(p?.data_dir || DATA_DIR, "grace_claims.json")
}

// ---------------------------------------------------------------------------
// Event spool
// ---------------------------------------------------------------------------
// OpenCode's TUI serves its API over an in-process transport unless started
// with an explicit --port, so by default nothing listens on TCP and the
// odometer has no server to poll. We already receive every assistant message
// here, inside OpenCode, so append them to a small JSONL file the odometer
// tails. This makes cost tracking work with a plain `opencode` launch, with no
// ports, flags or config.
//
// The odometer owns pricing; we only report raw token usage.

const SPOOL_MAX_BYTES = 4 * 1024 * 1024

function spoolFile() {
  const p = pointer()
  const dir = p?.data_dir || DATA_DIR
  return join(dir, "events.jsonl")
}

// Messages are re-emitted as a turn streams, so only write when the token
// counts actually changed. Without this the spool would grow by a record per
// keystroke-sized update.
const lastSeen = new Map()

function spoolWrite(record) {
  try {
    const target = spoolFile()

    if (record.type !== "message.removed") {
      const sig = `${record.tokens.input}/${record.tokens.output}/${record.tokens.cache.read}/${record.tokens.cache.write}`
      if (lastSeen.get(record.id) === sig) return
      lastSeen.set(record.id, sig)
      // Bound the dedupe map; a long session must not leak memory.
      if (lastSeen.size > 5000) {
        for (const k of lastSeen.keys()) {
          lastSeen.delete(k)
          if (lastSeen.size <= 4000) break
        }
      }
    }

    try {
      mkdirSync(join(target, ".."), { recursive: true })
    } catch {
      /* already exists */
    }

    // The odometer resumes from a byte offset and resets when the file
    // shrinks, so truncating here is safe.
    try {
      if (statSync(target).size > SPOOL_MAX_BYTES) rmSync(target, { force: true })
    } catch {
      /* missing file is fine */
    }

    appendFileSync(target, JSON.stringify(record) + "\n", "utf8")
  } catch {
    /* telemetry is best effort - never break a turn over it */
  }
}

// Pull the fields the odometer needs out of the many shapes an assistant
// message arrives in across OpenCode versions.
function toRecord(msg, type) {
  if (!msg) return null
  const info = msg.info || msg.message || msg
  const id = info.id || info.messageID
  if (!id) return null

  const role = String(info.role || "").toLowerCase()
  if (role && role !== "assistant") return null

  let provider = info.providerID || info.provider || ""
  let model = info.modelID || info.model || ""
  if (!provider && typeof model === "string" && model.includes("/")) {
    const i = model.indexOf("/")
    provider = model.slice(0, i)
    model = model.slice(i + 1)
  }

  const t = info.tokens || {}
  const cache = t.cache || {}
  return {
    type: type || "message",
    id,
    sessionID: info.sessionID || "",
    providerID: provider,
    modelID: model,
    tokens: {
      input: Number(t.input) || 0,
      output: Number(t.output) || 0,
      reasoning: Number(t.reasoning) || 0,
      cache: { read: Number(cache.read) || 0, write: Number(cache.write) || 0 },
    },
    cost: Number(info.cost) || 0,
    finish: info.finish || "",
    timeCreated: Number(info.time?.created || info.timeCreated || 0) || 0,
  }
}

// On first load, backfill any turns that happened while the odometer was not
// running. The odometer keys on message id and upserts, so re-sending is
// harmless.
async function spoolBackfill(client) {
  try {
    const sessions = await client.session.list?.()
    const list = Array.isArray(sessions) ? sessions : sessions?.data
    if (!Array.isArray(list)) return

    // Only the most recent few sessions: this runs on every OpenCode start.
    for (const s of list.slice(0, 5)) {
      const sid = s?.id
      if (!sid) continue
      const msgs = await client.session.messages?.({ path: { id: sid } })
      const items = Array.isArray(msgs) ? msgs : msgs?.data
      if (!Array.isArray(items)) continue
      for (const m of items) {
        const rec = toRecord(m)
        if (rec) {
          if (!rec.sessionID) rec.sessionID = sid
          spoolWrite(rec)
        }
      }
    }
  } catch {
    /* backfill is opportunistic; live events are the primary path */
  }
}

// ---------------------------------------------------------------------------
// Commands from the odometer
// ---------------------------------------------------------------------------
// The odometer has no way to reach OpenCode when the API runs in-process, so
// actions it cannot perform itself (aborting a session) are queued as a small
// file that we execute here, holding a live client.

function commandFile() {
  const p = pointer()
  return join(p?.data_dir || DATA_DIR, "commands.json")
}

const COMMAND_MAX_AGE = 30 // seconds; ignore anything stale

async function drainCommands(client) {
  const target = commandFile()
  let cmds
  try {
    if (!existsSync(target)) return
    cmds = JSON.parse(readFileSync(target, "utf8"))
    // Consume first: a command that throws must not be retried forever.
    rmSync(target, { force: true })
  } catch {
    return
  }
  if (!Array.isArray(cmds)) return

  const now = Date.now() / 1000
  for (const cmd of cmds) {
    if (!cmd || cmd.issued && now - cmd.issued > COMMAND_MAX_AGE) continue
    if (cmd.action !== "abort" || !cmd.sessionID) continue
    try {
      await client.session.abort({ path: { id: cmd.sessionID } })
    } catch {
      try {
        await client.session.abort({ path: { sessionID: cmd.sessionID } })
      } catch {
        /* session already finished */
      }
    }
  }
}

let lastLaunchAt = 0

// Minimum gap between spawn attempts. The odometer takes a moment to write
// its lock file, so retrying faster would start a second copy.
const RELAUNCH_COOLDOWN_MS = 30_000

/**
 * Report whether an odometer process is currently alive.
 *
 * The odometer holds a pid file for single-instance safety; reuse it rather
 * than latching on "we spawned once". A permanent latch meant that closing
 * the odometer required restarting OpenCode to get it back, because this
 * module stays loaded for the lifetime of the session.
 */
function odometerAlive() {
  try {
    const lockPath = join(tmpdir(), "opencode_odometer.lock")
    if (!existsSync(lockPath)) return false
    const pid = parseInt(readFileSync(lockPath, "utf8").trim(), 10)
    if (!pid || Number.isNaN(pid)) return false
    // Signal 0 performs the permission/existence check without delivering.
    process.kill(pid, 0)
    return true
  } catch {
    // ESRCH (no such process) or an unreadable lock: treat as not running.
    return false
  }
}

function launch() {
  if (odometerAlive()) return
  if (Date.now() - lastLaunchAt < RELAUNCH_COOLDOWN_MS) return
  lastLaunchAt = Date.now()

  let cmd, args, cwd
  const validExe = CANDIDATE_EXES.find((p) => existsSync(p))
  if (validExe) {
    cmd = validExe
    args = []
    cwd = ODO_DIR
  } else if (existsSync(PY)) {
    cmd = process.platform === "win32" ? "pythonw" : "python3"
    args = [PY]
    cwd = ODO_DIR
  } else {
    return
  }

  try {
    const child = spawn(cmd, args, {
      cwd,
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

export const OdometerPlugin = async ({ client, serverUrl }) => {
  if (process.env.OPENCODE_ODOMETER === "0") return {}

  // Publish before launching: the odometer reads this on startup, so writing
  // it first means the very first connection attempt already has the address.
  publishServerUrl(serverUrl)
  launch()

  // Catch up on anything spent while the odometer was closed.
  spoolBackfill(client)

  let warned = new Set()
  let blockNotified = new Set()

  async function toast(title, message, variant) {
    try {
      await client.tui.showToast({ body: { title, message, variant } })
    } catch {
      /* toast is best effort - never let UI failure affect the turn */
    }
  }
  // OpenCode has no structured "deny this tool" result for external plugins.
  // A thrown hook error is therefore the final safety backstop if a hard-limit
  // session reaches a tool boundary. It cannot stop model text already being
  // generated without a tool call; the normal hard-limit path below uses
  // session.abort() first so users see a clean session cancellation instead.
  function hardLimitToolCancellation(reason, detail) {
    const err = new Error(`${reason} - ${detail}`)
    err.name = "MessageAbortedError"
    err.data = { message: `${reason} - ${detail}` }
    return err
  }

  return {
    event: async ({ event }) => {
      // Execute anything the odometer asked for (e.g. STOP SESSION). Events
      // fire continuously during a turn, so this stays responsive without a
      // timer of its own.
      drainCommands(client)

      // Feed the odometer. This is the ingest path that works when OpenCode
      // has no TCP listener, which is the default for the TUI.
      if (event.type === "message.updated" || event.type === "message.created") {
        const rec = toRecord(event.properties?.info || event.properties?.message || event.properties)
        if (rec) spoolWrite(rec)
      } else if (event.type === "message.removed") {
        const id = event.properties?.messageID
        if (id) {
          spoolWrite({ type: "message.removed", id })
          lastSeen.delete(id)
        }
      }

      if (event.type === "session.created") {
        // Re-publish on activity: the odometer may have been started later,
        // or the pointer removed, and a session is the moment it matters.
        publishServerUrl(serverUrl)
        launch()
      }
      // a session that drops back under its cap can warn/block again later
      if (event.type === "session.idle") {
        const doc = readBudget()
        const s = doc?.sessions?.[event.properties?.sessionID]
        if (s && s.state === "ok") {
          warned.delete(event.properties.sessionID)
          blockNotified.delete(event.properties.sessionID)
        }
      }
      if (event.type === "session.compacted" || event.type === "experimental.session.compacting") {
        launch()
      }
    },

    "tool.execute.before": async (input) => {
      if (process.env.OPENCODE_ODOMETER_NOBLOCK === "1") return

      const doc = readBudget()
      if (!doc || !doc.enabled) return

      const sid = input.sessionID
      const s = doc.sessions?.[sid]
      if (!s) return

      if (s.state === "over" && s.enforced !== false) {
        const mode = s.mode || doc.mode || "soft"
        if (mode === "hard" || s.past_hard_stop) {
          const reason = s.past_hard_stop ? "Hard stop reached" : "Budget exceeded"
          // State the way out. A block that only reports the number leaves the
          // user stuck mid-task with no indication that the limit is theirs to
          // lift, and the spend has already happened either way.
          const detail =
            `$${s.cost.toFixed(2)} of $${s.limit} spent in this session. Tool execution blocked.\n` +
            `This limit is PER SESSION; other sessions are unaffected.\n` +
            `In the Odometer: "LET THIS ONE FINISH" to continue, "DOUBLE IT" to raise the limit, ` +
            `or untick SESSION LIMIT to stop enforcing.`
          await toast(reason, detail, "error")
          throw hardLimitToolCancellation(reason, detail)
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
