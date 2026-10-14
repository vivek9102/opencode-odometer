// Real OpenCode V2 + real Go budget engine + local fake provider only.
// All configuration, credentials, DB, logs and accounting live under build/.
const {spawn}=require('node:child_process');
const fs=require('node:fs');const path=require('node:path');const http=require('node:http');const assert=require('node:assert/strict');
const exe=process.env.OPENCODE_SMOKE_EXE,hostExe=process.env.ODOMETER_SMOKE_HOST;
if(!exe||!hostExe)throw Error('Set OPENCODE_SMOKE_EXE and ODOMETER_SMOKE_HOST');
const root=path.resolve(__dirname,'../build/v2-native-smoke-'+Date.now()),config=path.join(root,'config'),data=path.join(root,'odometer'),project=path.join(root,'project');
for(const dir of [root,config,data,project,path.join(root,'tmp')])fs.mkdirSync(dir,{recursive:true});
fs.writeFileSync(path.join(project,'fixture.txt'),'Synthetic smoke fixture');
const json=(name,value)=>fs.writeFileSync(path.join(data,name),JSON.stringify(value));
const load=name=>{try{return JSON.parse(fs.readFileSync(path.join(data,name)))}catch{return null}};
const sleep=ms=>new Promise(r=>setTimeout(r,ms));
async function until(fn,ms=15000){const end=Date.now()+ms;let last;while(Date.now()<end){try{const r=await fn();if(r)return r}catch(e){last=e}await sleep(100)}throw last||Error('Timed out waiting for smoke state');}
const children=[];let log='',hostLog='',server,base='',sessions=[],calls=[],holding=false;
const env={...process.env,HOME:root,USERPROFILE:root,LOCALAPPDATA:path.join(root,'local'),APPDATA:path.join(root,'roaming'),OPENCODE_TEST_HOME:root,OPENCODE_DB:path.join(root,'opencode.db'),XDG_CONFIG_HOME:path.join(root,'xdg-config'),XDG_DATA_HOME:path.join(root,'xdg-data'),XDG_CACHE_HOME:path.join(root,'cache'),XDG_STATE_HOME:path.join(root,'state'),TEMP:path.join(root,'tmp'),TMP:path.join(root,'tmp'),OPENCODE_CONFIG_DIR:config,OPENCODE_CONFIG:path.join(config,'opencode.json'),ProgramData:path.join(root,'system'),OPENCODE_CONFIG_PROJECT_DISABLE:'1',OPENCODE_DISABLE_MODELS_FETCH:'1',OPENCODE_DISABLE_FILEWATCHER:'0',OPENCODE_ODOMETER_DIR:data,OPENCODE_ODOMETER_POINTER:path.join(root,'pointer.json'),OPENCODE_ODOMETER_SERVER_POINTER:path.join(root,'server-pointer.json'),OPENCODE_SERVER_PASSWORD:'smoke-only',OPENCODE_LOG_LEVEL:'INFO'};
for(const k of Object.keys(env))if(/^(OPENAI|ANTHROPIC|GOOGLE|GEMINI|AZURE|AWS|ARTIFICIAL_ANALYSIS|OPENCODE_CONFIG_CONTENT)/.test(k))delete env[k];
const auth={authorization:'Basic '+Buffer.from('opencode:smoke-only').toString('base64'),'Content-Type':'application/json'};
async function api(endpoint,body,method){const r=await fetch(base+endpoint,{headers:auth,method:method||(body===undefined?'GET':'POST'),body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(10000)});const raw=await r.text();if(!r.ok)throw Error(`${endpoint} ${r.status} ${raw.slice(0,800)}`);return raw?JSON.parse(raw):null}
function child(binary,args){const p=spawn(binary,args,{cwd:project,env,stdio:['ignore','pipe','pipe'],windowsHide:true});children.push(p);p.stdout.on('data',b=>{if(binary===exe)log+=b;else hostLog+=b});p.stderr.on('data',b=>{if(binary===exe)log+=b;else hostLog+=b});return p}
async function freePort(){const s=http.createServer();await new Promise(r=>s.listen(0,'127.0.0.1',r));const port=s.address().port;await new Promise(r=>s.close(r));return port}
(async()=>{
  server=http.createServer(async(req,res)=>{
    let raw='';for await(const chunk of req)raw+=chunk;
    if(!req.url.endsWith('/chat/completions')){res.writeHead(404);res.end();return}
    const body=JSON.parse(raw);calls.push(body.model);
    const tool=body.model==='original';const content=tool?{tool_calls:[{index:0,id:'smoke_read',type:'function',function:{name:'read',arguments:JSON.stringify({filePath:path.join(project,'fixture.txt')})}}]}:{content:'Synthetic fallback completed the outstanding smoke task.'};
    res.writeHead(200,{'Content-Type':'text/event-stream','Cache-Control':'no-cache'});
    const send=o=>res.write('data: '+JSON.stringify({id:'chatcmpl-smoke',object:'chat.completion.chunk',created:Math.floor(Date.now()/1000),model:body.model,...o})+'\n\n');
    send({choices:[{index:0,delta:{role:'assistant',...content},finish_reason:null}]});
    if(holding&&!tool){await new Promise(resolve=>{const timer=setTimeout(resolve,15000);res.once('close',()=>{clearTimeout(timer);resolve()})});if(res.destroyed)return;}
    await sleep(150);send({choices:[{index:0,delta:{},finish_reason:tool?'tool_calls':'stop'}],usage:{prompt_tokens:tool?1000:100000,completion_tokens:10,total_tokens:tool?1010:100010}});res.end('data: [DONE]\n\n');
  });await new Promise(r=>server.listen(0,'127.0.0.1',r));
  json('preferences.json',{auto_start:false});
  json('prices.json',{reference:'synthetic smoke',models:{'smoke/original':{input:3,output:15},'smoke/cheap':{input:.1,output:.2}}});
  const plugin=path.join(config,'plugins','odometer-v2');fs.mkdirSync(plugin,{recursive:true});
  for(const [source,target] of [['odometer-v2.js','index.js'],['odometer-v2-tui.tsx','tui.tsx'],['tui-presence.js','tui-presence.js'],['v2-models.js','v2-models.js']])fs.copyFileSync(path.join(__dirname,'../plugin',source),path.join(plugin,target));
  fs.writeFileSync(path.join(plugin,'package.json'),JSON.stringify({name:'odometer-v2-smoke',type:'module',exports:{'.':'./index.js','./tui':'./tui.tsx'}}));
  const cfg={update:'disable',plugins:[plugin],model:'smoke/original',providers:{smoke:{name:'Smoke',package:'@opencode/ai/providers/openai-compatible',settings:{baseURL:`http://127.0.0.1:${server.address().port}/v1`,apiKey:'smoke-only'},models:{original:{name:'Original',limit:{context:200000,output:1000},cost:{input:3,output:15},capabilities:{tools:true,input:['text'],output:['text']}},cheap:{name:'Cheap',limit:{context:200000,output:1000},cost:{input:.1,output:.2},capabilities:{tools:true,input:['text'],output:['text']}}}}},permissions:[{action:'read',resource:'*',effect:'allow'}]};
  fs.writeFileSync(path.join(config,'opencode.json'),JSON.stringify(cfg));
  const port=await freePort();base=`http://127.0.0.1:${port}`;
  const oc=child(exe,['--print-logs','serve','--hostname','127.0.0.1','--port',String(port)]);
  const serverInfo=await until(async()=>{if(oc.exitCode!==null)throw Error('OpenCode exited: '+log.slice(-900));try{return await api('/api/info')}catch{return null}},30000);
  console.log(JSON.stringify({stage:'server-ready',server:serverInfo.version,pid:serverInfo.pid,root}));
  const host=child(hostExe,[]);
  for(let i=0;i<2;i++){const r=await api('/api/session',{title:'Synthetic V2 '+i,location:{directory:project},model:{providerID:'smoke',id:'original'}});sessions.push(r.data)}
  // Each real terminal has its own PID. The smoke uses two synthetic owners
  // with live child processes so Windows liveness validation is exercised.
  // V2 loads location plugins on prompt admission, not on session creation.
  const bootstrap=await api(`/api/session/${sessions[0].id}/prompt`,{text:'Synthetic plugin initialization',resume:false,delivery:'queue'});
  await fetch(base+`/api/session/${sessions[0].id}/inbox/${bootstrap.data.id}`,{method:'DELETE',headers:auth});
  const owner0=child(process.execPath,['-e','setInterval(()=>{},1000)']),owner1=child(process.execPath,['-e','setInterval(()=>{},1000)']);
  const beat=()=>{for(let i=0;i<2;i++)json(`tui-smoke${i}.json`,{id:`smoke${i}`,name:`Synthetic ${i}`,pid:[owner0.pid,owner1.pid][i],server_pid:serverInfo.pid,api_major:2,location:sessions[i].location.directory,started:startedAt,updated:Math.floor(Date.now()/1000),session_id:sessions[i].id,model:'smoke/original',active:false,closed:false})};
  const startedAt=Date.now(); beat();const timer=setInterval(beat,1000);
  try{
    console.log(JSON.stringify({stage:'sessions-created',sessions:sessions.map(s=>({id:s.id,location:s.location})),plugins:(await api('/api/plugin?location[directory]='+encodeURIComponent(project))).data.filter(p=>p.id.includes('odometer'))}));
    await until(()=>load(`models-${owner0.pid}.json`)?.models?.some(m=>m.key==='smoke/cheap'),30000);
    await until(()=>load('smoke-rows.json')?.length===2);
    json('smoke-command.json',{id:'smoke0',limit:.001,rule:{rule:'switch',fallback_model:'smoke/cheap',fallback_limit:.05,then:'stop'}});
    await until(()=>!fs.existsSync(path.join(data,'smoke-command.json')));
    json('smoke-command.json',{id:'smoke1',limit:.001,rule:{rule:'switch',fallback_model:'smoke/cheap',fallback_limit:.005,then:'stop'}});
    await until(()=>!fs.existsSync(path.join(data,'smoke-command.json')));
    await Promise.all(sessions.map(s=>api(`/api/session/${s.id}/prompt`,{text:'Read fixture.txt and finish this synthetic smoke task.'})));
    const row=await until(()=>{if(host.exitCode!==null)throw Error('Go host exited: '+hostLog.slice(-900));return load('smoke-rows.json')?.find(r=>r.id==='smoke0'&&r.on_fallback&&r.fallback_spent>0)},30000);
    assert.equal((await api(`/api/session/${sessions[0].id}`)).data.model.id,'cheap');
    assert.equal((await api(`/api/session/${sessions[1].id}`)).data.model.id,'cheap');
    const capped=await until(()=>load('smoke-rows.json')?.find(r=>r.id==='smoke1'&&r.stopped&&r.fallback_spent>=.005));
    assert.ok(capped.stopped);
    assert.equal(calls.filter(m=>m==='original').length,2,'hard budget allowed another original request');
    assert.equal(calls.filter(m=>m==='cheap').length,2,'continuation duplicated');
    assert.ok(row.fallback_spent>0);
    json('smoke-command.json',{id:'smoke0',clear:true});await until(()=>!fs.existsSync(path.join(data,'smoke-command.json')));
    await until(async()=>((await api(`/api/session/${sessions[0].id}`)).data.model.id==='original'));
    await api(`/api/session/${sessions[0].id}/model`,{model:{providerID:'smoke',id:'cheap'}});
    assert.equal((await api(`/api/session/${sessions[0].id}`)).data.model.id,'cheap');
    // Load the actual terminal adapter and exercise its cancellation bridge.
    json('smoke-command.json',{id:'smoke1',clear:true});await until(()=>!fs.existsSync(path.join(data,'smoke-command.json')));
    const tui=spawn(exe,['--server',base,'--session',sessions[0].id],{cwd:project,env:{...env,TERM:'xterm-256color',OPENCODE_CLI_CONFIG_CONTENT:JSON.stringify({plugins:[plugin]})},stdio:['pipe','pipe','pipe'],windowsHide:true});
    children.push(tui);let tuiLog='';tui.stdout.on('data',b=>tuiLog+=b);tui.stderr.on('data',b=>tuiLog+=b);
    const terminal=await until(()=>{if(tui.exitCode!==null)throw Error('TUI exited: '+tuiLog.slice(-600));return fs.readdirSync(data).filter(f=>/^tui-/.test(f)).map(load).find(t=>t.pid===tui.pid&&t.server_pid===serverInfo.pid&&t.session_id===sessions[0].id)},20000);
    assert.equal(terminal.api_major,2);
    holding=true;const before=calls.length;
    await api(`/api/session/${sessions[0].id}/prompt`,{text:'Synthetic held request to test Stop Session.'});
    await until(()=>calls.length===before+1);
    await api(`/api/session/${sessions[0].id}/prompt`,{text:'Synthetic queued automatic input.',metadata:{odometer_transition:'smoke-cancel'},delivery:'queue',resume:false});
    const queuedHuman=await api(`/api/session/${sessions[0].id}/prompt`,{text:'Synthetic queued human input.',delivery:'queue',resume:false});
    json('smoke-command.json',{id:terminal.id,stop:true});await until(()=>!fs.existsSync(path.join(data,'smoke-command.json')));
    await until(async()=>!(await api('/api/session/active')).data[sessions[0].id]);
    await until(async()=>!(await api(`/api/session/${sessions[0].id}/inbox`)).data.some(item=>item.payload?.metadata?.odometer_transition));
    const remaining=(await api(`/api/session/${sessions[0].id}/inbox`)).data;
    assert.ok(remaining.some(item=>item.id===queuedHuman.id||item.payload?.text==='Synthetic queued human input.'),'STOP must preserve queued human input');
    for(const item of remaining)await api(`/api/session/${sessions[0].id}/inbox/${item.id}`,undefined,'DELETE');
    assert.equal((await api(`/api/session/${sessions[1].id}`)).data.model.id,'original');
    // Piped input has no reliable native terminal focus; enable this only
    // when validating the harness with a real terminal/input driver.
    if(process.env.OPENCODE_SMOKE_ESC==='1'){
    const beforeEsc=calls.length;
    await api(`/api/session/${sessions[0].id}/prompt`,{text:'Synthetic held request to test native Esc.'});
    await until(()=>calls.length===beforeEsc+1);
    tui.stdin.write('\x1b');await sleep(150);tui.stdin.write('\x1b');
    await until(async()=>!(await api('/api/session/active')).data[sessions[0].id],8000);
    }
    fs.writeFileSync(path.join(root,'tui-console.log'),tuiLog);
    console.log(JSON.stringify({success:true,version:(await api('/api/info')).version,provider_calls:calls,root,checks:['two enabled sessions','fallback hard cap','hard original cap','actual fallback model','single continuation','clear releases routing','native V2 TUI loads','native Stop Session','STOP cancels queued automatic input and preserves human input']}));
  }finally{clearInterval(timer)}
})().catch(error=>{console.error(error);console.error(log.slice(-1800));console.error(hostLog.slice(-900));process.exitCode=1}).finally(async()=>{for(const p of children)p.kill();server?.close();fs.writeFileSync(path.join(root,'opencode-console.log'),log);fs.writeFileSync(path.join(root,'host-console.log'),hostLog)});
