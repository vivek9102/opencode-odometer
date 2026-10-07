import {test} from 'node:test'
import assert from 'node:assert/strict'
import {mkdtempSync,writeFileSync,readFileSync,existsSync,rmSync} from 'node:fs'
import {join} from 'node:path'
import {tmpdir} from 'node:os'

test('exhausted budgets allow confirmed free turns and tools without unblocking paid work or spending grace',async()=>{
 const dir=mkdtempSync(join(tmpdir(),'odo-free-cap-'))
 Object.assign(process.env,{OPENCODE_ODOMETER_DIR:dir,OPENCODE_ODOMETER_HOME:dir,OPENCODE_ODOMETER_POINTER:join(dir,'pointer.json'),OPENCODE_ODOMETER_NOBLOCK:'0'})
 writeFileSync(join(dir,'preferences.json'),JSON.stringify({auto_start:false}))
 const {OdometerPlugin}=await import('./odometer.js')
 const notifications=[]
 const client={provider:{list:async()=>({data:{connected:['custom'],all:[{id:'custom',models:{paid:{cost:{input:1,output:2}},approved:{cost:{input:0,output:0}},unknown:{cost:{input:0,output:0}}}}]}})},session:{list:async()=>({data:[]}),abort:async()=>({data:true})},tui:{showToast:async({body})=>notifications.push(body)}}
 const hooks=await OdometerPlugin({client});await new Promise(r=>setTimeout(r,30))
 const budget={enabled:true,updated:Date.now()/1000,mode:'hard',free_models:['custom/approved'],sessions:{root:{state:'over',enforced:true,cost:2,limit:1,fraction:2,past_hard_stop:true}}}
 const saveBudget=()=>writeFileSync(join(dir,'budget.json'),JSON.stringify(budget))
 saveBudget()
 const choice=(key,persistent=true)=>writeFileSync(join(dir,'switch-session-root.json'),JSON.stringify({id:'pick-'+key,session_id:'root',key:'custom/'+key,persistent,issued:Date.now()/1000}))
 const assistant=(sid,key,parentID)=>hooks.event({event:{type:'message.updated',properties:{info:{id:'response-'+key,sessionID:sid,parentID,role:'assistant',providerID:'custom',modelID:key,tokens:{}}}}})
 const message=(sid,key,id)=>({message:{id,model:{providerID:'custom',modelID:key}}})
 const request=async(sid,output)=>{
  // Saving the prompt must succeed; refusal belongs to the handled stream.
  await hooks['chat.message']({sessionID:sid},output)
  const model=output.message.model
  await assistant(sid,model.modelID,output.message.id)
  await hooks['chat.params']({sessionID:sid,message:output.message,model:{providerID:model.providerID,id:model.modelID}})
 }
 try{
  await assistant('root','paid','old-user');choice('approved')
  await assert.rejects(hooks['tool.execute.before']({sessionID:'root'}),'a future free choice unblocked the running paid turn')
  const first=message('root','paid','user-free');await request('root',first)
  assert.equal(first.message.model.modelID,'approved')
  await hooks['tool.execute.before']({sessionID:'root'})
  await assistant('root','paid','older-user')
  await hooks['tool.execute.before']({sessionID:'root'})
  await assistant('root','paid','user-free')
  await assert.rejects(hooks['chat.params']({sessionID:'root',message:first.message,model:{providerID:'custom',id:'paid'}}),{name:'AbortError'},'a paid fallback inherited the approved free request')
  await assert.rejects(hooks['tool.execute.before']({sessionID:'root'}),'the actual paid response inherited a free exemption')
  await assistant('root','approved','user-free');await hooks['tool.execute.before']({sessionID:'root'})
  budget.mode='soft';budget.sessions.root.grace_remaining=1;saveBudget()
  await request('root',message('root','paid','second-free'))
  assert.equal(existsSync(join(dir,'grace_claims.json')),false,'free continuation consumed grace')
  await hooks.event({event:{type:'session.created',properties:{info:{id:'child',parentID:'root'}}}})
  await assert.rejects(request('child',message('child','paid','child-paid')),{name:'AbortError'},'root free selection unblocked a paid child')
  await request('child',message('child','approved','child-free'))
  await hooks['tool.execute.before']({sessionID:'child'})
  await assistant('child','paid','child-free');await assert.rejects(hooks['tool.execute.before']({sessionID:'child'}))
  choice('paid',false)
  const before=notifications.length
  await assert.rejects(request('root',message('root','approved','back-paid')),{name:'AbortError'})
  assert.equal(notifications.length,before+1,'switching back to paid did not show the limit snackbar')
  await assert.rejects(request('root',message('root','approved','back-paid-again')),{name:'AbortError'})
  assert.equal(notifications.length,before+2,'a later refused attempt suppressed the limit snackbar')
  assert.ok(existsSync(join(dir,'switch-session-root.json')),'blocked paid one-shot choice was consumed')
  choice('unknown')
  await assert.rejects(request('root',message('root','approved','unknown-turn')),{name:'AbortError'},'zero SDK rates incorrectly counted as confirmed free')
  rmSync(join(dir,'switch-session-root.json'))
  await request('root',message('root','approved','manual-free'))
  budget.free_models=[];saveBudget()
  await assert.rejects(hooks['tool.execute.before']({sessionID:'root'}),'removing the free classification did not restore enforcement')
  const after=JSON.parse(readFileSync(join(dir,'budget.json')))
  assert.equal(after.enabled,true);assert.equal(after.sessions.root.cost,2);assert.equal(after.sessions.root.limit,1)
 }finally{rmSync(dir,{recursive:true,force:true})}
})
