import {createHash} from "node:crypto"

export function conversationIdentity(sessionID){
  const code=createHash("sha256").update(sessionID).digest("hex").slice(0,16)
  const words=["calm","bright","swift","clear","steady","keen"],animals=["fox","owl","panda","lynx","otter","robin"],n=parseInt(code.slice(0,2),16)
  return{id:`v2-${code}`,name:`${words[n%words.length]}-${animals[Math.floor(n/words.length)%animals.length]}-${code.slice(0,8)}`}
}

export function openConversations(ctx){
  const route=ctx.ui.router.current(),focused=route.type==="session"?ctx.data.session.root(route.sessionID):""
  const tabs=ctx.ui.tabs.list(),ids=new Set(tabs.map(t=>ctx.data.session.root(t.sessionID)))
  if(focused)ids.add(focused)
  return [...ids].filter(Boolean).map(sessionID=>{
    const info=ctx.data.session.get(sessionID),tab=tabs.find(t=>t.sessionID===sessionID),model=info?.model
    return{session_id:sessionID,title:info?.title||tab?.title||"",started:Number(info?.time?.created)||1,
      model:model?.providerID&&model?.id?`${model.providerID}/${model.id}`:"",
      active:ctx.data.session.status(sessionID)==="running",focused:sessionID===focused,location:info?.location?.directory||ctx.location?.directory||""}
  })
}

export function hasConversation(terminal,sessionID){
  return terminal.session_id===sessionID||terminal.tabs?.some(t=>t.session_id===sessionID)===true
}
