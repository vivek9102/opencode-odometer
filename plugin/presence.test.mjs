import {test} from 'node:test'
import assert from 'node:assert/strict'
import {mkdtempSync,readFileSync,rmSync} from 'node:fs'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import {createPresence} from './tui-presence.js'

test('each open TUI gets a readable startup identity, a heartbeat and a close tombstone',()=>{
 const dir=mkdtempSync(join(tmpdir(),'odo-presence-'))
 process.env.OPENCODE_ODOMETER_DIR=dir
 process.env.OPENCODE_ODOMETER_POINTER=join(dir,'missing-pointer.json')
 try{
  const first=createPresence('/example/migration-db'),second=createPresence('/example/migration-db')
  assert.notEqual(first.id,second.id);assert.notEqual(first.name,second.name)
  assert.match(first.name,/^[a-z]+-[a-z]+-[a-f0-9]{8}$/)
  assert.equal(first.name.includes('migration-db'),false,'folder name leaked into the session name')
  const home=first.publish();assert.equal(home.session_id,'');assert.equal(home.closed,false)
  second.publish({sessionID:'other',model:'mock/paid'})
  first.publish({sessionID:'selected',model:'mock/free',active:true})
  let row=JSON.parse(readFileSync(join(dir,`tui-${first.id}.json`)))
  assert.equal(row.name,home.name);assert.equal(row.started,home.started);assert.equal(row.active,true)
  first.close();row=JSON.parse(readFileSync(join(dir,`tui-${first.id}.json`)));assert.equal(row.closed,true)
  assert.equal(JSON.parse(readFileSync(join(dir,`tui-${second.id}.json`))).closed,false)
 }finally{rmSync(dir,{recursive:true,force:true})}
})
