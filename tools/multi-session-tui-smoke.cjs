// Two actual OpenCode TUIs, isolated data, localhost fake provider, no paid calls.
const {spawn}=require('node:child_process');
const {createServer}=require('node:http');
const {mkdirSync,writeFileSync,readFileSync,readdirSync,existsSync,renameSync}=require('node:fs');
const {join,resolve}=require('node:path');
const assert=require('node:assert/strict');
const wait=ms=>new Promise(r=>setTimeout(r,ms));
const root=resolve(process.env.OPENCODE_SMOKE_DIR||join(__dirname,'../build/multi-session-tui-'+Date.now()));
const exe=process.env.OPENCODE_SMOKE_EXE;if(!exe)throw Error('Set OPENCODE_SMOKE_EXE');
const read=p=>{try{return JSON.parse(readFileSync(p))}catch{return null}};
(async()=>{
 mkdirSync(root,{recursive:true});const data=join(root,'bridge');mkdirSync(data,{recursive:true});writeFileSync(join(data,'preferences.json'),JSON.stringify({auto_start:false}));
 let requested=0,holding=false;
 const provider=createServer(async(req,res)=>{
  let raw='';for await(const chunk of req)raw+=chunk;
  if(!req.url.endsWith('/chat/completions')){res.writeHead(404).end();return;}
  const input=JSON.parse(raw);requested++;const base={id:'fake-'+requested,object:'chat.completion.chunk',created:Math.floor(Date.now()/1000),model:input.model};
  if(input.stream){res.writeHead(200,{'Content-Type':'text/event-stream'});res.write('data: '+JSON.stringify({...base,choices:[{index:0,delta:{role:'assistant',content:'LOCAL MOCK OK'},finish_reason:null}]})+'\n\n');if(holding&&input.model==='paid'){const timer=setTimeout(()=>res.end('data: [DONE]\n\n'),60000);res.on('close',()=>clearTimeout(timer));return;}res.write('data: '+JSON.stringify({...base,choices:[{index:0,delta:{},finish_reason:'stop'}],usage:{prompt_tokens:10,completion_tokens:3,total_tokens:13}})+'\n\n');res.end('data: [DONE]\n\n');}
  else res.writeHead(200,{'Content-Type':'application/json'}).end(JSON.stringify({...base,object:'chat.completion',choices:[{index:0,message:{role:'assistant',content:'LOCAL MOCK OK'},finish_reason:'stop'}],usage:{prompt_tokens:10,completion_tokens:3,total_tokens:13}}));
 });
 await new Promise(r=>provider.listen(0,'127.0.0.1',r));
 const children=[],logs=[];let accountingTimer;
 const reports=()=>readdirSync(data).filter(n=>/^tui-.*\.json$/.test(n)).map(n=>read(join(data,n))).filter(Boolean);
 async function until(fn,label,ms=120000){const end=Date.now()+ms;while(Date.now()<end){const value=fn();if(value)return value;if(children.some(c=>c.exitCode!==null))throw Error(label+': TUI exited; '+logs.map(x=>x.text.slice(-2000)).join('\n'));await wait(100);}throw Error(label+' timed out; '+logs.map(x=>x.text.slice(-2000)).join('\n'));}
 const probe=`import {readFileSync,writeFileSync,rmSync} from 'node:fs';import {join} from 'node:path';
 export default {id:'multi-session-smoke-probe',tui:async api=>{
 const dir=process.env.ODO_SMOKE_CONTROL;writeFileSync(join(dir,'loaded.json'),JSON.stringify({version:api.app.version,route:api.route.current}));let busy=false;
 const originalTitle=api.renderer.setTerminalTitle.bind(api.renderer);api.renderer.setTerminalTitle=title=>{if(title.startsWith('OC | '))writeFileSync(join(dir,'title.json'),JSON.stringify({title}));return originalTitle(title);};
 const timer=setInterval(async()=>{if(busy)return;let cmd;try{cmd=JSON.parse(readFileSync(join(dir,'command.json')));rmSync(join(dir,'command.json'));}catch{return;}busy=true;try{let result;
 if(cmd.action==='create'){const response=await api.client.session.create({title:'Synthetic smoke '+process.env.ODO_SMOKE_NUMBER});if(response.error)throw Error(JSON.stringify(response.error));result=response.data;api.route.navigate('session',{sessionID:result.id});}
 if(cmd.action==='prompt'){result=await api.client.session.prompt({sessionID:cmd.sid,model:{providerID:'smoke',modelID:'paid'},parts:[{type:'text',text:'Reply with LOCAL MOCK OK; do not use tools.'}]});}
 if(cmd.action==='stop'){result=await api.client.session.abort({sessionID:cmd.sid});}
 if(cmd.action==='native-prompt'){api.ui.dialog.clear();await new Promise(r=>setTimeout(r,200));await api.client.tui.appendPrompt({text:'Reply with LOCAL MOCK OK; do not use tools.'});await new Promise(r=>setTimeout(r,200));api.command.trigger('prompt.submit');result={route:api.route.current};}
 if(cmd.action==='escape'){api.renderer.keyInput.emit('keypress',{name:'escape',sequence:'\u001b',ctrl:false,shift:false,meta:false,defaultPrevented:false,propagationStopped:false,preventDefault(){this.defaultPrevented=true},stopPropagation(){this.propagationStopped=true}});result={route:api.route.current};}
 if(cmd.action==='status'){result={status:api.state.session.status(cmd.sid),messages:api.state.session.messages(cmd.sid).map(m=>({id:m.id,role:m.role,error:m.error?.name,completed:m.time?.completed,modelID:m.modelID,providerID:m.providerID,parentID:m.parentID}))};}
 if(cmd.action==='layout'){const rows=[];function visit(node){let text=node.plainText??node.text??node.content;if(typeof text!=='string')text=text?.chunks?.map(c=>c.text).join('')??'';rows.push({id:node.id,type:node.constructor?.name,text,x:node.x,y:node.y,width:node.width,height:node.height});for(const child of node.getChildren?.()??[])visit(child);}visit(api.renderer.root);result=rows;}
 if(cmd.action==='exit'){api.command.trigger('app.exit');}
 writeFileSync(join(dir,'result-'+cmd.id+'.json'),JSON.stringify({result}));}catch(error){writeFileSync(join(dir,'result-'+cmd.id+'.json'),JSON.stringify({error:String(error)}));}finally{busy=false;}},200);
 api.lifecycle.onDispose(()=>clearInterval(timer));}};`;
 function start(n){
  const base=join(root,'tui-'+n),config=join(base,'config'),plugins=join(config,'plugins'),pkg=join(plugins,'odometer-presence');mkdirSync(pkg,{recursive:true});
  writeFileSync(join(plugins,'odometer.js'),readFileSync(join(__dirname,'../plugin/odometer.js')));
  writeFileSync(join(pkg,'package.json'),JSON.stringify({name:'opencode-odometer-presence',type:'module',exports:{'.':'./index.js','./tui':'./tui.tsx'}}));
  writeFileSync(join(pkg,'index.js'),'export default async()=>({})');writeFileSync(join(pkg,'tui.tsx'),readFileSync(join(__dirname,'../plugin/odometer-tui.tsx')));writeFileSync(join(pkg,'tui-presence.js'),readFileSync(join(__dirname,'../plugin/tui-presence.js')));
  writeFileSync(join(config,'probe.tsx'),probe);writeFileSync(join(config,'tui.json'),JSON.stringify({plugin:[join(config,'probe.tsx').replaceAll('\\','/'),join(pkg,'tui.tsx').replaceAll('\\','/')]}));
  const model={name:'Synthetic paid',limit:{context:10000,output:100},cost:{input:1,output:1}};
  writeFileSync(join(config,'opencode.json'),JSON.stringify({autoupdate:false,model:'smoke/paid',small_model:'smoke/paid',enabled_providers:['smoke'],provider:{smoke:{npm:'@ai-sdk/openai-compatible',name:'Local fake',options:{baseURL:'http://127.0.0.1:'+provider.address().port+'/v1',apiKey:'local-test-only'},models:{paid:model,cheap:{...model,name:'Synthetic fallback',limit:{context:200000,output:100},cost:{input:.1,output:.4}}}}}}));
  const env={...process.env,OPENCODE_TEST_HOME:join(base,'home'),XDG_CONFIG_HOME:join(base,'xdg-config'),XDG_DATA_HOME:join(base,'xdg-data'),XDG_CACHE_HOME:join(base,'xdg-cache'),XDG_STATE_HOME:join(base,'xdg-state'),OPENCODE_CONFIG_DIR:config,OPENCODE_DISABLE_PROJECT_CONFIG:'1',OPENCODE_DISABLE_MODELS_FETCH:'1',OPENCODE_DISABLE_DEFAULT_PLUGINS:'1',OPENCODE_ODOMETER_DIR:data,OPENCODE_ODOMETER_HOME:data,OPENCODE_ODOMETER_POINTER:join(data,'pointer.json'),OPENCODE_ODOMETER_SERVER_POINTER:join(base,'server.json'),ODO_SMOKE_CONTROL:config,ODO_SMOKE_NUMBER:String(n)};
  const child=spawn(exe,['--print-logs','--log-level','DEBUG'],{cwd:base,env,windowsHide:true,stdio:process.env.OPENCODE_SMOKE_TTY==='1'?'inherit':['pipe','pipe','pipe']});const log={text:''};child.stdout?.on('data',b=>log.text+=b);child.stderr?.on('data',b=>log.text+=b);children.push(child);logs.push(log);return {child,config};
 }
 async function command(tui,id,action,extra={}){writeFileSync(join(tui.config,'command.json'),JSON.stringify({id,action,...extra}));const result=await until(()=>read(join(tui.config,'result-'+id+'.json')),action);assert.ok(!result.error,result.error);return result.result;}
 try{
  const one=start(1),two=start(2);await until(()=>reports().filter(r=>!r.closed).length===2,'startup presence');
  const initial=reports();assert.equal(requested,0);assert.equal(initial.every(r=>r.session_id===''),true);assert.notEqual(initial[0].name,initial[1].name);
  await until(()=>initial.every(r=>[one,two].some(t=>read(join(t.config,'title.json'))?.title==='OC | '+r.name)),'visible startup names');
  writeFileSync(join(root,'home-layout.json'),JSON.stringify(await command(one,'home-layout','layout'),null,2));
  const chat1=await command(one,'create1','create'),chat2=await command(two,'create2','create');await until(()=>reports().some(r=>r.session_id===chat1.id)&&reports().some(r=>r.session_id===chat2.id),'chat binding');
  const row1=reports().find(r=>r.session_id===chat1.id),row2=reports().find(r=>r.session_id===chat2.id);assert.equal(row1.name,initial.find(r=>r.id===row1.id).name);
  await wait(500);writeFileSync(join(root,'session-layout.json'),JSON.stringify(await command(one,'session-layout','layout'),null,2));
  await command(one,'native-followup','native-prompt');
  await until(()=>requested>0,'native prompt request');await wait(1800);
  assert.equal(reports().find(r=>r.id===row1.id)?.session_id,chat1.id,'native prompt created a different conversation, which discards its budget');
  const preBudgetRequests=requested;
  const verdict=(row,cost,limit,mode)=>({budget_session_id:row.id,cost,limit,mode,state:cost>=limit?'over':'ok',fraction:cost/limit,enforced:true,grace_remaining:0,hard_stop_at:1.5,past_hard_stop:cost>=limit*1.5});
  const budget={enabled:true,updated:Math.floor(Date.now()/1000),block_when_exceeded:true,free_models:[],sessions:{[chat1.id]:verdict(row1,1.1,1,'hard'),[chat2.id]:verdict(row2,.1,2,'soft')}};
  writeFileSync(join(data,'budget.json'),JSON.stringify(budget));
  const denied=await command(one,'denied','prompt',{sid:chat1.id});assert.equal(denied.data?.info?.error?.name,'MessageAbortedError');assert.equal(requested,preBudgetRequests,'over-budget TUI reached provider');
  const allowed=await command(two,'allowed','prompt',{sid:chat2.id});assert.ok(!allowed.data?.info?.error,JSON.stringify(allowed));assert.ok(requested>0,'other TUI was blocked');
  budget.sessions[chat1.id]=verdict(row1,1.1,2,'hard');budget.updated=Math.floor(Date.now()/1000);writeFileSync(join(data,'budget.json'),JSON.stringify(budget));await command(one,'raised','prompt',{sid:chat1.id});
  // The actual prompt must keep its chat binding across repeated submissions.
  // Exercise native Esc twice (OpenCode's normal interrupt gesture), rather
  // than calling session.abort directly and bypassing the prompt integration.
  holding=true;const beforeEscape=requested;
  await command(one,'native-held','native-prompt');
  await until(()=>requested>beforeEscape&&reports().find(r=>r.id===row1.id)?.active,'native active turn');
  assert.equal(reports().find(r=>r.id===row1.id)?.session_id,chat1.id,'follow-up lost chat binding');
  await command(one,'escape-first','escape');
  assert.notEqual((await command(one,'escape-pending','status',{sid:chat1.id})).status?.type,'idle','first Esc should request confirmation');
  await command(one,'escape-second','escape');
  await until(()=>!reports().find(r=>r.id===row1.id)?.active,'native Esc interruption',10000);
  // Idle is published before the final assistant update reaches the TUI.
  let interrupted;
  for(let attempt=0;attempt<20;attempt++){
   interrupted=await command(one,'escape-result-'+attempt,'status',{sid:chat1.id});
   if(interrupted.messages.findLast(m=>m.role==='assistant')?.error)break;
   await wait(250);
  }
  assert.equal(interrupted.messages.findLast(m=>m.role==='assistant')?.error,'MessageAbortedError','Esc did not abort the actual conversation');
  // Keep both provider streams running. STOP must end only the chosen one.
  holding=true;const before=requested;let otherDone=false;
  const liveOne=command(one,'holding1','prompt',{sid:chat1.id}),liveTwo=command(two,'holding2','prompt',{sid:chat2.id}).finally(()=>{otherDone=true;});
  await until(()=>requested>=before+2,'two active provider streams');
  writeFileSync(join(data,`stop-tui-${row1.id}.json`),JSON.stringify({id:'target-stop',instance:row1.id,session_id:chat1.id,issued:Date.now()/1000}));
  const ack=await until(()=>read(join(data,'abort-status-target-stop.json')),'targeted stop');assert.equal(ack.status,'stopped',ack.detail);
  const cancelled=await liveOne;assert.equal(cancelled.data?.info?.error?.name,'MessageAbortedError');assert.equal(otherDone,false,'STOP cancelled another TUI');
  writeFileSync(join(data,`stop-tui-${row2.id}.json`),JSON.stringify({id:'cleanup-stop',instance:row2.id,session_id:chat2.id,issued:Date.now()/1000}));await liveTwo;
  // Two simultaneous automatic transitions through actual isolated TUIs.
  const ids=['automatic-tui-one','automatic-tui-two'],pairs=[[one,row1,chat1],[two,row2,chat2]];
  budget.accounted={};budget.sessions={};
  for(let i=0;i<2;i++)budget.sessions[pairs[i][2].id]={budget_session_id:pairs[i][1].id,strict:true,enforced:true,stage:'original',state:'ok',cost:0,limit:1,fraction:0,original_model:'smoke/paid',fallback_model:'smoke/cheap'};
  const publish=()=>{budget.updated=Date.now()/1000;const file=join(data,'budget.json'),tmp=file+'.tmp';writeFileSync(tmp,JSON.stringify(budget));try{renameSync(tmp,file)}catch(e){if(!['EPERM','EACCES','EBUSY'].includes(e.code))throw e;}};publish();
  accountingTimer=setInterval(()=>{
   if(existsSync(join(data,'events.jsonl')))for(const line of readFileSync(join(data,'events.jsonl'),'utf8').split('\n')){let r;try{r=JSON.parse(line)}catch{continue}if(r.type==='message'&&r.finish){const t=r.tokens;budget.accounted[r.id]=`${t.input}/${t.output}/${t.reasoning}/${t.cache.read}/${t.cache.write}/${r.finish}`;}}
   for(let i=0;i<2;i++)if(read(join(data,`continue-status-${ids[i]}.json`))?.status==='prepared')Object.assign(budget.sessions[pairs[i][2].id],{stage:'fallback',state:'ok',cost:0,limit:.1,fraction:0});publish();
  },100);
  const beforeAuto=requested,originals=pairs.map(([t,,chat],i)=>command(t,'auto-original-'+i,'prompt',{sid:chat.id}));
  originals.forEach(p=>void p.catch(()=>{}));await until(()=>requested>=beforeAuto+2,'two original streams before fallback');
  for(let i=0;i<2;i++){
   const [,row,chat]=pairs[i],inv=await until(()=>read(join(data,`models-${row.pid}.json`)),'owning inventory');
   Object.assign(budget.sessions[chat.id],{generation:ids[i],stage:'switching',state:'over',cost:1,limit:.5,fraction:2});publish();
   writeFileSync(join(data,`continue-${row.pid}-${ids[i]}.json`),JSON.stringify({id:ids[i],instance:inv.instance,session_id:chat.id,key:'smoke/cheap',children:[],issued:Date.now()/1000,input_price:.1,output_price:.4,context:200000,input_modalities:['text']}));
  }
  await until(()=>ids.every(id=>['confirmed','failed'].includes(read(join(data,`continue-status-${id}.json`))?.status)),'two fallback responses');
  for(const id of ids)assert.equal(read(join(data,`continue-status-${id}.json`)).status,'confirmed',JSON.stringify(read(join(data,`continue-status-${id}.json`))));
  await Promise.all(originals);await wait(1500);
  for(let i=0;i<2;i++){
   const [t,,chat]=pairs[i],state=await command(t,'auto-status-'+i,'status',{sid:chat.id});
   assert.equal(state.messages.findLast(m=>m.role==='assistant').modelID,'cheap');assert.ok(!state.messages.findLast(m=>m.role==='assistant').error);
   const layout=await command(t,'auto-layout-'+i,'layout');assert.ok(layout.some(r=>r.text.includes('Routing: Synthetic fallback [smoke]')),'effective routing absent from TUI');
   assert.ok(layout.some(r=>r.text.includes('LOCAL MOCK OK')&&r.y>=0&&r.y<80),'continuation response not visible');
  }
  clearInterval(accountingTimer);accountingTimer=null;
  Object.assign(budget.sessions[chat1.id],{state:'over',cost:.1,fraction:1});publish();const beforeCap=requested;
  const capped=await command(one,'fallback-capped','prompt',{sid:chat1.id});assert.equal(capped.data?.info?.error?.name,'MessageAbortedError');assert.equal(requested,beforeCap,'exhausted fallback reached provider');
  const uncapped=await command(two,'fallback-other','prompt',{sid:chat2.id});assert.ok(!uncapped.data?.info?.error);assert.equal(uncapped.data?.info?.modelID,'cheap');
  await command(one,'exit','exit');await until(()=>reports().find(r=>r.id===row1.id)?.closed,'normal exit tombstone');assert.equal(reports().find(r=>r.id===row2.id).closed,false);
  const summary={success:true,version:read(join(two.config,'loaded.json')).version,twoRealTUIs:true,twoAutomaticFallbacksConfirmed:true,effectiveRoutingVisible:true,independentFallbackCaps:true,registeredCompanion:true,beforeFirstChat:true,uniqueVisibleNames:true,nativeFollowupsKeepConversation:true,nativeEscapeInterrupts:true,paidBlockedBeforeProvider:true,otherTUIUntouched:true,targetedStopAcknowledged:true,onlySelectedStreamStopped:true,normalCloseReported:true,root};writeFileSync(join(root,'summary.json'),JSON.stringify(summary,null,2));console.log(JSON.stringify(summary));
 }finally{
  clearInterval(accountingTimer);for(const c of children)if(c.exitCode===null)c.kill();for(let i=0;i<logs.length;i++)writeFileSync(join(root,'tui-'+(i+1)+'.log'),logs[i].text);
  provider.closeAllConnections();await new Promise(r=>provider.close(r));
 }
})().catch(e=>{console.error(e);process.exitCode=1;});
