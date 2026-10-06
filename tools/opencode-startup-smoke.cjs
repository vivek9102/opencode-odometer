// Uses the installed OpenCode executable; makes no inference requests.
const {spawn}=require('node:child_process');
const {mkdirSync,writeFileSync,readFileSync,existsSync,readdirSync}=require('node:fs');
const {join,resolve}=require('node:path');
const assert=require('node:assert/strict');
const root=process.env.OPENCODE_SMOKE_DIR || resolve(__dirname,'../build/opencode-startup-smoke');
const exe=process.env.OPENCODE_SMOKE_EXE;
if(!exe)throw Error('Set OPENCODE_SMOKE_EXE to the installed binary');
const wait=ms=>new Promise(r=>setTimeout(r,ms));
async function freePort(){const server=require('node:net').createServer();await new Promise(r=>server.listen(0,'127.0.0.1',r));const port=server.address().port;await new Promise(r=>server.close(r));return port;}
async function probe(kind,source,port){
 const dir=join(root,kind);mkdirSync(dir,{recursive:true});
 const config=join(dir,'config');mkdirSync(config,{recursive:true});
 const plugin=join(config,'odometer.js');
 writeFileSync(plugin,source.replace('const SERVER_POINTER_FILE = join(homedir(), ".opencode-odometer-server.json")','const SERVER_POINTER_FILE = process.env.OPENCODE_ODOMETER_SERVER_POINTER'));
 const data=join(dir,'odometer');mkdirSync(data,{recursive:true});writeFileSync(join(data,'preferences.json'),JSON.stringify({auto_start:false}));
 writeFileSync(join(config,'opencode.json'),JSON.stringify({autoupdate:false,plugin:[plugin.replaceAll('\\','/')],enabled_providers:['smoke'],provider:{smoke:{npm:'@ai-sdk/openai-compatible',name:'Smoke',options:{baseURL:'http://127.0.0.1:1/v1',apiKey:'test-only'},models:{tiny:{name:'Tiny',limit:{context:1000,output:100},cost:{input:1,output:1}}}}}}));
 const env={...process.env,OPENCODE_TEST_HOME:join(dir,'home'),XDG_CONFIG_HOME:join(dir,'xdg-config'),XDG_DATA_HOME:join(dir,'xdg-data'),XDG_CACHE_HOME:join(dir,'xdg-cache'),XDG_STATE_HOME:join(dir,'xdg-state'),OPENCODE_CONFIG_DIR:config,OPENCODE_DISABLE_PROJECT_CONFIG:'1',OPENCODE_DISABLE_MODELS_FETCH:'1',OPENCODE_DISABLE_DEFAULT_PLUGINS:'1',OPENCODE_ODOMETER_DIR:data,OPENCODE_ODOMETER_HOME:data,OPENCODE_ODOMETER_POINTER:join(data,'pointer.json'),OPENCODE_ODOMETER_SERVER_POINTER:join(data,'server.json')};
 const child=spawn(exe,['serve','--hostname','127.0.0.1','--port',String(port)],{cwd:dir,env,windowsHide:true,stdio:['ignore','pipe','pipe']});
 let log='';child.stdout.on('data',b=>log+=b);child.stderr.on('data',b=>log+=b);
 const started=Date.now();let success=false;
 try{
  for(let n=0;n<40;n++){
   if(child.exitCode!==null)throw Error('OpenCode exited: '+log.slice(-1000));
   try{const response=await fetch(`http://127.0.0.1:${port}/provider`,{signal:AbortSignal.timeout(500)});if(response.ok){const doc=await response.json();assert.ok(doc.all.some(p=>p.id==='smoke'));success=true;break;}}catch{}
   await wait(150);
  }
  if(success){
   for(let n=0;n<15&&!readdirSync(data).some(f=>f.startsWith('models-')&&JSON.parse(readFileSync(join(data,f))).models.length);n++)await wait(200);
   const inventoryPath=join(data,`models-${child.pid}.json`);
   const inventory=JSON.parse(readFileSync(inventoryPath));
   assert.ok(inventory.models.some(m=>m.key==='smoke/tiny'),'provider metadata must reach the bridge');
   const create=async body=>{const r=await fetch(`http://127.0.0.1:${port}/session`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body),signal:AbortSignal.timeout(3000)});assert.ok(r.ok);return r.json()};
   const parent=await create({title:'Odometer isolated smoke'});
   const childSession=await create({parentID:parent.id});
   const spool=join(data,'events.jsonl');
   for(let n=0;n<20&&(!existsSync(spool)||!readFileSync(spool,'utf8').includes(childSession.id));n++)await wait(100);
   const records=readFileSync(spool,'utf8').trim().split('\n').map(JSON.parse);
   assert.ok(records.some(r=>r.type==='session'&&r.id===childSession.id&&r.parentID===parent.id),'real child-session ancestry must be spooled');
   // Queue only; submitting an inference request is deliberately unnecessary.
   const id='smoke-'+Date.now();writeFileSync(join(data,`switch-${inventory.pid}.json`),JSON.stringify({id,key:'smoke/tiny',session_id:parent.id,issued:Date.now()/1000}));
   const status=join(data,`switch-status-${id}.json`);
   for(let n=0;n<30&&!existsSync(status);n++)await wait(100);
   assert.equal(JSON.parse(readFileSync(status)).status,'queued','real SDK session lookup must acknowledge model routing');
  }
  writeFileSync(join(dir,'console.log'),log);
  console.log(JSON.stringify({kind,success,elapsed_ms:Date.now()-started,inventory:readdirSync(data).filter(f=>f.startsWith('models-'))}));
  return success;
 }finally{const exited=new Promise(r=>child.once('exit',r));child.kill();await Promise.race([exited,wait(2000)]);}
}
(async()=>{
 if(process.env.OPENCODE_SMOKE_BASELINE){
  const baseline=readFileSync(process.env.OPENCODE_SMOKE_BASELINE,'utf8');
  assert.equal(await probe('before',baseline,await freePort()),false,'old plugin should reproduce the hang');
 }
 assert.equal(await probe('after',readFileSync(join(__dirname,'../plugin/odometer.js'),'utf8'),await freePort()),true,'fixed plugin must start real OpenCode');
})().catch(e=>{console.error(e);process.exitCode=1});
