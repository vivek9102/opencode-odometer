// Optional OpenCode event diagnostics. Normal operation has no event handler.
// Enable with OPENCODE_EVENT_DIAGNOSTICS=1 before starting OpenCode.
import {appendFile, mkdir, rename, stat} from "node:fs/promises"
import {dirname, join} from "node:path"
import {homedir} from "node:os"

export const TestEvents = async () => {
  if (process.env.OPENCODE_EVENT_DIAGNOSTICS !== "1") return {}
  const file = process.env.OPENCODE_EVENT_DIAGNOSTICS_FILE || join(homedir(), ".opencode", "test-events.log")
  const targets = new Set(["message.part.updated", "message.updated", "session.updated", "session.error"])
  const pending = new Map()
  let timer, writing = false, dropped = 0
  const schedule = () => {
    if (timer || writing || !pending.size) return
    timer = setTimeout(() => {timer = null; void flush()}, 250)
    timer.unref?.()
  }
  async function flush() {
    if (writing || !pending.size) return
    writing = true
    const batch = [...pending.values()]
    pending.clear()
    if (dropped) {batch.push({type:"diagnostics.dropped",count:dropped}); dropped = 0}
    const text = batch.map(row => JSON.stringify(row)).join("\n") + "\n"
    try {
      await mkdir(dirname(file), {recursive:true})
      const size = await stat(file).then(s => s.size, () => 0)
      if (size + Buffer.byteLength(text) > 1024*1024) {
        try { await rename(file, file+".previous") }
        catch { return } // keep the size cap even if a Windows reader locks it
      }
      await appendFile(file, text, "utf8")
    } catch { /* optional diagnostics must never disrupt a request */ }
    finally {writing = false; schedule()}
  }
  return {
    event: async ({event}) => {
      if (!event || !targets.has(event.type)) return
      const p = event.properties || {}, info = p.info || {}, part = p.part || {}
      // Record routing and lifecycle metadata, never prompt/response content.
      const row = {
        time:new Date().toISOString(), type:event.type,
        session_id:p.sessionID || info.sessionID || part.sessionID || (event.type.startsWith("session.") ? info.id : undefined),
        message_id:p.messageID || (event.type.startsWith("message.") ? info.id : undefined) || part.messageID,
        part_id:part.id, role:info.role, provider:info.providerID, model:info.modelID,
        finish:info.finish, cost:info.cost, tokens:info.tokens,
        error:event.type === "session.error" ? String(p.error?.name || "unknown").slice(0,100) : undefined,
      }
      const key = [row.type,row.session_id,row.message_id,row.part_id].join("/")
      if (!pending.has(key) && pending.size >= 256) {dropped++; return}
      pending.set(key,row) // coalesce streaming updates within each batch
      schedule()
    },
  }
}
