/** @jsxImportSource @opentui/solid */
// Use additive V2 slots. The native composer owns submit, Esc, focus and scroll.
import {createSignal} from "solid-js"
import {createPresence} from "./tui-presence.js"
import {readFileSync,writeFileSync,renameSync,rmSync} from "node:fs"
import {join} from "node:path"
import {normalizeModel} from "./v2-models.js"
import {conversationIdentity,openConversations,hasConversation} from "./v2-sessions.js"

export default {
  id:"opencode-odometer-v2-presence",
  setup(ctx){
    if(process.env.OPENCODE_ODOMETER==="0")return
    const presence=createPresence(),[routing,setRouting]=createSignal(""),[name,setName]=createSignal("New session")
    let disposed=false,stopping=false,serverPID=0
    let draftStarted=Date.now(),lastSession=""
    const read=file=>{try{return JSON.parse(readFileSync(file,"utf8"))}catch{return null}}
    const identity=()=>{
      const route=ctx.ui.router.current()
      const sid=route.type==="session"?ctx.data.session.root(route.sessionID):""
      const session=sid?ctx.data.session.get(sid):null
      const selected=session?.model||ctx.ui.model.current()
      const selectedID=selected?.id||selected?.modelID
      return{sid,session,model:selected?.providerID&&selectedID?`${selected.providerID}/${selectedID}`:""}
    }
    function refresh(){
      if(disposed)return
      const {sid,session,model}=identity(),location=session?.location?.directory||ctx.location?.directory||ctx.data.location.default()?.directory||""
      if(!sid&&lastSession)draftStarted=Date.now()
      lastSession=sid
      const tabs=openConversations(ctx)
      const body=presence.publish({sessionID:sid,model,active:!!sid&&ctx.data.session.status(sid)==="running",serverPID,location,apiMajor:2,continueProtocol:2,tabs,draftStarted})
      setName(sid?conversationIdentity(sid).name:"New session")
      const dir=presence.directory(),choice=dir&&sid?read(join(dir,`switch-session-${sid}.json`)):null
      const budget=dir?read(join(dir,"budget.json")):null,s=budget?.sessions?.[sid]
	  // Make the picker available before the first prompt loads server hooks.
	  // A terminal-only inventory never advertises continuation capability.
	  if(dir&&serverPID){
	    const existing=read(join(dir,`models-${process.pid}.json`))
	    if(!existing?.continue_protocol||existing.server_pid!==serverPID){
	      const models=ctx.data.location.model.list(session?.location||ctx.location||ctx.data.location.default())||[]
	      if(models.length){try{const file=join(dir,`models-${process.pid}.json`),tmp=file+".tui.tmp";writeFileSync(tmp,JSON.stringify({api_major:2,server_pid:serverPID,pid:process.pid,instance:presence.id,updated:Math.floor(Date.now()/1000),switch_protocol:3,continue_protocol:0,sessions:sid?[sid]:[],models:models.filter(m=>m.enabled!==false).map(normalizeModel)}));renameSync(tmp,file)}catch{}}
	    }
	  }
      const valid=choice?.persistent&&choice.session_id===sid&&(choice.source!=="budget"||s?.stage==="fallback"&&s.generation===choice.id)
      const label=valid?choice.key:""
      setRouting(label?` · Routing: ${label}`:"")
      if(body)ctx.renderer.setTerminalTitle(`OC | ${name()}`)
    }
    async function discover(){
      try{const info=await ctx.client.server.info(undefined,{signal:AbortSignal.timeout(2000)});if(!disposed){serverPID=info.pid;refresh()}}catch{}
    }
    async function stop(){
      if(disposed||stopping||!presence.directory())return
      const dir=presence.directory(),file=join(dir,`stop-tui-${presence.id}.json`),req=read(file)
      if(!req)return
      rmSync(file,{force:true})
      if(!/^[a-zA-Z0-9-]+$/.test(req.id||""))return
      stopping=true
      let status="stopped",detail="OpenCode acknowledged cancellation."
      try{
        const sid=req.session_id
        if(req.instance!==presence.id||!hasConversation({tabs:openConversations(ctx)},sid)||!sid||Date.now()/1000-req.issued>30)throw Error("The selected conversation changed or closed")
        const family=new Set([sid,...ctx.data.session.family(sid),...(req.children||[])])
        for(const sessionID of family){
          await ctx.client.session.interrupt({sessionID,resume:false},{signal:AbortSignal.timeout(2000)})
          // V2 keeps queued inputs after interruption. Cancel only our own
          // automatic inputs so a later human prompt cannot revive them.
          const inbox=await ctx.client.session.inbox.list({sessionID},{signal:AbortSignal.timeout(2000)})
          for(const item of inbox)if(item.payload?.metadata?.odometer_transition){
            await ctx.client.session.inbox.cancel({sessionID,inboxID:item.id},{signal:AbortSignal.timeout(2000)})
          }
        }
      }catch(error){status="failed";detail=String(error.message||error)}
      finally{
        try{const out=join(dir,`abort-status-${req.id}.json`),tmp=out+".tmp";writeFileSync(tmp,JSON.stringify({id:req.id,status,detail}));renameSync(tmp,out)}catch{}
        stopping=false
      }
    }
    const removers=[
      ctx.ui.slot({append:"home.footer.status",render:()=> <text fg={ctx.theme.text.muted}>Odometer · New session</text>}),
      ctx.ui.slot({append:"session.composer.top",render:()=> <text fg={ctx.theme.text.muted}>Odometer · {name()}{routing()}</text>}),
    ]
    refresh();void discover();presence.startV2Widget()
    let ticks=0
    const timer=setInterval(()=>{refresh();if(!serverPID||++ticks%15===0)void discover()},1000),stopTimer=setInterval(()=>void stop(),100)
    return()=>{disposed=true;clearInterval(timer);clearInterval(stopTimer);for(const remove of removers)remove();presence.close()}
  },
}
