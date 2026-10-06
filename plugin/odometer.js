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
import { dirname, isAbsolute, join } from "node:path"
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

const EXECUTABLE_NAMES = ["OpenCode_Odometer.exe", "odometer.exe", "OpenCode_Odometer"]
const PY = join(ODO_DIR, "opencode_monitor.py")

// The odometer writes budget.json to its data dir (LOCALAPPDATA on Windows,
// Application Support on macOS, XDG_DATA_HOME elsewhere).
const PLUGIN_INSTANCE = `${process.pid}-${Date.now()}`

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
const POINTER_FILE =
  process.env.OPENCODE_ODOMETER_POINTER || join(homedir(), ".opencode-odometer.json")
function pointer() {
  try {
    if (!existsSync(POINTER_FILE)) return null
    return JSON.parse(readFileSync(POINTER_FILE, "utf8"))
  } catch {
    return null
  }
}

function isFile(path) {
  try {
    return typeof path === "string" && path.length > 0 && statSync(path).isFile()
  } catch {
    return false
  }
}

// Prefer the exact executable recorded by the last successfully started app.
// app_dir keeps compatibility with older pointer files. The fixed install
// locations remain fallbacks for first-run and source-build workflows.
function resolveExe() {
  const p = pointer()
  const candidates = []
  if (process.env.OPENCODE_ODOMETER_EXE) candidates.push(process.env.OPENCODE_ODOMETER_EXE)
  if (p?.executable) candidates.push(p.executable)
  if (p?.app_dir && isAbsolute(p.app_dir)) {
    candidates.push(...EXECUTABLE_NAMES.map((name) => join(p.app_dir, name)))
  }
  candidates.push(...CANDIDATE_EXES)
  return [...new Set(candidates)].find(isFile) || null
}

// The odometer runs as a separate process and cannot know which port this
// OpenCode instance bound to (the default is 4096, but it increments when
// taken, so a hardcoded port silently reads nothing). We are handed the real
// `serverUrl`, so publish it for the odometer to discover.
const SERVER_POINTER_FILE = process.env.OPENCODE_ODOMETER_SERVER_POINTER || join(homedir(), ".opencode-odometer-server.json")

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

