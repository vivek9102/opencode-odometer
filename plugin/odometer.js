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
  readdirSync,
  watch,
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
  // Bound to the actual turn, not to a picker choice that may target the next
  // turn while a paid response is still running.
  const turnModels = new Map()
  const modelKey = model => model?.providerID && model?.modelID ? model.providerID+"/"+model.modelID : ""
  const confirmedFree = (doc,key) => !!key && Array.isArray(doc?.free_models) && doc.free_models.includes(key)
  let inventory = []
  const observedSessions = new Set()
  let openTUISessions = new Set()
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
    const newOwnership=!observedSessions.has(sid) || (parent && !observedSessions.has(parent))
    if (sessionParents.get(sid) !== parent) {
      sessionParents.set(sid, parent)
      spoolWrite({type:"session", id:sid, parentID:parent})
    }
    observeID(sid, false)
    if (parent) observeID(parent, false)
    heartbeat(!!newOwnership)
  }
  function observeID(sid, force = true) {
    if (!sid) return
    if (observedSessions.has(sid)) {
      observedSessions.delete(sid)
      observedSessions.add(sid)
      return
    }
    observedSessions.add(sid)
    if (observedSessions.size > 100) {
      const oldest=[...observedSessions].find(id=>!openTUISessions.has(id))
      if(oldest){observedSessions.delete(oldest);turnModels.delete(oldest)}
    }
    heartbeat(force)
  }
  function refreshTUIOwnership() {
    let names=[];try{names=readdirSync(bridgeDir())}catch{return}
    const current=new Set()
    for(const name of names){
      if(!name.startsWith('tui-')||!name.endsWith('.json'))continue
      let tui;try{tui=JSON.parse(readFileSync(join(bridgeDir(),name),'utf8'))}catch{continue}
      if(tui.pid!==process.pid||tui.closed||name!==`tui-${tui.id}.json`||!/^ses_[a-zA-Z0-9]+$/.test(tui.session_id||''))continue
      current.add(tui.session_id)
    }
    openTUISessions=current
    for(const sid of current)observeID(sid)
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
    try { atomicBridge(`models-${process.pid}.json`, { switch_protocol:3, continue_protocol:2, abort_protocol:1, pid: process.pid, instance:PLUGIN_INSTANCE, updated: Math.floor(Date.now()/1000), sessions: [...observedSessions], models: inventory }) } catch { /* bridge is optional */ }
  }
  function chatRoot(sid) {
    const seen=new Set();
    while(sessionParents.get(sid) && !seen.has(sid)){seen.add(sid);sid=sessionParents.get(sid)}
    return sid;
  }
  async function executeAbort(req) {
    let status="stopped",detail="OpenCode acknowledged cancellation.";
    try {
      if(Date.now()/1000-req.issued>30)throw new Error("Stop request expired. Try again.");
      if(!observedSessions.has(req.session_id))throw new Error("The requested chat is unavailable in this OpenCode instance.");
      // Cancel the parent and its delegated sessions together, leaving other
      // chats running. Abort executes outside event hooks: the SDK publishes
      // idle events while cancelling, so awaiting it in a hook can deadlock.
      const ids=new Set([req.session_id,...[...observedSessions].filter(sid=>chatRoot(sid)===req.session_id)]);
      await Promise.all([...ids].map(async id=>{
        const result=await sdkCall(signal=>client.session.abort({path:{id},signal}));
        if(result?.error || result?.data===false || result===false)throw new Error("OpenCode rejected cancellation for "+id);
      }));
    }catch(err){status="failed";detail=err?.message || "OpenCode could not stop this chat."}
    try{atomicBridge(`abort-status-${req.id}.json`,{id:req.id,status,detail})}catch{/* caller reports timeout */}
  }
  function drainAborts() {
    let names;
    try{names=readdirSync(bridgeDir())}catch{return}
    for(const name of names){
      if(!new RegExp(`^abort-${process.pid}-[a-zA-Z0-9-]+\\.json$`).test(name))continue;
      const target=join(bridgeDir(),name);
      let req;
      try{
        req=JSON.parse(readFileSync(target,"utf8"));
        if(req.instance!==PLUGIN_INSTANCE || !/^[a-zA-Z0-9-]+$/.test(req.id) || !req.session_id)continue;
        // Claim synchronously before scheduling any SDK work; watcher and
        // fallback polling cannot execute a request twice.
        rmSync(target);
      }catch{continue}
      void executeAbort(req);
    }
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
          canonical_model_id:m.canonical_model_id || "", tool_call:m.tool_call ?? m.capabilities?.toolcall,
          reasoning:m.reasoning ?? m.capabilities?.reasoning, context:m.limit?.context || 0, input_modalities:m.modalities?.input || Object.entries(m.capabilities?.input || {}).filter(([,enabled])=>enabled).map(([type])=>type),
          modalities:m.modalities || {},
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

  const continuations=new Map(), completedUsage=new Map(),blockedTools=new Set()
  const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms))
  function continuationVerdict(req) {
    const doc=readBudget(),s=doc&&sessionBudget(doc,req.session_id)
    if(!doc?.enabled || !s?.enforced || s.manual_stop || s.generation!==req.id)throw Error("Continuation cancelled: the budget or selected conversation changed.")
    if(s.stage==="fallback"&&s.state==="over")throw Error("Fallback allowance exhausted; the session remains paused.")
    return s
  }
  async function continuationBridge(name,doc) {
    // Windows readers can briefly deny replacement of a bridge file. Retry
    // only this atomic publication, never a model submission or tool action.
    for(let attempt=0;;attempt++){
      try{atomicBridge(name,doc);return}catch(error){
        if(attempt>=5||!['EPERM','EACCES','EBUSY'].includes(error.code))throw error
        await delay(10*2**attempt)
      }
    }
  }
  function continuationStatus(req,status,detail,ids=[]) {
    return continuationBridge(`continue-status-${req.id}.json`,{id:req.id,status,detail,original_ids:ids})
  }
  async function conversationMessages(sid) {
    const result=await sdkCall(signal=>client.session.messages({path:{id:sid},signal}))
    if(result?.error)throw Error("Could not inspect the interrupted conversation.")
    const messages=result?.data || result
    if(!Array.isArray(messages))throw Error("OpenCode did not return conversation messages.")
    return messages
  }
  async function nativeContinuationCall(req,action,body) {
    // The plugin's in-process HTTP client can have a different live event
    // scope from the owning TUI. Use that TUI's client for cancellation and
    // submission so its running turn and synced messages change together.
    let owner;
    for(const name of readdirSync(bridgeDir())){
      if(!name.startsWith("tui-")||!name.endsWith(".json"))continue;
      try{const row=JSON.parse(readFileSync(join(bridgeDir(),name),"utf8"));if(row.started&&row.pid===process.pid&&row.session_id===req.session_id&&!row.closed){owner=row;break}}catch{}
    }
    if(!owner)return null; // Headless OpenCode has no terminal client.
    if(owner.continue_protocol!==1)throw Error("Restart OpenCode to load automatic continuation in its owning TUI.");
    const id=`${req.id}-${action}-${Date.now()}`,responseFile=join(bridgeDir(),`continue-tui-status-${id}.json`);
    await continuationBridge(`continue-tui-${owner.id}.json`,{id,instance:owner.id,session_id:req.session_id,generation:req.id,action,body,children:req.children||[],issued:Date.now()/1000});
    const end=Date.now()+15000;
    while(Date.now()<end){
      try{const response=JSON.parse(readFileSync(responseFile,"utf8"));if(response.id===id){try{rmSync(responseFile,{force:true})}catch{};if(response.error)throw Error(response.error);return response.result||{data:true}}}catch(e){if(e.message&&!['ENOENT'].includes(e.code)&&!(e instanceof SyntaxError))throw e}
      await delay(50);
    }
    throw Error("Owning TUI did not acknowledge automatic continuation. No retry was sent.");
  }
  async function continueAutomatically(req) {
    let originalIDs=[]
    try {
      continuationVerdict(req)
      const selected=inventory.find(m=>m.key===req.key&&m.category==="chat")
      if(!selected)throw Error("Configured fallback is unavailable in this OpenCode instance.")
      // Do not abort a running tool and blindly replay its side effects.
      // The exhausted verdict blocks new tool/model work; let active tools settle.
      const end=Date.now()+30_000
      let rootMessages=[]
      while(true){
        continuationVerdict(req)
        let busy=false
        for(const sid of [req.session_id,...(req.children||[])]){
          const messages=await conversationMessages(sid)
          if(sid===req.session_id)rootMessages=messages
          busy ||= messages.some(m=>(m.parts||[]).some(p=>p.type==="tool"&&["pending","running"].includes(p.state?.status)))
        }
        if(!busy)break
        if(Date.now()>end)throw Error("A tool is still running. Automatic continuation was paused to avoid repeating actions.")
        await delay(100)
      }
      const nativeAbort=await nativeContinuationCall(req,"abort");
      if(!nativeAbort)for(const sid of [req.session_id,...(req.children||[])]){
        const result=await sdkCall(signal=>client.session.abort({path:{id:sid},signal}))
        if(result?.error)throw Error("OpenCode did not acknowledge cancellation.")
      }
      // Abort acknowledgement plus idle status prevents submitting into an
      // old running turn. This also works when OpenCode has no TCP listener.
      while(true){
        continuationVerdict(req)
        const result=await nativeContinuationCall(req,"status")||await sdkCall(signal=>client.session.status({signal}))
        if(result?.error)throw Error("Could not confirm that OpenCode stopped.")
        const states=result?.data || result
        if(!states || typeof states!=="object")throw Error("Could not confirm idle session state.")
        if([req.session_id,...(req.children||[])].every(sid=>!states[sid]||states[sid].type==="idle"))break
        if(Date.now()>end)throw Error("OpenCode did not become idle; automatic continuation was paused.")
        await delay(100)
      }
      for(const sid of [req.session_id,...(req.children||[])]){
        const messages=await conversationMessages(sid)
        if(sid===req.session_id)rootMessages=messages
        originalIDs.push(...messages.filter(m=>m.info?.role==="assistant").map(m=>m.info.id))
        if(messages.some(m=>(m.parts||[]).some(p=>p.type==="tool"&&["pending","running"].includes(p.state?.status))))throw Error("An interrupted tool has an uncertain result. The session remains paused.")
        if(messages.some(m=>(m.parts||[]).some(p=>p.type==="tool"&&p.state?.status==="error"&&/abort|interrupt|cancel/i.test(p.state.error||"")&&!blockedTools.has(`${sid}:${m.info?.parentID}:${p.callID}`))))throw Error("An interrupted tool has an uncertain result. Verify it before resuming.")
      }
      await continuationStatus(req,"prepared","Original turn stopped; preparing fallback allowance.",originalIDs)
      while(continuationVerdict(req).stage!=="fallback"){
        if(Date.now()>end)throw Error("Fallback allowance was not activated; the session remains paused.")
        await delay(50)
      }
      const choice={id:req.id,session_id:req.session_id,key:req.key,persistent:true,source:"budget",issued:req.issued}
      await continuationBridge(`switch-session-${req.session_id}.json`,choice)
      const last=rootMessages.findLast(m=>m.info?.role==="assistant")?.info
      const lastUser=rootMessages.findLast(m=>m.info?.role==="user")?.info
      // A finished answer is not an outstanding task: select the fallback
      // for future messages without buying an unnecessary extra response.
      if(last?.finish==="stop"&&!last.error&&last.time?.completed && !rootMessages.findLast(m=>m.info?.id===last.id)?.parts?.some(p=>p.type==="tool")){
        await continuationStatus(req,"completed","Task already completed; fallback selected for the next message.",originalIDs);return
      }
      const allowance=continuationVerdict(req)
      // A conservative estimate includes the uncached conversation, completed
      // tool output and space for tool schemas. It is not a provider quote.
      const usage=last?.tokens||{},knownInput=(usage.input||0)+(usage.cache?.read||0)+(usage.cache?.write||0)+(usage.output||0)
      const inputEstimate=Math.max(knownInput,Buffer.byteLength(JSON.stringify(rootMessages),"utf8"))+2048
      // Private routes often omit context metadata. Unknown is not a zero
      // capacity: attempt the user's configured model and let OpenCode/provider
      // validate it. Do not invent a context limit or demand a confirmation.
      const context=req.context||selected.context||0
      if(context>0&&inputEstimate+32>context)throw Error("Conversation exceeds the configured fallback context capacity. The session remains paused.")
      for(const m of rootMessages)for(const p of m.parts||[]){
        if(p.type!=="file")continue
        const modality=p.mime?.startsWith("image/")?"image":p.mime?.startsWith("audio/")?"audio":p.mime?.startsWith("video/")?"video":p.mime==="application/pdf"?"pdf":"text"
        if(modality!=="text"&&!req.input_modalities?.includes(modality))throw Error("Fallback does not have confirmed support for a conversation attachment.")
      }
      if(!confirmedFree(readBudget(),req.key)&&!(allowance.limit-allowance.cost>(inputEstimate*req.input_price+32*req.output_price)/1_000_000))throw Error("Fallback allowance cannot cover the estimated next request. Increase it before resuming.")
      await continuationStatus(req,"dispatching","Sending one automatic continuation on the fallback.",originalIDs)
      continuationVerdict(req) // STOP may arrive while a bridge write is retried.
      const [providerID,...modelParts]=req.key.split("/")
      // Async submission avoids holding a synchronous HTTP request for the
      // entire coding task. Correlate its first successful model step using
      // a unique user-message ID, not another turn in the same conversation.
      // Match OpenCode's ascending message-ID clock (low 48 bits of ms*4096).
      // An arbitrary UUID/timestamp prefix would sort after later normal turns
      // and could make the continuation appear to be the last user forever.
      const clock=((BigInt(Date.now())*4096n+1n)&((1n<<48n)-1n)).toString(16).padStart(12,"0")
      const messageID=`msg_${clock}${req.id.replaceAll("-","").slice(0,14).padEnd(14,"0")}`
      if(!client.session.promptAsync)throw Error("This OpenCode version does not support asynchronous continuation.")
      const promptBody={messageID,agent:lastUser?.agent||"build",model:{providerID,modelID:modelParts.join("/")},parts:[{type:"text",text:"[Odometer automatic budget continuation] The configured fallback allowance is active. Resume the outstanding user task from the interruption point in this conversation. Changing models does not change the requested scope, depth, deliverables, or completion criteria. Complete the remaining requested work; do not conclude early or replace unfinished work with a high-level summary because of this budget transition. Review completed and interrupted tool actions first, preserve completed work, and verify uncertain results before repeating actions. Respect existing permissions and constraints. Do not restart the task from scratch."}]}
      const response=await nativeContinuationCall(req,"prompt",promptBody)||await sdkCall(signal=>client.session.promptAsync({path:{id:req.session_id},body:promptBody,signal}),10_000)
      if(response?.error)throw Error("Fallback submission failed. The session remains paused; no automatic retry was sent.")
      const confirmEnd=Date.now()+300_000
      while(true){
        continuationVerdict(req)
        const answer=(await conversationMessages(req.session_id)).find(m=>m.info?.role==="assistant"&&m.info?.parentID===messageID)
        if(answer?.info?.error)throw Error(fallbackFailure(answer.info.error,req.key))
        if(answer?.info?.finish){
          const actual=answer.info.providerID+"/"+answer.info.modelID
          if(actual!==req.key)throw Error("OpenCode did not confirm the configured fallback model.")
          await continuationStatus(req,"confirmed",`Switched to ${selected.name} · continuing.`,originalIDs);break
        }
        if(Date.now()>confirmEnd)throw Error("Fallback response was not confirmed; the session remains paused. No automatic retry was sent.")
        await delay(250)
      }
    }catch(e){await continuationStatus(req,"failed",String(e.message||e),originalIDs)}
  }
  function fallbackFailure(error,key){
    const status=error?.data?.statusCode||error?.statusCode;
    const message=String(error?.data?.message||error?.message||"");
    const reason=status===429||/rate.?limit/i.test(message)?"rate limit":status===401||status===403||error?.name==="ProviderAuthError"||/expired.?key|invalid.?api.?key|api.?key.*not valid|API_KEY_INVALID/i.test(message)?"authentication failed":"request failed";
    return `Fallback ${key} stopped: ${reason}. No other provider was tried.`;
  }
  function reportFallbackFailure(sid,error,actual){
    if(!sid||!error||error.name==="MessageAbortedError"||error.name==="AbortError")return;
    const s=sessionBudget(readBudget()||{},sid);
    if(s?.stage!=="fallback"||s.manual_stop||!s.fallback_model||actual&&actual!==s.fallback_model)return;
    if(!s.budget_session_id||!/^[a-zA-Z0-9-]+$/.test(s.budget_session_id))return;
    // Subsequent user turns also need failure reporting after STOP invalidates
    // the original continuation generation. Never select an alternate route.
    atomicBridge(`fallback-error-${s.budget_session_id}.json`,{id:s.budget_session_id,session_id:chatRoot(sid),generation:s.generation||"",key:s.fallback_model,detail:fallbackFailure(error,s.fallback_model)});
  }
  function drainContinuations(){
    let names=[];try{names=readdirSync(bridgeDir())}catch{return}
    for(const name of names){
      if(!name.startsWith(`continue-${process.pid}-`)||!name.endsWith(".json"))continue
      const file=join(bridgeDir(),name);let req
      try{req=JSON.parse(readFileSync(file,"utf8"))}catch{continue}
      if(!req?.id||!/^[a-zA-Z0-9-]+$/.test(req.id)||req.instance!==PLUGIN_INSTANCE||!req.session_id||!observedSessions.has(req.session_id))continue
      const claim=file+".claim"
      try{renameSync(file,claim)}catch{continue}
      const existing=join(bridgeDir(),`continue-status-${req.id}.json`)
      if(continuations.has(req.id)||existsSync(existing)){rmSync(claim,{force:true});continue}
      const task=(async()=>{
        if(Date.now()/1000-req.issued>60){await continuationStatus(req,"failed","Automatic continuation expired; the session remains paused.");return}
        await continuationStatus(req,"claimed","Waiting for original work to stop.")
        await continueAutomatically(req)
      })().catch(()=>{/* Persistent bridge errors are reported by the widget's timeout. */}).finally(()=>{continuations.delete(req.id);try{rmSync(claim,{force:true})}catch{}})
      continuations.set(req.id,task)
    }
  }
  async function syncStrictUsage(input) {
    let doc=readBudget(),s=doc&&sessionBudget(doc,input.sessionID)
    if(!s?.strict || !doc?.enabled)return
    let root=input.sessionID;const visited=new Set();while(sessionParents.has(root)&&!visited.has(root)){visited.add(root);root=sessionParents.get(root)}
    const observation=completedUsage.get(input.sessionID)||completedUsage.get(root)
    if(!observation)return
    const end=Date.now()+4000
    while(doc?.accounted?.[observation.id]!==observation.signature){
      if(Date.now()>end||!doc?.enabled||!s)throw new DOMException("Budget accounting unavailable; request paused before provider dispatch.","AbortError")
      await delay(50);doc=readBudget();s=doc&&sessionBudget(doc,input.sessionID)
    }
  }
  function pendingSelection(sid) {
    const req=pending.get(sid)
    if(req && Date.now()/1000-req.issued>600){pending.delete(sid);writeSwitchStatus(req,"expired","No request was submitted within ten minutes.");return}
    return req
  }
  // Durable selections belong to a chat, never to a process or its bounded
  // ownership history. Claim only inside that chat's actual prompt hook.
  function takeChatSelection(sid, peek = false, incomingKey = "") {
    if (!sid || !/^[a-zA-Z0-9_-]+$/.test(sid)) return
    const target=join(bridgeDir(),`switch-session-${sid}.json`)
    const claim=target+`.${process.pid}.claim`
    let req
    try { req=JSON.parse(readFileSync(target,"utf8")) } catch { return }
    const doc=readBudget(),budget=sessionBudget(doc||{},sid)
    // A late continuation must never repin a released allowance. Explicit
    // native picker choices also take precedence over a one-shot restoration.
    if ((req?.source==="budget" && doc && (budget?.stage!=="fallback" || budget.generation!==req.id)) ||
        (req?.source==="budget_restore" && incomingKey && incomingKey!==req.key)) {
      try { rmSync(target,{force:true}) } catch {}
      pending.delete(sid)
      writeSwitchStatus(req,"cancelled","Budget routing released; following OpenCode selection.")
      return
    }
    // Chat choices are read on every turn. Older one-request files retain
    // their original semantics until the user makes a new persistent choice.
    if (!req.persistent && !peek) {
    try { renameSync(target,claim) } catch { return }
    try {
      req=JSON.parse(readFileSync(claim,"utf8"))
    } catch { /* malformed selections must never break a prompt */ }
    finally { rmSync(claim,{force:true}) }
    }
    if (!req?.id || !/^[a-zA-Z0-9-]+$/.test(req.id) || req.session_id!==sid) return
    const previous=pending.get(sid)
    if(previous && !peek) { pending.delete(sid);writeSwitchStatus(previous,"cancelled","Replaced by a newer selection.") }
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
  // Avoid adding a native filesystem watcher inside Bun on Windows. Polling
  // the small command directory keeps cancellation independent of chat events
  // and avoids that runtime's watcher lifecycle entirely.
  if(process.platform==="win32") {
    const abortTimer=setInterval(drainAborts,250)
    abortTimer.unref?.()
  } else try {
    const abortWatcher=watch(bridgeDir(),(_event,name)=>{
      // Windows can keep delivering rename events after a watched directory
      // is removed; release that handle instead of spinning on stale state.
      if(!existsSync(bridgeDir())){abortWatcher.close();return;}
      if(name?.toString().startsWith(`abort-${process.pid}-`))drainAborts();
    });
    abortWatcher.on("error",()=>abortWatcher.close());
    abortWatcher.unref?.();
  }catch{/* polling fallback */}
  drainAborts()
  const startupTimer = setTimeout(() => {
    refreshInventory()
    spoolBackfill(client, observeSession)
  }, 0)
  startupTimer.unref?.()
  const bridgeTimer=setInterval(() => {
    refreshTUIOwnership()
    heartbeat()
    drainSwitch()
    drainCommands(client)
    drainAborts()
    drainContinuations()
    const now=Date.now()/1000
    if(!inventory.length && !inventoryBusy && now-lastInventoryAttempt>=5) void refreshInventory()
    for (const [sid,req] of pending) if(now-req.issued>600) {pending.delete(sid);writeSwitchStatus(req,"expired","No request was submitted within ten minutes.")}
    for (const [sid,req] of awaiting) if(now-req.applied_at>120) {awaiting.delete(sid);writeSwitchStatus(req,"unconfirmed","Request was routed; OpenCode has not confirmed the response model.")}
  }, 1000)
  bridgeTimer.unref?.()
  const inventoryTimer=setInterval(refreshInventory, 60_000)
  inventoryTimer.unref?.()

  function deferBudgetBlock(input, output, key) {
    if (!output?.message) return
    const slash = key.indexOf("/")
    if (slash > 0) {
      output.message.model = {providerID:key.slice(0,slash),modelID:key.slice(slash+1)}
      delete output.message.variant
    }
    turnModels.set(input.sessionID,{key,userID:output.message.id,blocked:true})
  }

  async function enforceChat(input, key = "", deferBlock = false) {
      if (process.env.OPENCODE_ODOMETER_NOBLOCK === "1") return

      const doc = readBudget()
      if (!doc || !doc.enabled) return
      // Historical spend stays over the limit. Only this confirmed free
      // request is allowed; paid requests still use the existing verdict.
      const s = sessionBudget(doc, input.sessionID)
      if (!s) return
      if(s.strict && s.enforced!==false){
        const denied=s.stage==="switching" || s.stage==="fallback" && key!==s.fallback_model && !confirmedFree(doc,key)
        if(denied){if(deferBlock)return true;throw new DOMException("Session paused by its budget rule; no request dispatched.","AbortError")}
      }
      if (confirmedFree(doc,key)) return

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

      // chat.message executes outside OpenCode's response error handler.
      // Let that hook save the user's message, then refuse the request in
      // chat.params, inside the handled stream and before provider dispatch.
      if (deferBlock) return true
      const reason = s.past_hard_stop && mode === "soft"
        ? `Hard stop at ${s.hard_stop_at || 1.5}x limit`
        : "Budget limit reached"
      const detail =
        `$${s.cost.toFixed(2)} of $${s.limit} spent. Choose a free model or raise the limit in Odometer to continue.`

      // Always toast: this is the part the user actually reads.
      await toast(reason, detail, "error")
      // OpenCode converts AbortError into a handled MessageAbortedError.
      // Show the limit notification on each refused attempt, including after
      // a free turn, rather than suppressing it for the whole budget period.
      throw new DOMException(`${reason} - ${detail}`, "AbortError")
  }

  return {
    event: async ({ event }) => {
      // Execute anything the odometer asked for (e.g. STOP SESSION). Events
      // fire continuously during a turn, so this stays responsive without a
      // timer of its own.
      void drainCommands(client)

      void drainSwitch()
      const info=event.properties?.info || event.properties?.message
      const sid=info?.sessionID || event.properties?.sessionID
      if (event.type === "session.created" || event.type === "session.updated") observeSession(info)
      if (sid) observeID(sid)
      if (info?.role === "assistant") {
        reportFallbackFailure(sid,info.error,modelKey(info))
        if(info.finish&&info.tokens){const t=info.tokens;completedUsage.set(info.sessionID,{id:info.id,signature:`${t.input||0}/${t.output||0}/${t.reasoning||0}/${t.cache?.read||0}/${t.cache?.write||0}/${info.finish}`})}
        const turn=turnModels.get(info.sessionID)
        if(!turn || info.parentID===turn.userID)turnModels.set(info.sessionID,{...turn,key:modelKey(info),userID:info.parentID})
        const req=awaiting.get(info.sessionID)
        if(req && info.parentID === req.message_id) {
          awaiting.delete(info.sessionID)
          const actual=info.providerID + "/" + info.modelID
          writeSwitchStatus(req,actual === req.key ? "confirmed" : "failed",actual === req.key ? (req.persistent ? "OpenCode confirmed this model. It stays active for this chat." : "OpenCode confirmed the response model.") : "OpenCode responded with " + actual)
        }
      }
      if (event.type === "session.error") {
        reportFallbackFailure(sid,event.properties?.error)
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
        turnModels.delete(event.properties?.sessionID)
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
      if(s.strict){
        try{await syncStrictUsage(input);await enforceChat(input,turnModels.get(sid)?.key||"")}
        catch(e){
          // A rejection from this before-hook proves the tool body was not
          // entered. It is safe to retry on the fallback, unlike an aborted
          // running tool with an uncertain side effect.
          const userID=turnModels.get(sid)?.userID
          if(e.name==="AbortError"&&input.callID&&userID){blockedTools.add(`${sid}:${userID}:${input.callID}`);if(blockedTools.size>5000)blockedTools.delete(blockedTools.values().next().value)}
          throw e
        }
        return
      }
      if(confirmedFree(doc,turnModels.get(sid)?.key))return

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
      const cancelled=sessionBudget(readBudget()||{},input.sessionID)
      const automatic=output?.parts?.some(p=>p.type==="text"&&p.text?.startsWith("[Odometer automatic budget continuation]"))
      if(cancelled?.manual_stop&&cancelled.cancellation_id&&!automatic&&output?.message){
        try{atomicBridge(`continue-reset-${cancelled.budget_session_id}.json`,{id:cancelled.budget_session_id,session_id:chatRoot(input.sessionID),token:cancelled.cancellation_id})}catch{}
      }
      void drainCommands(client)
      await drainSwitch()
      if (existsSync(join(bridgeDir(),`switch-session-${input.sessionID}.json`)) && !inventory.length) await refreshInventory()
      // Inspect without consuming a one-shot choice: paid choices must remain
      // queued when the cap refuses the turn.
      const incomingKey=modelKey(output?.message?.model || input.model)
      const preview=takeChatSelection(input.sessionID,true,incomingKey) || pendingSelection(input.sessionID)
      const previewKey=(output?.message && preview?.key) || modelKey(output?.message?.model || input.model)
      if (await enforceChat(input,previewKey,true)) {
        deferBudgetBlock(input,output,previewKey)
        return
      }
      // Budget checks happen before claiming: a blocked turn keeps its choice.
      if (!output?.message) return
      const durable = takeChatSelection(input.sessionID,false,incomingKey)
      const req = durable || pendingSelection(input.sessionID)
      const key=req?.key || modelKey(output.message.model || input.model)
      // A choice can change while the async budget check is running. Recheck
      // its actual key so a free-to-paid race cannot bypass enforcement.
      if(key!==previewKey && await enforceChat(input,key,true)) {
        deferBudgetBlock(input,output,key)
        return
      }
      turnModels.set(input.sessionID,{key,approvedKey:key,userID:output.message.id})
      if (!req) return
      const slash = req.key.indexOf("/")
      output.message.model = { providerID: req.key.slice(0, slash), modelID: req.key.slice(slash + 1) }
      delete output.message.variant
      req.message_id = output.message.id
      req.applied_at = Date.now() / 1000
      pending.delete(input.sessionID)
      awaiting.set(input.sessionID, req)
      writeSwitchStatus(req, "applied", req.persistent ? "Applied to this message. This model stays active for the chat." : "Applied to the next request; awaiting OpenCode's response.")
    },
    "chat.params": async (input) => {
      await syncStrictUsage(input)
      const key = modelKey({providerID:input.model?.providerID,modelID:input.model?.id})
      const turn = turnModels.get(input.sessionID)
      // An allowed turn has already claimed its grace in chat.message. Do
      // not charge grace again on each model step. Deferred blocks and an
      // actual model differing from that choice still need enforcement.
      const strict=sessionBudget(readBudget()||{},input.sessionID)?.strict
      if (strict || !turn || turn.blocked || turn.userID !== input.message?.id || turn.approvedKey !== key) {
        await enforceChat(input,key)
      }
    },
  }
}
