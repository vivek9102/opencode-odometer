import {test} from 'node:test'
import assert from 'node:assert/strict'
import childProcess from 'node:child_process'
import {syncBuiltinESMExports} from 'node:module'
import {mkdtempSync,writeFileSync,rmSync} from 'node:fs'
import {join} from 'node:path'
import {tmpdir} from 'node:os'

test('startup off suppresses boot, session and compaction relaunch; live preference is re-read',async()=>{
 const dir=mkdtempSync(join(tmpdir(),'odo-startup-'))
 process.env.OPENCODE_ODOMETER_DIR=dir;process.env.OPENCODE_ODOMETER_HOME=dir
 process.env.OPENCODE_ODOMETER_POINTER=join(dir,'pointer.json')
 process.env.TMPDIR=dir;process.env.TEMP=dir;process.env.TMP=dir
 const exe=join(dir,'Odometer.exe');writeFileSync(exe,'test-only spawn is mocked')
 process.env.OPENCODE_ODOMETER_EXE=exe
 let spawns=0,args=[]
 const original=childProcess.spawn
 childProcess.spawn=(cmd,a)=>{assert.equal(cmd,exe);spawns++;args=a;return {unref(){}}}
 syncBuiltinESMExports()
 const prefs=value=>writeFileSync(join(dir,'preferences.json'),JSON.stringify({auto_start:value}))
 const client={provider:{list:async()=>({data:{all:[],connected:[]}})},session:{list:async()=>({data:[]})}}
 prefs(false)
 const {OdometerPlugin}=await import('./odometer.js')
 const hook=await OdometerPlugin({client})
 const event=type=>hook.event({event:{type,properties:{}}})
 try {
  assert.equal(spawns,0)
  await event('session.created');await event('session.compacted');await event('experimental.session.compacting')
  assert.equal(spawns,0,'disabled startup must not relaunch on background events')
  prefs(true);await event('session.created');assert.equal(spawns,1);assert.deepEqual(args,['--autostart'])
  prefs(false);await event('session.compacted');assert.equal(spawns,1)
 }finally{childProcess.spawn=original;syncBuiltinESMExports();rmSync(dir,{recursive:true,force:true})}
})
