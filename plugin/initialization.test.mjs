import {test} from 'node:test'
import assert from 'node:assert/strict'
import {mkdtempSync,writeFileSync,rmSync} from 'node:fs'
import {tmpdir} from 'node:os'
import {join} from 'node:path'

test('plugin initialization returns hooks while provider API waits for initialization',async()=>{
 const dir=mkdtempSync(join(tmpdir(),'odo-init-'))
 process.env.OPENCODE_ODOMETER_DIR=dir
 process.env.OPENCODE_ODOMETER_HOME=dir
 process.env.OPENCODE_ODOMETER_POINTER=join(dir,'pointer.json')
 writeFileSync(join(dir,'preferences.json'),JSON.stringify({auto_start:false}))
 const {OdometerPlugin}=await import('./odometer.js')
 const never=new Promise(()=>{})
 try {
  const hook=await Promise.race([OdometerPlugin({client:{provider:{list:()=>never},session:{list:()=>never}}}),new Promise((_,reject)=>setTimeout(()=>reject(Error('Plugin blocked OpenCode initialization')),300))])
  assert.equal(typeof hook['chat.message'],'function')
  await hook.event({event:{type:'session.idle',properties:{sessionID:'s1'}}})
 }finally{rmSync(dir,{recursive:true,force:true})}
})
