import {test} from 'node:test'
import assert from 'node:assert/strict'
import {mkdtempSync,writeFileSync,readFileSync,existsSync,rmSync,renameSync} from 'node:fs'
import {tmpdir} from 'node:os'
import {join} from 'node:path'

test('Stop wakes without events, acknowledges SDK errors and only cancels the chosen chat tree',async()=>{
 const dir=mkdtempSync(join(tmpdir(),'odo-stop-'))
 Object.assign(process.env,{OPENCODE_ODOMETER_DIR:dir,OPENCODE_ODOMETER_HOME:dir,OPENCODE_ODOMETER_POINTER:join(dir,'pointer.json'),OPENCODE_ODOMETER_NOBLOCK:'1'})
 writeFileSync(join(dir,'preferences.json'),JSON.stringify({auto_start:false}))
 const {OdometerPlugin}=await import('./odometer.js')
 const aborted=[]
 let hooks,fail=false
 const client={provider:{list:async()=>({data:{connected:[],all:[]}})},session:{list:async()=>({data:[]}),abort:async({path})=>{
  aborted.push(path.id)
  // Cancellation emits an idle event before resolving. Awaiting abort from
  // that event hook used to allow a re-entrant lifecycle deadlock.
  await hooks.event({event:{type:'session.idle',properties:{sessionID:path.id}}})
  return fail ? {error:{message:'rejected'}} : {data:true}
 }},tui:{showToast:async()=>{}}}
 hooks=await OdometerPlugin({client})
 await hooks.event({event:{type:'session.created',properties:{info:{id:'root'}}}})
 await hooks.event({event:{type:'session.created',properties:{info:{id:'child',parentID:'root'}}}})
 await hooks.event({event:{type:'session.created',properties:{info:{id:'other'}}}})
 const inv=JSON.parse(readFileSync(join(dir,`models-${process.pid}.json`)))
 assert.equal(inv.abort_protocol,1)
 assert.ok(inv.sessions.includes('root') && inv.sessions.includes('child'),'new chats must be stoppable before the next heartbeat')
 const queue=(id,pid=process.pid,issued=Date.now()/1000)=>{
  const path=join(dir,`abort-${pid}-${id}.json`),tmp=path+'.tmp'
  writeFileSync(tmp,JSON.stringify({id,instance:inv.instance,session_id:'root',issued}));renameSync(tmp,path)
  return path
 }
 const status=async id=>{
  const path=join(dir,`abort-status-${id}.json`),until=Date.now()+2500
  while(!existsSync(path)&&Date.now()<until)await new Promise(r=>setTimeout(r,10))
  assert.ok(existsSync(path),'OpenCode did not acknowledge '+id)
  return JSON.parse(readFileSync(path))
 }
 try{
  const unrelated=queue('wrong-instance',process.pid+10000)
  queue('first')
  assert.equal((await status('first')).status,'stopped')
  assert.deepEqual(aborted.sort(),['child','root'])
  assert.ok(existsSync(unrelated),'another process consumed the stop request')
  fail=true;queue('rejected')
  assert.equal((await status('rejected')).status,'failed','a resolved SDK error was reported as success')
  fail=false;queue('second');queue('third')
  assert.equal((await status('second')).status,'stopped');assert.equal((await status('third')).status,'stopped')
  const before=aborted.length;queue('expired',process.pid,Date.now()/1000-60)
  assert.equal((await status('expired')).status,'failed');assert.equal(aborted.length,before)
 }finally{rmSync(dir,{recursive:true,force:true})}
})
