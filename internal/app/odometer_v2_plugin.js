// OpenCode 2.0.28 server adapter. Provider credentials and conversation text
// stay in OpenCode; only accounting metadata crosses the local file bridge.
import {normalizeModel} from "./v2-models.js"
import {hasConversation} from "./v2-sessions.js"
export {normalizeModel} from "./v2-models.js"
import {randomUUID} from "node:crypto"
import {spawn} from "node:child_process"
import {readFileSync,writeFileSync,renameSync,appendFileSync,mkdirSync,readdirSync,rmSync,statSync} from "node:fs"
import {join} from "node:path"
import {homedir} from "node:os"

const instance=`${process.pid}-${randomUUID()}`
const sleep=ms=>new Promise(resolve=>setTimeout(resolve,ms))
const read=file=>{try{return JSON.parse(readFileSync(file,"utf8"))}catch{return null}}
const pointer=()=>read(process.env.OPENCODE_ODOMETER_POINTER||join(homedir(),".opencode-odometer.json"))
const directory=()=>pointer()?.data_dir||process.env.OPENCODE_ODOMETER_DIR||join(process.env.LOCALAPPDATA||homedir(),"OpenCodeOdometer")
const key=model=>model?.providerID&&model?.id?`${model.providerID}/${model.id}`:""
const model=route=>{const i=route.indexOf("/");if(i<1)throw Error("Invalid configured model");return{providerID:route.slice(0,i),id:route.slice(i+1)}}
const millis=value=>typeof value==="number"?value:Date.parse(String(value))||0
const identifier=value=>typeof value==="string"&&/^[a-zA-Z0-9_-]+$/.test(value)
const unwrap=result=>result?.data??result
const signature=r=>`${r.tokens.input}/${r.tokens.output}/${r.tokens.reasoning}/${r.tokens.cache.read}/${r.tokens.cache.write}/${r.finish}`
const text="[Odometer automatic budget continuation] The configured fallback allowance is active. Resume the outstanding user task from the interruption point in this conversation. Changing models does not change the requested scope, depth, deliverables, or completion criteria. Complete the remaining requested work; do not conclude early or replace unfinished work with a high-level summary because of this budget transition. Review completed and interrupted tool actions first, preserve completed work, and verify uncertain results before repeating actions. Respect existing permissions and constraints. Do not restart the task from scratch."

function write(name,value){
  const dir=directory();mkdirSync(dir,{recursive:true})
  const file=join(dir,name),tmp=`${file}.${process.pid}.tmp`
  writeFileSync(tmp,JSON.stringify(value))
  // A short Windows reader can temporarily deny rename. Retrying file
  // publication does not repeat an OpenCode operation or an inference.
  for(let attempt=0;;attempt++){
    try{renameSync(tmp,file);return}catch(error){
      if(attempt>=4||!["EPERM","EACCES","EBUSY"].includes(error.code))throw error
      Atomics.wait(new Int32Array(new SharedArrayBuffer(4)),0,0,10)
    }
  }
}
function budget(){const doc=read(join(directory(),"budget.json"));return doc&&Date.now()/1000-doc.updated<=120?doc:null}
function launch(){
  const p=pointer(),dir=directory()
  if(p?.api_major!==2||!p.executable||read(join(dir,"preferences.json"))?.auto_start===false||read(join(dir,"launch-suppressed.json"))?.instances?.includes(instance))return
  try{spawn(p.executable,["--autostart"],{detached:true,stdio:"ignore",windowsHide:true}).unref()}catch{}
}
async function call(fn,ms=4000){
  const controller=new AbortController();let timer
  try{return await Promise.race([Promise.resolve().then(()=>fn(controller.signal)),new Promise((_,reject)=>{timer=setTimeout(()=>{controller.abort();reject(Error("OpenCode operation timed out"))},ms)})])}
  finally{clearTimeout(timer)}
}

