// Exercise the installed OpenCode executable against a local fake provider.
// No real provider requests, credentials, or live user data are used.
const {spawn}=require('node:child_process');
const {createServer}=require('node:http');
const {mkdirSync,writeFileSync,readFileSync,existsSync}=require('node:fs');
const {join,resolve}=require('node:path');
const assert=require('node:assert/strict');
const wait=ms=>new Promise(r=>setTimeout(r,ms));
const root=process.env.OPENCODE_SMOKE_DIR || resolve(__dirname,'../build/stop-session-smoke');
const exe=process.env.OPENCODE_SMOKE_EXE;
if(!exe)throw Error('Set OPENCODE_SMOKE_EXE');
async function freePort(){const server=createServer();await new Promise(r=>server.listen(0,'127.0.0.1',r));const port=server.address().port;await new Promise(r=>server.close(r));return port;}
(async()=>{
 const requested=[];
 let holding=false;
 const provider=createServer(async(req,res)=>{
  let raw='';for await(const chunk of req)raw+=chunk;
  if(!req.url.endsWith('/chat/completions')){res.writeHead(404).end();return;}
  const input=JSON.parse(raw);requested.push(input.model);
  const id='fake-'+requested.length,base={id,object:'chat.completion.chunk',created:Math.floor(Date.now()/1000),model:input.model};
  if(input.stream){
   res.writeHead(200,{'Content-Type':'text/event-stream'});
   res.write('data: '+JSON.stringify({...base,choices:[{index:0,delta:{role:'assistant',content:'LOCAL MOCK OK'},finish_reason:null}]})+'\n\n');
   if(!holding){res.write('data: '+JSON.stringify({...base,choices:[{index:0,delta:{},finish_reason:'stop'}]})+'\n\n');res.end('data: [DONE]\n\n');return;}
   const timer=setTimeout(()=>res.end('data: [DONE]\n\n'),60000); res.on('close',()=>clearTimeout(timer));
   // Keep the stream active until cancelled.
  }else{res.writeHead(200,{'Content-Type':'application/json'}).end(JSON.stringify({...base,object:'chat.completion',choices:[{index:0,message:{role:'assistant',content:'LOCAL MOCK OK'},finish_reason:'stop'}],usage:{prompt_tokens:10,completion_tokens:3,total_tokens:13}}));}
 });
 await new Promise(r=>provider.listen(0,'127.0.0.1',r));
 const config=join(root,'config'),data=join(root,'odometer');mkdirSync(config,{recursive:true});mkdirSync(data,{recursive:true});
 writeFileSync(join(data,'preferences.json'),JSON.stringify({auto_start:false}));
 const plugin=join(config,'odometer.js');
 writeFileSync(plugin,readFileSync(join(__dirname,'../plugin/odometer.js'),'utf8').replace('const SERVER_POINTER_FILE = join(homedir(), ".opencode-odometer-server.json")','const SERVER_POINTER_FILE = process.env.OPENCODE_ODOMETER_SERVER_POINTER'));
 const model={limit:{context:10000,output:100},cost:{input:1,output:1}};
 writeFileSync(join(config,'opencode.json'),JSON.stringify({autoupdate:false,model:'smoke/default',small_model:'smoke/default',agent:{build:{model:'smoke/default'}},plugin:[plugin.replaceAll('\\','/')],enabled_providers:['smoke'],provider:{smoke:{npm:'@ai-sdk/openai-compatible',name:'Local smoke',options:{baseURL:`http://127.0.0.1:${provider.address().port}/v1`,apiKey:'local-test-only'},models:{default:{...model,name:'Default'},selected:{...model,name:'Selected'}}}}}));
 const env={...process.env,OPENCODE_TEST_HOME:join(root,'home'),XDG_CONFIG_HOME:join(root,'xdg-config'),XDG_DATA_HOME:join(root,'xdg-data'),XDG_CACHE_HOME:join(root,'xdg-cache'),XDG_STATE_HOME:join(root,'xdg-state'),OPENCODE_CONFIG_DIR:config,OPENCODE_DISABLE_PROJECT_CONFIG:'1',OPENCODE_DISABLE_MODELS_FETCH:'1',OPENCODE_DISABLE_DEFAULT_PLUGINS:'1',OPENCODE_ODOMETER_DIR:data,OPENCODE_ODOMETER_HOME:data,OPENCODE_ODOMETER_POINTER:join(data,'pointer.json'),OPENCODE_ODOMETER_SERVER_POINTER:join(data,'server.json')};
 let child,base,logs='';
 async function start(){
  const port=await freePort();base=`http://127.0.0.1:${port}`;
  child=spawn(exe,['serve','--hostname','127.0.0.1','--port',String(port),'--print-logs','--log-level','DEBUG'],{cwd:root,env,windowsHide:true,stdio:['ignore','pipe','pipe']});
  child.stdout.on('data',b=>logs+=b);child.stderr.on('data',b=>logs+=b);
  for(let n=0;n<80;n++){
   if(child.exitCode!==null)throw Error('OpenCode exited: '+logs.slice(-1000));
   try{const r=await fetch(base+'/provider',{signal:AbortSignal.timeout(1000)});if(r.ok){const d=await r.json();writeFileSync(join(root,'providers.json'),JSON.stringify({connected:d.connected,all:d.all?.map(p=>({id:p.id,models:Object.keys(p.models||{})}))}));}}catch{}
   const inventory=join(data,`models-${child.pid}.json`);
   if(existsSync(inventory)&&JSON.parse(readFileSync(inventory)).models.length)return;
   await wait(150);
  }
  throw Error('OpenCode inventory timed out');
 }
 async function stop(){if(!child||child.exitCode!==null)return;const ended=new Promise(r=>child.once('exit',r));child.kill();await Promise.race([ended,wait(2000)]);}
 async function api(path,body){const response=await fetch(base+path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body),signal:AbortSignal.timeout(30000)});assert.ok(response.ok,'HTTP '+response.status+' '+await response.clone().text());return response.json();}
 async function prompt(sid){return api('/session/'+sid+'/message',{agent:'build',model:{providerID:'smoke',modelID:'default'},parts:[{type:'text',text:'Reply with LOCAL MOCK OK. Do not use tools.'}]});}
 const queue=(sid,id,key='smoke/selected')=>writeFileSync(join(data,`switch-session-${sid}.json`),JSON.stringify({id,session_id:sid,key,persistent:true,issued:Math.floor(Date.now()/1000)}));
 try{
  await start();
  const chat=await api('/session',{title:'Stop smoke'}),other=await api('/session',{title:'Untouched chat'});
  await prompt(other.id);holding=true;
  let otherDone=false;
  const otherPrompt=prompt(other.id).finally(()=>{otherDone=true});
  for(let n=0;n<500&&requested.length<2;n++)await wait(50);
  const activePrompt=prompt(chat.id);
  for(let n=0;n<500&&requested.length<3;n++)await wait(50);
  assert.equal(requested.length,3,'provider streams did not start');
  const inv=JSON.parse(readFileSync(join(data,`models-${child.pid}.json`)));
  assert.equal(inv.abort_protocol,1);
  const id='stop-once',started=Date.now();
  writeFileSync(join(data,`abort-${child.pid}-${id}.json`),JSON.stringify({id,instance:inv.instance,session_id:chat.id,issued:Date.now()/1000}));
  const status=join(data,`abort-status-${id}.json`);
  for(let n=0;n<100&&!existsSync(status);n++)await wait(25);
  assert.ok(existsSync(status),'stop was not acknowledged');
  const ack=JSON.parse(readFileSync(status));assert.equal(ack.status,'stopped',ack.detail);
  const elapsed=Date.now()-started;
  const result=await Promise.race([activePrompt,wait(2500).then(()=>{throw Error('acknowledged stop did not end the response')})]);
  assert.equal(result.info.error?.name,'MessageAbortedError','response did not report cancellation');
  await wait(300);assert.equal(otherDone,false,'another chat was stopped');
  await api('/session/'+other.id+'/abort',{});await otherPrompt;
  console.log(JSON.stringify({success:true,firstClick:true,acknowledged:true,elapsedMS:elapsed,otherChatUntouched:true,error:result.info.error?.name}));
 }finally{await stop();writeFileSync(join(root,'console.log'),logs);provider.closeAllConnections();await new Promise(r=>provider.close(r));}
})().catch(e=>{console.error(e);process.exitCode=1});