// Optional SDK work must never hold plugin initialization or a chat forever.
async function sdkCall(call, ms = 2000) {
  const controller = new AbortController()
  let timer
  try {
    return await Promise.race([Promise.resolve().then(() => call(controller.signal)), new Promise((_, reject) => {
      timer = setTimeout(() => { controller.abort(); reject(new Error("Odometer SDK timeout")) }, ms)
      timer.unref?.()
    })])
  } finally { clearTimeout(timer) }
}

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

    if (record.type === "message") {
      const sig = `${record.tokens.input}/${record.tokens.output}/${record.tokens.reasoning}/${record.tokens.cache.read}/${record.tokens.cache.write}/${record.cost}/${record.finish}`
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
async function spoolBackfill(client, observeSession) {
  try {
    const sessions = await sdkCall(signal => client.session.list?.({signal}))
    const list = Array.isArray(sessions) ? sessions : sessions?.data
    if (!Array.isArray(list)) return

    // Preserve ownership of recent chats when the observed set is bounded.
    for (const session of [...list].reverse()) observeSession(session)

    // Only the most recent few sessions: this runs on every OpenCode start.
    for (const s of list.slice(0, 5)) {
      const sid = s?.id
      if (!sid) continue
      const msgs = await sdkCall(signal => client.session.messages?.({ path: { id: sid }, signal }))
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
      await sdkCall(signal => client.session.abort({ path: { id: cmd.sessionID }, signal }))
    } catch {
      try {
        await sdkCall(signal => client.session.abort({ path: { sessionID: cmd.sessionID }, signal }))
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
  try {
    if(JSON.parse(readFileSync(join(pointer()?.data_dir || DATA_DIR,"launch-suppressed.json"),"utf8")).instances?.includes(PLUGIN_INSTANCE))return
  }catch{/* no suppression */}
  try {
    const dir = pointer()?.data_dir || DATA_DIR
    if (JSON.parse(readFileSync(join(dir, "preferences.json"), "utf8")).auto_start === false) return
  } catch { /* default: attached to OpenCode startup */ }
  if (odometerAlive()) return
  if (Date.now() - lastLaunchAt < RELAUNCH_COOLDOWN_MS) return
  lastLaunchAt = Date.now()

  let cmd, args, cwd
  const validExe = resolveExe()
  if (validExe) {
    cmd = validExe
    args = ["--autostart"]
    cwd = dirname(validExe)
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
    // One outstanding claim per chat until the odometer acknowledges it.
    // Siblings cannot each consume the same advertised grace turn.
    if (claims[sessionID] > 0) return false
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

  let warned = new Set()
  let blockNotified = new Set()

  async function toast(title, message, variant) {
    try {
      await sdkCall(signal => client.tui.showToast({ body: { title, message, variant }, signal }))
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

  // Only public model metadata crosses the bridge; never credentials or headers.
  const bridgeDir = () => pointer()?.data_dir || DATA_DIR
  const pending = new Map()
  const awaiting = new Map()
  let inventory = []
  const observedSessions = new Set()
  const sessionParents = new Map()
  function sessionBudget(doc, sid) {
    const seen = new Set()
    let root = sid
    while (sessionParents.get(root) && !seen.has(root)) { seen.add(root); root = sessionParents.get(root) }
    return doc?.sessions?.[root] || doc?.sessions?.[sid]
  }
  let bridgeWork = null
  let inventoryBusy = false
  let lastInventoryAttempt = 0
  let lastHeartbeat = 0
  function observeSession(session) {
    const sid = session?.id
    if (!sid) return
    const parent = session.parentID || ""
    if (sessionParents.get(sid) !== parent) {
      sessionParents.set(sid, parent)
      spoolWrite({type:"session", id:sid, parentID:parent})
    }
    observeID(sid, false)
    if (parent) observeID(parent, false)
    heartbeat()
  }
  function observeID(sid, force = true) {
    if (!sid) return
    if (observedSessions.has(sid)) {
      observedSessions.delete(sid)
      observedSessions.add(sid)
      return
    }
    observedSessions.add(sid)
    if (observedSessions.size > 100) observedSessions.delete(observedSessions.values().next().value)
    heartbeat(force)
  }
  function atomicBridge(name, doc) {
    const dir = bridgeDir()
    mkdirSync(dir, { recursive: true })
    const target = join(dir, name)
    const tmp = `${target}.${process.pid}.tmp`
    writeFileSync(tmp, JSON.stringify(doc), "utf8")
    renameSync(tmp, target)
  }
  function writeSwitchStatus(req, status, detail) {
    try { atomicBridge(`switch-status-${req.id}.json`, { ...req, status, detail }) } catch { /* UI can report timeout */ }
  }
  function category(key) {
    const k = key.toLowerCase()
    if (/embed|e5-|rerank/.test(k)) return "embedding"
    if (/image|ocr/.test(k)) return "image"
    if (/asr|realtime|audio|tts/.test(k)) return "audio"
    return "chat"
  }
  function heartbeat(force = false) {
    const now = Date.now()
    if (!force && now-lastHeartbeat < 10_000) return
    lastHeartbeat = now
    try { atomicBridge(`models-${process.pid}.json`, { switch_protocol:3, pid: process.pid, instance:PLUGIN_INSTANCE, updated: Math.floor(Date.now()/1000), sessions: [...observedSessions], models: inventory }) } catch { /* bridge is optional */ }
  }
  async function refreshInventory() {
    if (inventoryBusy) return
    inventoryBusy = true
    lastInventoryAttempt = Date.now()/1000
    try {
      const response = await sdkCall(signal => client.provider.list({signal}))
      const doc = response?.data || response
      if (!Array.isArray(doc?.all) || !Array.isArray(doc?.connected)) return
      inventory = doc.all.filter(p => doc.connected.includes(p.id)).flatMap(p =>
        Object.entries(p.models || {}).map(([id, m]) => ({
          key: p.id + "/" + id, name: m.name || id, category: category(id),
          // SDK-normalised zero rates can mean unknown for private providers.
          // Do not turn them into claims that an unpriced model is free.
          ...(m.cost && (m.cost.input > 0 || m.cost.output > 0) ? {rate: {input:m.cost.input,output:m.cost.output,cache_read:m.cost.cache_read || 0,cache_write:m.cost.cache_write || 0}} : {}),
        })))
      heartbeat(true)
    } catch { /* unavailable SDK; backend config fallback remains visible */ }
    finally { inventoryBusy = false }
  }
  function drainSwitch() {
    if (bridgeWork) return bridgeWork
    bridgeWork = consumeSwitch().finally(() => { bridgeWork = null })
    return bridgeWork
  }
  // Durable selections belong to a chat, never to a process or its bounded
  // ownership history. Claim only inside that chat's actual prompt hook.
  function takeChatSelection(sid) {
    if (!sid || !/^[a-zA-Z0-9_-]+$/.test(sid)) return
    const target=join(bridgeDir(),`switch-session-${sid}.json`)
    const claim=target+`.${process.pid}.claim`
    let req
    try { req=JSON.parse(readFileSync(target,"utf8")) } catch { return }
    // Chat choices are read on every turn. Older one-request files retain
    // their original semantics until the user makes a new persistent choice.
    if (!req.persistent) {
    try { renameSync(target,claim) } catch { return }
    try {
      req=JSON.parse(readFileSync(claim,"utf8"))
    } catch { /* malformed selections must never break a prompt */ }
    finally { rmSync(claim,{force:true}) }
    }
    if (!req?.id || !/^[a-zA-Z0-9-]+$/.test(req.id) || req.session_id!==sid) return
    const previous=pending.get(sid)
    if(previous) { pending.delete(sid);writeSwitchStatus(previous,"cancelled","Replaced by a newer selection.") }
    const model=inventory.find(m=>m.key===req.key)
    if (!model || model.category!=="chat") {
      const detail=model ? "This specialised model cannot run a coding chat." : "The selected model is unavailable in this OpenCode instance. Choose another model."
      writeSwitchStatus(req,"failed",detail)
      // Never silently charge the default model when a saved choice fails.
      throw hardLimitToolCancellation("Model switch failed",detail)
    }
    return req
  }
  async function consumeSwitch() {
    try {
      const target = join(bridgeDir(), `switch-${process.pid}.json`)
      if (!existsSync(target)) return
      const req = JSON.parse(readFileSync(target, "utf8"))
      rmSync(target, {force:true})
      if (!req?.id || !/^[a-zA-Z0-9-]+$/.test(req.id)) return
      if (!req.session_id || !req.key || Date.now()/1000 - req.issued > 30) {
        writeSwitchStatus(req,"expired","The selection expired before OpenCode received it."); return
      }
      const model = inventory.find(m => m.key === req.key)
      if (!model || model.category !== "chat") {
        writeSwitchStatus(req,"failed",model ? "This specialised model cannot run a coding chat." : "The model is not available in this OpenCode instance."); return
      }
      try {
        const result = await sdkCall(signal => client.session.get({path:{id:req.session_id}, signal}))
        if (result?.error || !result?.data?.id) throw new Error("Session unavailable")
      } catch {writeSwitchStatus(req,"failed","The selected chat is unavailable in OpenCode.");return}
      const prev=pending.get(req.session_id)
      if (prev) writeSwitchStatus(prev,"cancelled","Replaced by a newer selection.")
      pending.set(req.session_id,req)
      writeSwitchStatus(req,"queued","Ready for the next request in this chat. The current response continues.")
    } catch { /* malformed bridge request must never break a turn */ }
  }
  // Provider initialization can depend on Plugin.init(). Awaiting /provider
  // here forms a cycle and leaves OpenCode on a blank startup screen.
  heartbeat(true)
  const startupTimer = setTimeout(() => {
    refreshInventory()
    spoolBackfill(client, observeSession)
  }, 0)
  startupTimer.unref?.()
  const bridgeTimer=setInterval(() => {
    heartbeat()
    drainSwitch()
    drainCommands(client)
    const now=Date.now()/1000
    if(!inventory.length && !inventoryBusy && now-lastInventoryAttempt>=5) void refreshInventory()
    for (const [sid,req] of pending) if(now-req.issued>600) {pending.delete(sid);writeSwitchStatus(req,"expired","No request was submitted within ten minutes.")}
    for (const [sid,req] of awaiting) if(now-req.applied_at>120) {awaiting.delete(sid);writeSwitchStatus(req,"unconfirmed","Request was routed; OpenCode has not confirmed the response model.")}
  }, 1000)
  bridgeTimer.unref?.()
  const inventoryTimer=setInterval(refreshInventory, 60_000)
  inventoryTimer.unref?.()

  async function enforceChat(input) {
      if (process.env.OPENCODE_ODOMETER_NOBLOCK === "1") return

      const doc = readBudget()
      if (!doc || !doc.enabled) return

      const s = sessionBudget(doc, input.sessionID)
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
        if (claimGrace(s.budget_session_id || sid)) {
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
        await sdkCall(signal => client.session.abort({ path: { id: sid }, signal }))
        aborted = true
      } catch {
        try {
          // older/newer SDKs name the path param differently
          await sdkCall(signal => client.session.abort({ path: { sessionID: sid }, signal }))
          aborted = true
        } catch {
          /* fall through to throwing */
        }
      }

      if (aborted) {
        // Give the abort a moment to land, then leave quietly. The turn is
        // already cancelled; throwing on top would re-introduce the ugly error.
        await new Promise((r) => setTimeout(r, 50))
        return false
      }

      // Fallback only: abort unavailable. Mimic OpenCode's own cancellation
      // error so the TUI treats it as an abort rather than a plugin crash.
      const err = new Error(`${reason} - ${detail}`)
      err.name = "MessageAbortedError"
      err.data = { message: `${reason} - ${detail}` }
      throw err
  }

  return {
    event: async ({ event }) => {
      // Execute anything the odometer asked for (e.g. STOP SESSION). Events
      // fire continuously during a turn, so this stays responsive without a
      // timer of its own.
      await drainCommands(client)

      void drainSwitch()
      const info=event.properties?.info || event.properties?.message
      const sid=info?.sessionID || event.properties?.sessionID
      if (event.type === "session.created" || event.type === "session.updated") observeSession(info)
      if (sid) observeID(sid)
      if (info?.role === "assistant") {
        const req=awaiting.get(info.sessionID)
        if(req && info.parentID === req.message_id) {
          awaiting.delete(info.sessionID)
          const actual=info.providerID + "/" + info.modelID
          writeSwitchStatus(req,actual === req.key ? "confirmed" : "failed",actual === req.key ? (req.persistent ? "OpenCode confirmed this model. It stays active for this chat." : "OpenCode confirmed the response model.") : "OpenCode responded with " + actual)
        }
      }
      if (event.type === "session.error") {
        const req=awaiting.get(sid);if(req){awaiting.delete(sid);writeSwitchStatus(req,"failed","OpenCode reported a request error.")}
      }
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
      const s = sessionBudget(doc, sid)
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

    "chat.message": async (input, output) => {
      observeID(input.sessionID)
      await drainCommands(client)
      await drainSwitch()
      const allowed = await enforceChat(input)
      if (allowed === false) return
      // Budget checks happen before claiming: a blocked turn keeps its choice.
      if (!output?.message) return
      if (existsSync(join(bridgeDir(),`switch-session-${input.sessionID}.json`)) && !inventory.length) await refreshInventory()
      const durable = takeChatSelection(input.sessionID)
      const req = durable || pending.get(input.sessionID)
      if (!req) return
      if (!durable && Date.now() / 1000 - req.issued > 600) {
        pending.delete(input.sessionID)
        writeSwitchStatus(req, "expired", "No request was submitted within ten minutes.")
        return
      }
      if (!output?.message) {
        writeSwitchStatus(req, "failed", "This OpenCode version does not expose a mutable chat message.")
        pending.delete(input.sessionID)
        return
      }
      const slash = req.key.indexOf("/")
      output.message.model = { providerID: req.key.slice(0, slash), modelID: req.key.slice(slash + 1) }
      delete output.message.variant
      req.message_id = output.message.id
      req.applied_at = Date.now() / 1000
      pending.delete(input.sessionID)
      awaiting.set(input.sessionID, req)
      writeSwitchStatus(req, "applied", req.persistent ? "Applied to this message. This model stays active for the chat." : "Applied to the next request; awaiting OpenCode's response.")
    },
  }
}