export function normalizeUsage(message,sessionID){
  if(!["assistant","compaction"].includes(message?.type)||!message.tokens||!identifier(message.id)||!identifier(sessionID)||!key(message.model))return null
  const t=message.tokens
  return{type:"message",id:message.id,sessionID,providerID:message.model.providerID,modelID:message.model.id,
    tokens:{input:Number(t.input)||0,output:Number(t.output)||0,reasoning:Number(t.reasoning)||0,cache:{read:Number(t.cache?.read)||0,write:Number(t.cache?.write)||0}},
    cost:Number(message.cost)||0,finish:message.type==="compaction"?"compaction":message.finish||(message.error?"error":""),timeCreated:millis(message.time?.created)}
}


// One transport per server process; a location-scoped setup contributes its
// own catalog and hooks. A second project must not duplicate command handling.
let runtime
export default {
  id:"opencode-odometer-v2",
  async setup(ctx){
    if(process.env.OPENCODE_ODOMETER==="0")return
    if(!String(ctx.app?.version||"").replace(/^v/,"").startsWith("2."))throw Error("Odometer V2 requires OpenCode V2")
    runtime ||= createRuntime()
    const live=runtime,location=ctx.location.directory
    live.contexts.set(location,ctx)
    await ctx.session.hook("prompt",event=>live.prompt(ctx,event))
    for(const kind of ["context","compaction","title","generate"])await ctx.session.hook(kind,event=>live.enforce(ctx,event,kind))
    await ctx.tool.hook("execute.before",event=>live.enforce(ctx,event,"tool"))
    void live.refresh();launch()
    return()=>{if(live.contexts.get(location)===ctx){live.contexts.delete(location);live.catalogs.delete(location)};if(!live.contexts.size){live.close();runtime=null}}
  },
}

