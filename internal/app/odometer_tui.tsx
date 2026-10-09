/** @jsxImportSource @opentui/solid */
import {createPresence} from "./tui-presence.js"
import {readFileSync,rmSync,writeFileSync,renameSync} from "node:fs"
import {join} from "node:path"

// This runs in the terminal, including home before any session exists.
// It does not create a conversation or make an inference request.
export default {id:"opencode-odometer-presence",tui:async(api)=>{
  if(process.env.OPENCODE_ODOMETER==="0")return
  const presence=createPresence()
  let current="",root="",busy=false,disposed=false,stopping=false
  function refresh(){
    if(disposed)return
    const route=api.route.current
    const sid=route.name==="session"?route.params?.sessionID||"":""
    if(sid!==current){current=sid;root=sid;if(sid)void ancestry(sid)}
    const messages=sid?api.state.session.messages(sid):[]
    const latest=messages.findLast(m=>m.role==="assistant" || m.role==="user")
    const model=latest?.model || latest
    const key=model?.providerID&&model?.modelID?`${model.providerID}/${model.modelID}`:""
    const status=sid?api.state.session.status(sid):null
    presence.publish({sessionID:root,model:key,active:!!status&&status.type!=="idle"})
    // Reassert on route/session changes; OpenCode's own home/session title
    // effect also runs on those changes. Never patch the OpenCode binary.
    const title=`OC | ${presence.name}`
    api.renderer.setTerminalTitle(title)
  }
  async function ancestry(sid){
    if(busy)return;busy=true
    try{
      let next=sid;const seen=new Set()
      while(next&&!seen.has(next)){
        seen.add(next)
        const response=await api.client.session.get({sessionID:next,signal:AbortSignal.timeout(2000)})
        const parent=response.data?.parentID
        if(!parent)break
        next=parent
      }
      if(!disposed&&current===sid){root=next;refresh()}
    }catch{}finally{busy=false;if(current&&current!==sid)void ancestry(current)}
  }
  async function drainStop(){
    if(stopping||disposed||!presence.directory())return
    const dir=presence.directory(),file=join(dir,`stop-tui-${presence.id}.json`)
    let req
    try{req=JSON.parse(readFileSync(file,"utf8"));rmSync(file,{force:true})}catch{return}
    if(!req?.id || !/^[a-zA-Z0-9-]+$/.test(req.id))return
    stopping=true
    let status="stopped",detail="OpenCode acknowledged cancellation."
    try{
      if(req.instance!==presence.id||req.session_id!==root||!root||Date.now()/1000-req.issued>30)throw Error("The selected conversation changed or closed.")
      for(const sid of new Set([root,...(req.children||[])])){
        const result=await api.client.session.abort({sessionID:sid,signal:AbortSignal.timeout(2000)})
        if(result.error)throw Error("OpenCode rejected cancellation.")
      }
    }catch(e){status="failed";detail=String(e)}
    finally{
      try{const out=join(dir,`abort-status-${req.id}.json`),tmp=out+".tmp";writeFileSync(tmp,JSON.stringify({id:req.id,status,detail}));renameSync(tmp,out)}catch{}
      stopping=false
    }
  }
  api.slots.register({slots:{
    home_bottom:()=> <box width="100%" maxWidth={75} height={2} paddingTop={1} flexShrink={0} alignItems="center">
      <text height={1} fg={api.theme.current.textMuted}>Odometer · {presence.name}</text>
    </box>,
    // Keep the name on a separate row above the native prompt. Putting a
    // long name in its right-hand metadata slot competes with model text.
    session_prompt:(props)=> <box width="100%" flexShrink={0} flexDirection="column" visible={props.visible!==false}>
      <box height={1} flexShrink={0} paddingLeft={1}><text fg={api.theme.current.textMuted}>Odometer · {presence.name}</text></box>
      <api.ui.Prompt sessionID={props.session_id} visible={props.visible} disabled={props.disabled}
        onSubmit={props.on_submit} ref={props.ref}
        right={<api.ui.Slot name="session_prompt_right" session_id={props.session_id} />} />
    </box>,
  }})
  // Initialize synchronously; server discovery must never hold TUI startup.
  refresh()
  const timer=setInterval(refresh,1000)
  const stopTimer=setInterval(()=>void drainStop(),250)
  api.lifecycle.onDispose(()=>{disposed=true;clearInterval(timer);clearInterval(stopTimer);presence.close()})
}}
