import {test} from 'node:test'
import assert from 'node:assert/strict'
import {mkdtempSync,writeFileSync,readFileSync,existsSync,rmSync} from 'node:fs'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import fs from 'node:fs'
import {syncBuiltinESMExports} from 'node:module'

const wait=ms=>new Promise(r=>setTimeout(r,ms))
test('automatic fallback stops, prepares and continues once; manual stop, uncertainty and affordability pause it',async()=>{
 const dir=mkdtempSync(join(tmpdir(),'odo-continue-'))
 Object.assign(process.env,{OPENCODE_ODOMETER_DIR:dir,OPENCODE_ODOMETER_HOME:dir,OPENCODE_ODOMETER_POINTER:join(dir,'missing-pointer.json'),OPENCODE_ODOMETER_NOBLOCK:'0'})
 writeFileSync(join(dir,'preferences.json'),JSON.stringify({auto_start:false}))
 const {OdometerPlugin}=await import('./odometer.js')
 let hooks,scenario='success',prompts=[],aborts=[],pendingStatus=true,continuationAnswer=null
 const messages=()=>[{info:{id:'user',role:'user',agent:'build'},parts:[{type:'text',text:'Continue my work'}]},{info:{id:'original',parentID:'user',role:'assistant',finish:'tool-calls',tokens:{input:10},time:{completed:1}},parts:[{type:'tool',tool:'write',callID:'blocked-call',state:scenario==='uncertain'?{status:'error',error:'Action interrupted; outcome unknown'}:scenario==='guarded'?{status:'error',error:'AbortError: budget rejected before execution'}:{status:'completed',output:'Written once'}}]}]
 const client={
  provider:{list:async()=>({data:{connected:['mock'],all:[{id:'mock',models:{cheap:{name:'Cheap',tool_call:true,cost:{input:.1,output:.4},limit:{context:0}},paid:{name:'Paid',tool_call:true,cost:{input:3,output:15}}}}]}})},
  session:{
   list:async()=>({data:[]}),
   messages:async({path})=>({data:['root','ses_openConversation'].includes(path.id)?[...messages(),...(continuationAnswer?[continuationAnswer]:[])]:[]}),
   abort:async({path})=>{aborts.push(path.id);return{data:true}},
   status:async()=>({data:pendingStatus?(pendingStatus=false,{root:{type:'busy'}}):{}}),
   promptAsync:async args=>{prompts.push(args);continuationAnswer={info:{role:'assistant',id:'fallback',parentID:args.body.messageID,providerID:'mock',modelID:'cheap',finish:'stop'},parts:[]};return{data:undefined}}
  },tui:{showToast:async()=>{}}
 }
 hooks=await OdometerPlugin({client});await wait(50)
 await hooks.event({event:{type:'session.created',properties:{info:{id:'root'}}}})
 await hooks.event({event:{type:'session.created',properties:{info:{id:'child',parentID:'root'}}}})
 const inventory=JSON.parse(readFileSync(join(dir,`models-${process.pid}.json`)))
 assert.equal(inventory.continue_protocol,2)
 const budgetPath=join(dir,'budget.json')
 const status=id=>{try{return JSON.parse(readFileSync(join(dir,`continue-status-${id}.json`)))}catch{return null}}
 const request=(id,limit=.1,context=200000,free=false)=>{
  continuationAnswer=null
  const doc={enabled:true,updated:Date.now()/1000,mode:'hard',block_when_exceeded:true,free_models:free?['mock/cheap']:[],sessions:{root:{strict:true,enforced:true,generation:id,stage:'switching',state:'over',cost:.2,limit,fraction:2,fallback_model:'mock/cheap'}}}
  writeFileSync(budgetPath,JSON.stringify(doc))
  const req={id,session_id:'root',instance:inventory.instance,key:'mock/cheap',children:['child'],issued:Date.now()/1000,input_price:.1,output_price:.4,context,input_modalities:['text']}
  const path=join(dir,`continue-${process.pid}-${id}.json`);writeFileSync(path,JSON.stringify(req));return{doc,req,path}
 }
 const finish=async(id,doc,manual=false)=>{
  const end=Date.now()+6000;let armed=false
  while(Date.now()<end){const s=status(id);if(s?.status==='prepared'&&!armed){armed=true;assert.ok(s.original_ids.includes('original'));doc.sessions.root.stage='fallback';doc.sessions.root.cost=0;doc.sessions.root.state='ok';if(manual){doc.sessions.root.manual_stop=true;doc.sessions.root.generation=''};writeFileSync(budgetPath,JSON.stringify(doc))}
   if(['confirmed','failed','completed'].includes(s?.status))return s;await wait(20)
  }throw Error('continuation timed out: '+JSON.stringify(status(id)))
 }
 const rename=fs.renameSync;let transientWrites=2;
 fs.renameSync=(from,to)=>{if(String(to).includes('continue-status-success.json')&&transientWrites-->0){const error=Error('test sharing violation');error.code='EPERM';throw error}return rename(from,to)};syncBuiltinESMExports()
 try{
  let r=request('success');const done=await finish('success',r.doc);assert.equal(done.status,'confirmed');assert.equal(prompts.length,1);assert.deepEqual(aborts,['root','child'])
  assert.equal(prompts[0].path.id,'root');assert.deepEqual(prompts[0].body.model,{providerID:'mock',modelID:'cheap'});assert.match(prompts[0].body.parts[0].text,/preserve completed work/i)
  assert.match(prompts[0].body.parts[0].text,/does not change the requested scope, depth, deliverables, or completion criteria/)
  assert.match(prompts[0].body.parts[0].text,/do not conclude early or replace unfinished work with a high-level summary/)
  assert.match(prompts[0].body.messageID,/^msg_[a-f0-9]{12}[a-zA-Z0-9]{14}$/)
  writeFileSync(r.path,JSON.stringify(r.req));await wait(1100);assert.equal(prompts.length,1,'duplicate automatic prompt');assert.equal(existsSync(r.path),false)
  r=request('manual');assert.equal((await finish('manual',r.doc,true)).status,'failed');assert.equal(prompts.length,1,'manual stop ignored')
  scenario='uncertain';r=request('uncertain');const uncertain=await finish('uncertain',r.doc);assert.equal(uncertain.status,'failed');assert.match(uncertain.detail,/uncertain/);assert.equal(prompts.length,1)
  scenario='success';r=request('unaffordable',.000000001);const poor=await finish('unaffordable',r.doc);assert.equal(poor.status,'failed');assert.match(poor.detail,/cannot cover/);assert.equal(prompts.length,1)
  // Every model step is enforced, including steps inside an already approved
  // user turn; synchronise the preceding usage fingerprint before dispatch.
  const doc={enabled:true,updated:Date.now()/1000,mode:'hard',block_when_exceeded:true,accounted:{original:'10/0/0/0/0/tool-calls'},sessions:{root:{strict:true,enforced:true,stage:'original',state:'ok',cost:0,limit:.1,fraction:0}}}
  writeFileSync(budgetPath,JSON.stringify(doc))
  await hooks.event({event:{type:'message.updated',properties:{info:{id:'original',sessionID:'root',role:'assistant',finish:'tool-calls',tokens:{input:10}}}}})
  const input={sessionID:'root',model:{providerID:'mock',id:'paid'},message:{id:'user'}}
  await hooks['chat.message']({sessionID:'root'},{message:{id:'user',model:{providerID:'mock',modelID:'paid'}}})
  await hooks['chat.params'](input);doc.sessions.root.state='over';doc.sessions.root.cost=.2;doc.sessions.root.fraction=2;writeFileSync(budgetPath,JSON.stringify(doc))
  await assert.rejects(hooks['chat.params'](input),{name:'AbortError'})
  await assert.rejects(hooks['tool.execute.before']({sessionID:'root',tool:'write',callID:'blocked-call'}),{name:'AbortError'})
  scenario='guarded';r=request('guarded');assert.equal((await finish('guarded',r.doc)).status,'confirmed','a tool rejected before execution was treated as an uncertain side effect');assert.equal(prompts.length,2)
  // STOP suppresses only the cancelled task. An explicit later user message
  // releases it, while the automatic continuation marker must never do so.
  const cancelled={enabled:true,updated:Date.now()/1000,mode:'hard',block_when_exceeded:true,accounted:{original:'10/0/0/0/0/tool-calls'},sessions:{root:{budget_session_id:'one',strict:true,enforced:true,stage:'original',state:'ok',cost:.02,limit:.1,manual_stop:true,cancellation_id:'cancel-one'}}}
  writeFileSync(budgetPath,JSON.stringify(cancelled))
  const resetPath=join(dir,'continue-reset-one.json')
  await hooks['chat.message']({sessionID:'root'},{message:{id:'automatic',model:{providerID:'mock',modelID:'paid'}},parts:[{type:'text',text:'[Odometer automatic budget continuation] Continue'}]})
  assert.equal(existsSync(resetPath),false)
  await hooks['chat.message']({sessionID:'root'},{message:{id:'explicit-user',model:{providerID:'mock',modelID:'paid'}},parts:[{type:'text',text:'Continue please'}]})
  assert.deepEqual(JSON.parse(readFileSync(resetPath)),{id:'one',session_id:'root',token:'cancel-one'})
  await hooks['chat.params']({sessionID:'root',model:{providerID:'mock',id:'paid'},message:{id:'explicit-user'}})
  // Later fallback provider failures stop the configured route, with no
  // alternate provider or additional continuation. User cancellation is benign.
  Object.assign(cancelled.sessions.root,{stage:'fallback',generation:'later-failure',fallback_model:'mock/cheap',manual_stop:false});writeFileSync(budgetPath,JSON.stringify(cancelled))
  await hooks.event({event:{type:'message.updated',properties:{info:{role:'assistant',sessionID:'root',id:'later',providerID:'mock',modelID:'cheap',error:{name:'MessageAbortedError'}}}}})
  const failurePath=join(dir,'fallback-error-one.json')
  assert.equal(existsSync(failurePath),false)
  await hooks.event({event:{type:'session.error',properties:{sessionID:'root',error:{name:'APIError',data:{statusCode:429,message:'rate limit'}}}}})
  assert.match(JSON.parse(readFileSync(failurePath)).detail,/mock\/cheap.*rate limit.*No other provider/);assert.equal(prompts.length,2)
  cancelled.sessions.root.generation='';writeFileSync(budgetPath,JSON.stringify(cancelled))
  await hooks.event({event:{type:'session.error',properties:{sessionID:'root',error:{name:'ProviderAuthError'}}}})
  assert.equal(JSON.parse(readFileSync(failurePath)).generation,'');assert.match(JSON.parse(readFileSync(failurePath)).detail,/authentication failed/)
  // Missing context metadata must not turn a pre-authorized automatic switch
  // into a manual pause. Both paid and free routes continue exactly once.
  scenario='success';r=request('unknown-context-paid',.1,0);assert.equal((await finish('unknown-context-paid',r.doc)).status,'confirmed');assert.equal(prompts.length,3)
  r=request('unknown-context-free',0,0,true);assert.equal((await finish('unknown-context-free',r.doc)).status,'confirmed');assert.equal(prompts.length,4)
  r=request('known-too-small',.1,64);const small=await finish('known-too-small',r.doc);assert.equal(small.status,'failed');assert.match(small.detail,/exceeds.*context/);assert.equal(prompts.length,4)
  // Opening a saved conversation is stronger ownership evidence than the
  // bounded history cache. Pin it even after >100 unrelated chat events.
  const openID='ses_openConversation',presence=join(dir,'tui-open.json')
  writeFileSync(presence,JSON.stringify({id:'open',pid:process.pid,session_id:openID,closed:false}))
  await wait(1100)
  for(let i=0;i<110;i++)await hooks.event({event:{type:'session.created',properties:{info:{id:`history-${i}`}}}})
  await wait(1100)
  assert.ok(JSON.parse(readFileSync(join(dir,`models-${process.pid}.json`))).sessions.includes(openID),'open TUI chat evicted by history')
  const reopened=request('reopened');reopened.req.session_id=openID;reopened.req.children=[]
  reopened.doc.sessions[openID]=reopened.doc.sessions.root
  writeFileSync(budgetPath,JSON.stringify(reopened.doc));writeFileSync(reopened.path,JSON.stringify(reopened.req))
  const end=Date.now()+6000;let reopenedStatus
  while(Date.now()<end){
   reopenedStatus=status('reopened')
   if(reopenedStatus?.status==='prepared'){Object.assign(reopened.doc.sessions[openID],{stage:'fallback',state:'ok',cost:0});writeFileSync(budgetPath,JSON.stringify(reopened.doc))}
   if(['failed','confirmed','completed'].includes(reopenedStatus?.status))break
   await wait(20)
  }
  assert.equal(reopenedStatus?.status,'confirmed',JSON.stringify(reopenedStatus));assert.equal(prompts.length,5)
 }finally{fs.renameSync=rename;syncBuiltinESMExports();rmSync(dir,{recursive:true,force:true})}
})