function createRuntime(){
  const contexts=new Map(),catalogs=new Map(),parents=new Map(),latest=new Map(),steps=new Map(),seen=new Map(),blockedTools=new Set(),routes=new Map(),tasks=new Map(),sessions=new Map()
  const published=new Set(),backfilled=new Set(),catalogTimes=new Map();let closed=false,refreshing=false,draining=false,subscription
  const root=sid=>{const visited=new Set();while(parents.get(sid)&&!visited.has(sid)){visited.add(sid);sid=parents.get(sid)}return sid}
  const verdict=(doc,sid)=>doc?.sessions?.[root(sid)]||doc?.sessions?.[sid]
  const free=(doc,route)=>!!route&&doc?.free_models?.includes(route)
  const status=(req,state,detail,ids=[])=>write(`continue-status-${req.id}.json`,{id:req.id,status:state,detail,original_ids:ids})
  function spool(record){
    if(!record)return
    const sig=record.type==="message"?signature(record)+`/${record.cost}`:JSON.stringify(record)
    if(seen.get(record.id)===sig)return
    seen.set(record.id,sig);if(seen.size>5000)seen.delete(seen.keys().next().value)
    try{const dir=directory();mkdirSync(dir,{recursive:true});const file=join(dir,"events.jsonl");try{if(statSync(file).size>4*1024*1024)rmSync(file)}catch{};appendFileSync(file,JSON.stringify(record)+"\n")}catch{}
    if(record.type==="message"&&record.finish)latest.set(record.sessionID,{id:record.id,signature:signature(record)})
  }
  async function observe(ctx,sid){
    if(!identifier(sid))throw Error("Invalid conversation identity")
    const info=unwrap(await call(signal=>ctx.session.get({sessionID:sid},{signal})))
    sessions.set(sid,ctx.location.directory);parents.set(sid,info.parentID||"")
    spool({type:"session",id:sid,parentID:info.parentID||""})
    if(info.parentID&&!parents.has(info.parentID))await observe(ctx,info.parentID)
    return info
  }
  async function messages(ctx,sid){
    const result=unwrap(await call(signal=>ctx.session.context({sessionID:sid},{signal})))
    if(!Array.isArray(result))throw Error("OpenCode returned invalid conversation history")
    for(const item of result)spool(normalizeUsage(item,sid))
    return result
  }
  function presence(){
    let files=[];try{files=readdirSync(directory())}catch{}
    return files.filter(f=>/^tui-[a-zA-Z0-9-]+\.json$/.test(f)).map(f=>read(join(directory(),f))).filter(t=>t&&!t.closed&&t.api_major===2&&t.server_pid===process.pid&&Date.now()/1000-t.updated<30)
  }
  function ownerContext(tui,sid=tui.session_id){return contexts.get(tui.tabs?.find(t=>t.session_id===sid)?.location)||contexts.get(sessions.get(sid))||contexts.get(tui.location)}
  async function refresh(){
    if(refreshing||closed)return;refreshing=true
    try{
      for(const [location,ctx] of contexts){
        if(catalogs.has(location)&&Date.now()-(catalogTimes.get(location)||0)<60000)continue
        try{const list=unwrap(await call(signal=>ctx.model.list(undefined,{signal})));if(Array.isArray(list)){catalogs.set(location,list.filter(m=>m.enabled!==false).map(normalizeModel));catalogTimes.set(location,Date.now())}}catch{}
      }
      for(const tui of presence()){
        const ids=[...new Set([tui.session_id,...(tui.tabs||[]).map(t=>t.session_id)].filter(identifier))]
        const models=new Map()
        for(const sid of ids){
          const ctx=ownerContext(tui,sid);if(!ctx)continue
          try{await observe(ctx,sid);if(!backfilled.has(sid)){await messages(ctx,sid);backfilled.add(sid)}}catch{}
          for(const m of catalogs.get(ctx.location.directory)||[])models.set(m.key,m)
        }
        if(!ids.length)continue
        write(`models-${tui.pid}.json`,{api_major:2,server_pid:process.pid,switch_protocol:3,continue_protocol:2,abort_protocol:1,
          pid:tui.pid,instance,updated:Math.floor(Date.now()/1000),sessions:ids,models:[...models.values()]})
        published.add(tui.pid)
      }
      for(const pid of published)if(!presence().some(t=>t.pid===pid)){try{rmSync(join(directory(),`models-${pid}.json`),{force:true})}catch{};published.delete(pid)}
    }finally{refreshing=false}
  }
  async function accounted(ctx,sid){
    let doc=budget(),s=verdict(doc,sid)
    if(!doc?.enabled||!s?.strict||s.enforced===false)return
    await messages(ctx,sid)
    const observation=latest.get(sid)||latest.get(root(sid));if(!observation)return
    const end=Date.now()+4000
    while(doc?.accounted?.[observation.id]!==observation.signature){
      if(closed||Date.now()>end||!doc)throw Error("Budget accounting unavailable; request paused before dispatch")
      if(!doc.enabled||verdict(doc,sid)?.enforced===false)return
      await sleep(40);doc=budget()
    }
  }
  async function enforce(ctx,event,kind){
    if(process.env.OPENCODE_ODOMETER_NOBLOCK==="1")return
    if(!parents.has(event.sessionID))await observe(ctx,event.sessionID)
    await accounted(ctx,event.sessionID)
    const doc=budget(),s=verdict(doc,event.sessionID);if(!doc?.enabled||!s||s.enforced===false)return
    const route=key(event.model)||key(steps.get(event.messageID)?.model)||key(unwrap(await call(signal=>ctx.session.get({sessionID:event.sessionID},{signal}))).model)
    // Switching must halt even a free original request until the transition
    // settles. Once stopped, only a route confirmed free by the app may pass.
    const wrong=s.stage==="switching"||kind==="context"&&s.stage==="fallback"&&route!==s.fallback_model
    if(!wrong&&free(doc,route))return
    if(!wrong&&s.state!=="over")return
    if(kind==="tool")blockedTools.add(`${event.sessionID}/${event.messageID}/${event.id}`)
    throw Error("Budget limit reached. Raise or clear the limit in Odometer to continue.")
  }
  async function choose(ctx,sid){
    const chat=root(sid),file=join(directory(),`switch-session-${chat}.json`),choice=read(file),doc=budget(),s=verdict(doc,chat)
    if(!choice||choice.session_id!==chat||!identifier(choice.id))return
    if(choice.source==="budget"&&(s?.stage!=="fallback"||s.generation!==choice.id))return
    const info=unwrap(await call(signal=>ctx.session.get({sessionID:sid},{signal})))
    if(choice.source==="budget_restore"&&info.model&&key(info.model)!==s?.fallback_model&&key(info.model)!==choice.key)return
    const available=(catalogs.get(ctx.location.directory)||[]).find(m=>m.key===choice.key&&m.category==="chat")
    if(!available)throw Error("Selected model is unavailable in this conversation's location")
    const policy=verdict(doc,sid)
    if(doc?.enabled&&policy?.state==="over"&&policy.enforced!==false&&!free(doc,choice.key))return
    if(key(info.model)!==choice.key){
      if(!routes.has(chat))routes.set(chat,{ctx,selected:choice.key,previous:info.model})
      await call(signal=>ctx.session.switchModel({sessionID:sid,model:model(choice.key)},{signal}))
    }
    write(`switch-status-${choice.id}.json`,{...choice,status:"applied",detail:"Selected in OpenCode; awaiting the next response."})
    if(!choice.persistent){rmSync(file,{force:true});routes.delete(chat)}
  }
  async function prompt(ctx,event){
    await observe(ctx,event.sessionID)
    const doc=budget(),s=verdict(doc,event.sessionID)
    const automatic=event.prompt?.text?.startsWith("[Odometer automatic budget continuation]")
    if(s?.manual_stop&&!automatic&&s.cancellation_id)write(`continue-reset-${s.budget_session_id}.json`,{id:s.budget_session_id,session_id:root(event.sessionID),token:s.cancellation_id})
    if(automatic&&s?.manual_stop)throw Error("Automatic continuation cancelled by STOP")
    await choose(ctx,event.sessionID)
    await enforce(ctx,event,"prompt")
  }
  async function releaseRoutes(){
    for(const [sid,saved] of routes){
      const choice=read(join(directory(),`switch-session-${sid}.json`));if(choice)continue
      routes.delete(sid)
      try{const info=unwrap(await call(signal=>saved.ctx.session.get({sessionID:sid},{signal})));if(saved.previous&&key(info.model)===saved.selected)await call(signal=>saved.ctx.session.switchModel({sessionID:sid,model:saved.previous},{signal}))}catch{}
    }
  }
  function transition(req){
    if(closed)throw Error("OpenCode plugin was unloaded; no automatic retry was sent")
    const doc=budget(),s=verdict(doc,req.session_id)
    if(!doc?.enabled||!s||s.generation!==req.id||s.manual_stop||s.state==="over"&&s.stage==="fallback")throw Error("Automatic continuation cancelled or allowance unavailable")
    return s
  }
  function checkTools(items,sid){
    for(const m of items)for(const p of m.content||[]){
      if(p.type!=="tool")continue
      if(["streaming","running"].includes(p.state?.status))return false
      if(p.state?.status==="error"&&/interrupt|abort|cancel|budget limit/i.test(JSON.stringify(p.state.error))&&!blockedTools.has(`${sid}/${m.id}/${p.id}`))throw Error("An interrupted tool has an uncertain result; verify it before resuming")
    }
    return true
  }
  async function continueTask(ctx,req,tui){
    let ids=[]
    try{
      transition(req);const found=(catalogs.get(ctx.location.directory)||[]).find(m=>m.key===req.key&&m.category==="chat")
      if(!found)throw Error("Configured fallback is unavailable")
      const family=[req.session_id,...(req.children||[])],end=Date.now()+30000
      let history
      while(true){
        transition(req);let settled=true
        for(const sid of family){const items=await messages(ctx,sid);if(sid===req.session_id)history=items;settled=checkTools(items,sid)&&settled}
        if(settled)break
        if(Date.now()>end)throw Error("A tool is still running; automatic continuation paused")
        await sleep(100)
      }
      for(const sid of family){await call(signal=>ctx.session.interrupt({sessionID:sid,resume:false},{signal}));await call(signal=>ctx.session.wait({sessionID:sid},{signal}),10000)}
      for(const sid of family){const items=await messages(ctx,sid);if(sid===req.session_id)history=items;if(!checkTools(items,sid))throw Error("An interrupted tool is unresolved");ids.push(...items.filter(m=>m.tokens).map(m=>m.id))}
      status(req,"prepared","Original turn stopped; activating fallback allowance.",ids)
      while(transition(req).stage!=="fallback"){if(Date.now()>end)throw Error("Fallback allowance did not activate");await sleep(50)}
      const choice={id:req.id,session_id:req.session_id,key:req.key,persistent:true,source:"budget",issued:req.issued}
      write(`switch-session-${req.session_id}.json`,choice)
      await choose(ctx,req.session_id)
      const last=history.findLast(m=>m.type==="assistant"),lastIdle=history.findLast(m=>m.type==="idle")
      if(lastIdle?.outcome==="succeeded"&&last?.finish==="stop") {status(req,"completed","Task already completed; fallback selected for the next message.",ids);return}
      const usage=last?.tokens||{},estimate=Math.max((usage.input||0)+(usage.cache?.read||0)+(usage.cache?.write||0)+(usage.output||0),Buffer.byteLength(JSON.stringify(history)))+2048
      if((req.context||found.context)>0&&estimate+32>(req.context||found.context))throw Error("Conversation exceeds fallback context capacity")
      for(const m of history)for(const f of m.prompt?.files||[]){const uri=String(f.uri||"");const modality=/^data:image|\.(png|jpe?g|webp|gif)$/i.test(uri)?"image":/\.pdf$/i.test(uri)?"pdf":null;if(modality&&!(req.input_modalities||found.input_modalities).includes(modality))throw Error("Fallback does not support a conversation attachment")}
      const allowance=transition(req)
      if(!free(budget(),req.key)&&!(allowance.limit-allowance.cost>(estimate*req.input_price+32*req.output_price)/1e6))throw Error("Fallback allowance cannot cover the estimated next request")
      if(!presence().some(t=>hasConversation(t,req.session_id)))throw Error("The conversation closed in all terminals")
      status(req,"dispatching","Sending one continuation on the selected fallback.",ids)
      transition(req)
      const clock=(BigInt(Date.now())*4096n&((1n<<48n)-1n)).toString(16).padStart(12,"0"),id=`msg_${clock}${req.id.replaceAll("-","").slice(0,14)}`
      await call(signal=>ctx.session.prompt({sessionID:req.session_id,id,text,metadata:{odometer_transition:req.id},resume:true},{signal}),10000)
      const confirmEnd=Date.now()+300000
      while(true){
        const items=await messages(ctx,req.session_id),start=items.findIndex(m=>m.id===id),following=start<0?[]:items.slice(start+1),answer=following.find(m=>m.type==="assistant"&&m.finish)
        if(following.some(m=>m.type==="user"))throw Error("A new user prompt arrived; automatic response correlation is uncertain")
        if(answer?.error)throw Error("Configured fallback request failed; no other provider was tried")
        if(answer){
          const current=verdict(budget(),req.session_id)
          if(closed||current?.manual_stop||current?.generation!==req.id)throw Error("Automatic continuation was cancelled")
          if(key(answer.model)!==req.key)throw Error("OpenCode responded with a different model")
          status(req,"confirmed",`Switched to ${found.name} · continuing.`,ids);break
        }
        transition(req)
        if(Date.now()>confirmEnd)throw Error("Fallback response was not confirmed; no automatic retry was sent")
        await sleep(200)
      }
    }catch(error){status(req,"failed",String(error.message||error),ids)}
  }
  async function drain(){
    if(draining||closed)return;draining=true
    try{
      await releaseRoutes()
      for(const tui of presence()){
        let files=[];try{files=readdirSync(directory())}catch{}
        for(const name of files){
          if(!name.startsWith(`continue-${tui.pid}-`)||!name.endsWith(".json"))continue
          const file=join(directory(),name),req=read(file)
          if(!req||!identifier(req.id)||req.instance!==instance||!hasConversation(tui,req.session_id)||Date.now()/1000-req.issued>600)continue
          const ctx=ownerContext(tui,req.session_id);if(!ctx)continue
          // Persist the claim before any async side effect. A restart must
          // report uncertainty instead of buying a duplicate continuation.
          rmSync(file,{force:true})
          if(tasks.has(req.id)||read(join(directory(),`continue-status-${req.id}.json`)))continue
          status(req,"claimed","Waiting for original work to settle.")
          const task=continueTask(ctx,req,tui).finally(()=>tasks.delete(req.id));tasks.set(req.id,task)
        }
      }
    }finally{draining=false}
  }
  async function events(){
    while(!closed){
      const ctx=contexts.values().next().value;if(!ctx){await sleep(100);continue}
      subscription=new AbortController()
      try{for await(const event of ctx.event.subscribe({signal:subscription.signal})){
        if(closed)break
        if(event.type==="model.updated"||event.type==="provider.updated"){catalogTimes.clear();void refresh()}
        const d=event.data||{},sid=d.sessionID
        if(!identifier(sid))continue
        if(event.type==="session.created"||event.type==="session.forked"){parents.set(sid,d.parentID||"");spool({type:"session",id:sid,parentID:d.parentID||""})}
        if(event.type==="session.step.started")steps.set(d.assistantMessageID,{...d,created:millis(d.started||event.created)})
        if(event.type==="session.step.ended"||event.type==="session.step.failed"){
          let start=steps.get(d.assistantMessageID)
          if(!start){try{const items=await messages(ctx,sid);start=items.find(m=>m.id===d.assistantMessageID)}catch{}}
          if(start&&d.tokens)spool(normalizeUsage({type:"assistant",id:d.assistantMessageID,model:start.model,tokens:d.tokens,cost:d.cost,finish:d.finish||"error",time:{created:start.created||start.time?.created}},sid))
          const choice=read(join(directory(),`switch-session-${root(sid)}.json`))
          if(choice&&start)write(`switch-status-${choice.id}.json`,{...choice,status:key(start.model)===choice.key?"confirmed":"failed",detail:key(start.model)===choice.key?"OpenCode confirmed the selected response model.":"OpenCode responded with a different model."})
        }
        if(/^session\.(compaction\.|execution\.)/.test(event.type)){try{await messages(ctx,sid)}catch{}}
        if(event.type==="session.execution.failed"){
          const s=verdict(budget(),sid)
          if(s?.stage==="fallback"&&!s.manual_stop&&identifier(s.budget_session_id))write(`fallback-error-${s.budget_session_id}.json`,{id:s.budget_session_id,session_id:root(sid),generation:s.generation,key:s.fallback_model,detail:"Configured fallback failed. No other provider was tried."})
        }
      }}catch{}finally{subscription?.abort()}
      if(!closed){backfilled.clear();void refresh();await sleep(500)}
    }
  }
  const timer=setInterval(()=>{void refresh();void drain()},1000)
  const commandTimer=setInterval(()=>void drain(),100)
  void events()
  return{contexts,catalogs,refresh,prompt,enforce,
    close(){closed=true;clearInterval(timer);clearInterval(commandTimer);subscription?.abort();for(const pid of published){try{rmSync(join(directory(),`models-${pid}.json`),{force:true})}catch{}}},
  }
}
