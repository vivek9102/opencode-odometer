import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync,writeFileSync,readFileSync,existsSync,rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

test('chat model routing persists choices and preserves nested IDs, budgets and confirmations', async () => {
 const dir=mkdtempSync(join(tmpdir(),'odo-plugin-'))
 process.env.OPENCODE_ODOMETER_DIR=dir
 process.env.OPENCODE_ODOMETER_HOME=dir
 process.env.OPENCODE_ODOMETER_POINTER=join(dir,'pointer.json')
 process.env.OPENCODE_ODOMETER_NOBLOCK='1'
 writeFileSync(join(dir,'preferences.json'),JSON.stringify({auto_start:false}))
 const {OdometerPlugin}=await import('./odometer.js')
 const messages=[]
 const client={provider:{list:async()=>({data:{connected:['custom'],all:[{id:'custom',models:{'google/gemma-4':{name:'Gemma',cost:{input:.1,output:.2},tool_call:true},'text-embed':{cost:{input:.01,output:0},tool_call:false}}}]}})},session:{list:async()=>({data:[]}),get:async()=>({data:{id:'s1'}})},tui:{showToast:async x=>messages.push(x)}}
 const hooks=await OdometerPlugin({client})
 await new Promise(r=>setTimeout(r,30))
 const command=(id,key,sid='s1')=>writeFileSync(join(dir,`switch-${process.pid}.json`),JSON.stringify({id,key,session_id:sid,issued:Date.now()/1000}))
 const status=id=>JSON.parse(readFileSync(join(dir,`switch-status-${id}.json`),'utf8'))
 try {
  assert.ok(existsSync(join(dir,`models-${process.pid}.json`)))
  command('req1','custom/google/gemma-4')
  const output={message:{id:'user-1',model:{providerID:'old',modelID:'expensive'},variant:'thinking'}}
  await hooks['chat.message']({sessionID:'s1'},output)
  assert.deepEqual(output.message.model,{providerID:'custom',modelID:'google/gemma-4'})
  assert.equal(output.message.variant,undefined)
  assert.equal(status('req1').status,'applied')
  await hooks.event({event:{type:'message.updated',properties:{info:{id:'a0',parentID:'older-user',role:'assistant',sessionID:'s1',providerID:'custom',modelID:'google/gemma-4',tokens:{}}}}})
  assert.equal(status('req1').status,'applied','an older response must not confirm the switch')
  await hooks.event({event:{type:'message.updated',properties:{info:{id:'a1',parentID:'user-1',role:'assistant',sessionID:'s1',providerID:'custom',modelID:'google/gemma-4',tokens:{},cost:.2,finish:'stop'}}}})
  assert.equal(status('req1').status,'confirmed')
  const manual={message:{id:'user-2',model:{providerID:'manual',modelID:'choice'}}}
  await hooks['chat.message']({sessionID:'s1'},manual)
  assert.equal(manual.message.model.providerID,'manual','one-shot routing must not overwrite later manual selections')
  // A chat-specific selection survives a plugin restart and is consumed only
  // by a message in that chat, even if it was evicted from ownership history.
  const durablePath=join(dir,'switch-session-new-chat.json')
  writeFileSync(durablePath,JSON.stringify({id:'durable1',key:'custom/google/gemma-4',session_id:'new-chat',persistent:true,issued:Date.now()/1000-3600}))
  const restarted=await OdometerPlugin({client})
  await new Promise(r=>setTimeout(r,30))
  const elsewhere={message:{id:'elsewhere',model:{providerID:'manual',modelID:'choice'}}}
  await restarted['chat.message']({sessionID:'other-chat'},elsewhere)
  assert.equal(elsewhere.message.model.providerID,'manual')
  assert.ok(existsSync(durablePath),'another chat consumed the selection')
  const next={message:{id:'new-chat-user',model:{providerID:'old',modelID:'expensive'},variant:'thinking'}}
  await restarted['chat.message']({sessionID:'new-chat'},next)
  assert.deepEqual(next.message.model,{providerID:'custom',modelID:'google/gemma-4'})
  assert.equal(next.message.variant,undefined)
  assert.equal(existsSync(durablePath),true,'a persistent chat choice must remain after the first turn')
  assert.equal(status('durable1').status,'applied')
  await restarted.event({event:{type:'message.updated',properties:{info:{id:'new-chat-response',parentID:'new-chat-user',role:'assistant',sessionID:'new-chat',providerID:'custom',modelID:'google/gemma-4',tokens:{}}}}})
  assert.equal(status('durable1').status,'confirmed')
  const later={message:{id:'later',model:{providerID:'manual',modelID:'choice'}}}
  await hooks['chat.message']({sessionID:'new-chat'},later)
  assert.deepEqual(later.message.model,{providerID:'custom',modelID:'google/gemma-4'},'the next turn reverted to its default model')
  rmSync(durablePath)
  const following={message:{id:'follow-opencode',model:{providerID:'manual',modelID:'choice'}}}
  await restarted['chat.message']({sessionID:'new-chat'},following)
  assert.equal(following.message.model.providerID,'manual','clearing a chat choice must restore OpenCode selection')
  // Released/stale budget generations cannot override explicit native choices.
  writeFileSync(durablePath,JSON.stringify({id:'old-budget',key:'custom/google/gemma-4',session_id:'new-chat',persistent:true,source:'budget'}))
  writeFileSync(join(dir,'budget.json'),JSON.stringify({enabled:true,updated:Date.now()/1000,sessions:{'new-chat':{stage:'original',state:'ok',cost:0,limit:1}}}))
  for(let i=0;i<2;i++){
    const output={message:{id:'native-'+i,model:{providerID:'manual',modelID:'choice'}}}
    await restarted['chat.message']({sessionID:'new-chat'},output)
    assert.equal(output.message.model.providerID,'manual','released budget still pins the model')
  }
  assert.equal(existsSync(durablePath),false)
  writeFileSync(durablePath,JSON.stringify({id:'restored',key:'custom/google/gemma-4',session_id:'new-chat',persistent:false,source:'budget_restore'}))
  const native={message:{id:'native-restore',model:{providerID:'manual',modelID:'choice'}}}
  await restarted['chat.message']({sessionID:'new-chat'},native)
  assert.equal(native.message.model.providerID,'manual','one-shot restoration overrides native picker')
  assert.equal(existsSync(durablePath),false)
  // Deliberate Odometer picks stay pinned even when the native picker differs.
  writeFileSync(durablePath,JSON.stringify({id:'manual-pin',key:'custom/google/gemma-4',session_id:'new-chat',persistent:true,source:'manual'}))
  const pinned={message:{id:'pinned-native',model:{providerID:'manual',modelID:'choice'}}}
  await restarted['chat.message']({sessionID:'new-chat'},pinned)
  assert.equal(pinned.message.model.providerID,'custom')
  rmSync(durablePath)
  // Pausing enforcement is not releasing the configured fallback route.
  writeFileSync(durablePath,JSON.stringify({id:'paused-budget',key:'custom/google/gemma-4',session_id:'new-chat',persistent:true,source:'budget'}))
  writeFileSync(join(dir,'budget.json'),JSON.stringify({enabled:false,updated:Date.now()/1000,sessions:{'new-chat':{stage:'fallback',generation:'paused-budget',state:'ok',cost:0,limit:1}}}))
  const pausedRoute={message:{id:'paused-route',model:{providerID:'manual',modelID:'choice'}}}
  await restarted['chat.message']({sessionID:'new-chat'},pausedRoute)
  assert.equal(pausedRoute.message.model.providerID,'custom','pausing enforcement released the fallback route')
  rmSync(durablePath)
  writeFileSync(durablePath,JSON.stringify({id:'unavailable',key:'custom/removed',session_id:'new-chat',issued:Date.now()/1000}))
  await assert.rejects(restarted['chat.message']({sessionID:'new-chat'},{message:{id:'invalid-selection',model:{providerID:'old',modelID:'expensive'}}}),{name:'MessageAbortedError'})
  assert.equal(status('unavailable').status,'failed','an unavailable choice must not silently use the expensive default')
  command('req2','custom/not-configured')
  await hooks.event({event:{type:'session.idle',properties:{sessionID:'s1'}}});await new Promise(r=>setTimeout(r,30))
  assert.equal(status('req2').status,'failed')
  command('req3','custom/text-embed')
  await hooks.event({event:{type:'session.idle',properties:{sessionID:'s1'}}});await new Promise(r=>setTimeout(r,30))
  assert.equal(status('req3').status,'failed','specialised models remain listed but cannot route a coding chat')
  process.env.OPENCODE_ODOMETER_NOBLOCK='0'
  const budgetRequest=async(sid,output)=>{
    await hooks['chat.message']({sessionID:sid},output)
    const model=output.message.model || {}
    await hooks['chat.params']({sessionID:sid,message:output.message,model:{providerID:model.providerID,id:model.modelID}})
  }
  command('req4','custom/google/gemma-4')
  writeFileSync(join(dir,'budget.json'),JSON.stringify({enabled:true,updated:Date.now()/1000,mode:'hard',sessions:{s1:{state:'over',enforced:true,cost:2,limit:1,fraction:2}}}))
  await assert.rejects(budgetRequest('s1',{message:{id:'blocked',model:{providerID:'old',modelID:'expensive'}}}),{name:'AbortError'})
  assert.notEqual(status('req4').status,'applied','a blocked prompt must not consume its model choice')
  writeFileSync(join(dir,'switch-session-s1.json'),JSON.stringify({id:'blocked-durable',key:'custom/google/gemma-4',session_id:'s1',issued:Date.now()/1000}))
  await assert.rejects(budgetRequest('s1',{message:{id:'blocked-again'}}),{name:'AbortError'})
  assert.ok(existsSync(join(dir,'switch-session-s1.json')),'budget enforcement consumed a durable selection')
  await hooks.event({event:{type:'session.created',properties:{info:{id:'child',parentID:'s1'}}}})
  await assert.rejects(hooks['tool.execute.before']({sessionID:'child'}),'a new child must inherit the exhausted root cap before the next odometer poll')
  await assert.rejects(budgetRequest('child',{message:{id:'child-blocked'}}),{name:'AbortError'})
  const spool=readFileSync(join(dir,'events.jsonl'),'utf8').trim().split('\n').map(JSON.parse)
  assert.ok(spool.some(r=>r.type==='session'&&r.id==='child'&&r.parentID==='s1'))
  writeFileSync(join(dir,'budget.json'),JSON.stringify({enabled:true,updated:Date.now()/1000,mode:'soft',sessions:{s1:{budget_session_id:'s1',state:'over',enforced:true,cost:1.1,limit:1,fraction:1.1,grace_remaining:1}}}))
  const grace={message:{id:'one-grace',model:{providerID:'custom',modelID:'google/gemma-4'}}}
  await budgetRequest('child',grace)
  await hooks['chat.params']({sessionID:'child',message:grace.message,model:{providerID:'custom',id:'google/gemma-4'}})
  assert.deepEqual(JSON.parse(readFileSync(join(dir,'grace_claims.json'),'utf8')),{s1:1})
  await assert.rejects(budgetRequest('child',{message:{id:'extra-grace'}}),{name:'AbortError'},'an unacknowledged grace claim must not be repeated')
 } finally {rmSync(dir,{recursive:true,force:true})}
})
