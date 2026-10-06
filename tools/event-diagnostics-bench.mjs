import {readFileSync,writeFileSync,mkdirSync,existsSync,statSync} from 'node:fs'
import {resolve,join} from 'node:path'
import {performance} from 'node:perf_hooks'
import assert from 'node:assert/strict'
const dir=resolve('build/event-diagnostics-bench-'+Date.now());mkdirSync(dir,{recursive:true})
const original=readFileSync(process.env.OPENCODE_DIAGNOSTICS_BASELINE,'utf8')
 .replace('const LOG_FILE = join(process.env.HOME || process.env.USERPROFILE || ".", ".opencode", "test-events.log")','const LOG_FILE = '+JSON.stringify(join(dir,'original.log')))
 .replace('mkdirSync(join(process.env.HOME || process.env.USERPROFILE || ".", ".opencode"), { recursive: true })','mkdirSync('+JSON.stringify(dir)+', {recursive:true})')
writeFileSync(join(dir,'original.mjs'),original)
const baseline=await import('file:///'+join(dir,'original.mjs').replaceAll('\\','/'))
const optimized=await import('../plugin/event-diagnostics.js')
const records=[]
for(let i=0;i<1200;i++)records.push({type:'message.part.updated',properties:{part:{id:'part-'+Math.floor(i/120),messageID:'message-'+Math.floor(i/120),sessionID:'test-session',type:'text',text:'synthetic benchmark text '.repeat(1280)}}})
const results=[]
async function run(label,plugin,enabled,file){
 process.env.OPENCODE_EVENT_DIAGNOSTICS=enabled?'1':'0';process.env.OPENCODE_EVENT_DIAGNOSTICS_FILE=file
 const hooks=await plugin.TestEvents()
 const start=performance.now();const delayed=new Promise(r=>setTimeout(()=>r(performance.now()-start),0))
 let max=0
 for(const event of records){const t=performance.now();await hooks.event?.({event});max=Math.max(max,performance.now()-t)}
 const hook_ms=performance.now()-start,event_loop_delay_ms=await delayed
 await new Promise(r=>setTimeout(r,400))
 const bytes=existsSync(file)?statSync(file).size:0
 results.push({mode:label,events:records.length,hook_ms:+hook_ms.toFixed(2),max_hook_ms:+max.toFixed(2),event_loop_delay_ms:+event_loop_delay_ms.toFixed(2),log_bytes:bytes})
 return hooks
}
await run('original full sync logger',baseline,true,join(dir,'original.log'))
const off=await run('optimized normal mode',optimized,false,join(dir,'off.log'))
assert.equal(off.event,undefined);assert.equal(existsSync(join(dir,'off.log')),false)
const on=await run('optimized opt-in summaries',optimized,true,join(dir,'summary.log'))
assert.ok(readFileSync(join(dir,'summary.log'),'utf8').includes('message.part.updated'))
assert.ok(!readFileSync(join(dir,'summary.log'),'utf8').includes('synthetic benchmark text'))
await on.event({event:{type:'session.error',properties:{sessionID:'test-session',error:{name:'BenchmarkError'}}}})
await new Promise(r=>setTimeout(r,400))
assert.ok(readFileSync(join(dir,'summary.log'),'utf8').includes('BenchmarkError'))
writeFileSync(join(dir,'results.json'),JSON.stringify(results,null,2))
console.log(JSON.stringify({results,artifact:join(dir,'results.json')},null,2))
