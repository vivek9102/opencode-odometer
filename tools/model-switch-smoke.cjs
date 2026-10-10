// Exercise the installed OpenCode executable against a local fake provider.
// No real provider requests, credentials, or live user data are used.
const {spawn}=require('node:child_process');
const {createServer}=require('node:http');
const {mkdirSync,writeFileSync,readFileSync,existsSync}=require('node:fs');
const {join,resolve}=require('node:path');
const assert=require('node:assert/strict');
const wait=ms=>new Promise(r=>setTimeout(r,ms));
const root=process.env.OPENCODE_SMOKE_DIR || resolve(__dirname,'../build/model-switch-smoke');
const freeBudget=process.env.OPENCODE_SMOKE_FREE_BUDGET==='1';
const autoFallback=process.env.OPENCODE_SMOKE_AUTO_FALLBACK==='1';
const unknownContext=autoFallback&&process.env.OPENCODE_SMOKE_UNKNOWN_CONTEXT==='1';
const freeFallback=autoFallback&&process.env.OPENCODE_SMOKE_FREE_FALLBACK==='1';
const atToolLimit=autoFallback&&process.env.OPENCODE_SMOKE_AT_TOOL_LIMIT==='1';
const historyChurn=autoFallback&&process.env.OPENCODE_SMOKE_HISTORY_CHURN==='1';
const exe=process.env.OPENCODE_SMOKE_EXE;
if(!exe)throw Error('Set OPENCODE_SMOKE_EXE');
async function freePort(){const server=createServer();await new Promise(r=>server.listen(0,'127.0.0.1',r));const port=server.address().port;await new Promise(r=>server.close(r));return port;}
(async()=>{
 const requested=[],providerInputs=[];let originalHeld=false,toolLimitGate=()=>{};
 const provider=createServer(async(req,res)=>{
  let raw='';for await(const chunk of req)raw+=chunk;
  if(!req.url.endsWith('/chat/completions')){res.writeHead(404).end();return;}
  const input=JSON.parse(raw);requested.push(input.model);providerInputs.push(input);
  const id='fake-'+requested.length,base={id,object:'chat.completion.chunk',created:Math.floor(Date.now()/1000),model:input.model};
  if(input.stream){
   res.writeHead(200,{'Content-Type':'text/event-stream'});
   const automaticTask=autoFallback&&input.tools?.some(t=>t.function?.name==='read')&&JSON.stringify(input.messages).includes('AUTO_FALLBACK_TASK');
   if(automaticTask&&input.model==='default'&&input.messages.some(m=>m.role==='tool')){
    originalHeld=true;res.write('data: '+JSON.stringify({...base,choices:[{index:0,delta:{role:'assistant',content:'Original turn pending'},finish_reason:null}]})+'\n\n');return;
   }
   if((freeBudget&&input.model==='selected'||automaticTask&&input.model==='default') && !input.messages.some(m=>m.role==='tool') || atToolLimit&&automaticTask&&input.model==='selected'&&!input.messages.some(m=>m.role==='tool'&&JSON.stringify(m.content).includes('LOCAL MOCK OK'))){
    if(atToolLimit&&input.model==='default')toolLimitGate();
    assert.ok(input.tools.some(t=>t.function?.name==='read'),'read tool unavailable');
    res.write('data: '+JSON.stringify({...base,choices:[{index:0,delta:{role:'assistant',tool_calls:[{index:0,id:'read-sample-'+requested.length,type:'function',function:{name:'read',arguments:JSON.stringify({filePath:join(root,'sample.txt')})}}]},finish_reason:null}]})+'\n\n');
    res.write('data: '+JSON.stringify({...base,choices:[{index:0,delta:{},finish_reason:'tool_calls'}],usage:{prompt_tokens:10,completion_tokens:3,total_tokens:13}})+'\n\n');res.end('data: [DONE]\n\n');return;
   }
   res.write('data: '+JSON.stringify({...base,choices:[{index:0,delta:{role:'assistant',content:'LOCAL MOCK OK'},finish_reason:null}]})+'\n\n');
   res.write('data: '+JSON.stringify({...base,choices:[{index:0,delta:{},finish_reason:'stop'}],usage:{prompt_tokens:10,completion_tokens:3,total_tokens:13}})+'\n\n');
   res.end('data: [DONE]\n\n');
  }else{res.writeHead(200,{'Content-Type':'application/json'}).end(JSON.stringify({...base,object:'chat.completion',choices:[{index:0,message:{role:'assistant',content:'LOCAL MOCK OK'},finish_reason:'stop'}],usage:{prompt_tokens:10,completion_tokens:3,total_tokens:13}}));}
 });
 await new Promise(r=>provider.listen(0,'127.0.0.1',r));
 const config=join(root,'config'),data=join(root,'odometer');mkdirSync(config,{recursive:true});mkdirSync(data,{recursive:true});
 if(freeBudget||autoFallback)writeFileSync(join(root,'sample.txt'),'LOCAL MOCK OK');
 writeFileSync(join(data,'preferences.json'),JSON.stringify({auto_start:false}));
 const plugin=join(config,'odometer.js');
 writeFileSync(plugin,readFileSync(join(__dirname,'../plugin/odometer.js'),'utf8').replace('const SERVER_POINTER_FILE = join(homedir(), ".opencode-odometer-server.json")','const SERVER_POINTER_FILE = process.env.OPENCODE_ODOMETER_SERVER_POINTER'));
 const model={tool_call:true,limit:{context:unknownContext?0:10000,output:100},cost:{input:1,output:1}};
 writeFileSync(join(config,'opencode.json'),JSON.stringify({autoupdate:false,model:'smoke/default',small_model:'smoke/default',permission:{read:'allow'},agent:{build:{model:'smoke/default'}},plugin:[plugin.replaceAll('\\','/')],enabled_providers:['smoke'],provider:{smoke:{npm:'@ai-sdk/openai-compatible',name:'Local smoke',options:{baseURL:`http://127.0.0.1:${provider.address().port}/v1`,apiKey:'local-test-only'},models:{default:{...model,name:'Default'},selected:{...model,name:'Selected',...(freeBudget||freeFallback ? {cost:{input:0,output:0}} : autoFallback ? {cost:{input:.1,output:.1}} : {})}}}}}));
 const env={...process.env,OPENCODE_TEST_HOME:join(root,'home'),XDG_CONFIG_HOME:join(root,'xdg-config'),XDG_DATA_HOME:join(root,'xdg-data'),XDG_CACHE_HOME:join(root,'xdg-cache'),XDG_STATE_HOME:join(root,'xdg-state'),OPENCODE_CONFIG_DIR:config,OPENCODE_DISABLE_PROJECT_CONFIG:'1',OPENCODE_DISABLE_MODELS_FETCH:'1',OPENCODE_DISABLE_DEFAULT_PLUGINS:'1',OPENCODE_ODOMETER_DIR:data,OPENCODE_ODOMETER_HOME:data,OPENCODE_ODOMETER_POINTER:join(data,'pointer.json'),OPENCODE_ODOMETER_SERVER_POINTER:join(data,'server.json')};
 let child,base,logs='',eventController,eventTask,accountingTimer;
 const events=[];
 async function watchEvents(){
  eventController=new AbortController();
  const response=await fetch(base+'/event',{signal:eventController.signal});
  assert.ok(response.ok);
  eventTask=(async()=>{
   let buffer='';const decoder=new TextDecoder();
   for await(const chunk of response.body){
    buffer+=decoder.decode(chunk,{stream:true});
    let end;while((end=buffer.indexOf('\n'))>=0){
     const line=buffer.slice(0,end).trim();buffer=buffer.slice(end+1);
     if(line.startsWith('data: '))events.push(JSON.parse(line.slice(6)));
    }
   }
  })().catch(error=>{if(!eventController.signal.aborted)throw error});
 }
 const limitToasts=()=>events.filter(e=>e.type==='tui.toast.show'&&/limit|budget|hard stop/i.test(e.properties?.title||''));
 async function waitForToasts(count){for(let n=0;n<100&&limitToasts().length<count;n++)await wait(20);assert.equal(limitToasts().length,count,'limit snackbar missing or duplicated');}
 async function start(){
  const port=await freePort();base=`http://127.0.0.1:${port}`;
  child=spawn(exe,['serve','--hostname','127.0.0.1','--port',String(port),'--print-logs','--log-level','DEBUG'],{cwd:root,env,windowsHide:true,stdio:['ignore','pipe','pipe']});
  child.stdout.on('data',b=>logs+=b);child.stderr.on('data',b=>logs+=b);
  const startupDeadline=Date.now()+120000;
  while(Date.now()<startupDeadline){
   if(child.exitCode!==null)throw Error('OpenCode exited: '+logs.slice(-1000));
   try{const r=await fetch(base+'/provider',{signal:AbortSignal.timeout(1000)});if(r.ok){const d=await r.json();writeFileSync(join(root,'providers.json'),JSON.stringify({connected:d.connected,all:d.all?.map(p=>({id:p.id,models:Object.keys(p.models||{})}))}));}}catch{}
   const inventory=join(data,`models-${child.pid}.json`);
   if(existsSync(inventory)&&JSON.parse(readFileSync(inventory)).models.length)return;
   await wait(250);
  }
  throw Error('OpenCode inventory timed out');
 }
 async function stop(){if(!child||child.exitCode!==null)return;const ended=new Promise(r=>child.once('exit',r));child.kill();await Promise.race([ended,wait(2000)]);}
 async function api(path,body){const response=await fetch(base+path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body),signal:AbortSignal.timeout(30000)});assert.ok(response.ok,'HTTP '+response.status+' '+await response.clone().text());return response.json();}
 async function prompt(sid){return api('/session/'+sid+'/message',{agent:'build',model:{providerID:'smoke',modelID:'default'},parts:[{type:'text',text:freeBudget ? 'Read sample.txt using read and reply LOCAL MOCK OK.' : 'Reply with LOCAL MOCK OK. Do not use tools.'}]});}
 const queue=(sid,id,key='smoke/selected')=>writeFileSync(join(data,`switch-session-${sid}.json`),JSON.stringify({id,session_id:sid,key,persistent:true,issued:Math.floor(Date.now()/1000)}));
 try{
  await start();const firstPID=child.pid;
  const chat=await api('/session',{title:'Model routing smoke'}),other=await api('/session',{title:'Other chat'});
  if(autoFallback){
   const inventory=JSON.parse(readFileSync(join(data,`models-${child.pid}.json`))),id='automatic-fallback-smoke';
   const budgetFile=join(data,'budget.json');
   const budget={enabled:true,updated:Date.now()/1000,mode:'hard',block_when_exceeded:true,accounted:{},free_models:freeFallback?['smoke/selected']:[],sessions:{[chat.id]:{strict:true,enforced:true,stage:'original',state:'ok',cost:0,limit:1,fraction:0,original_model:'smoke/default',fallback_model:'smoke/selected'}}};
   const publish=()=>{budget.updated=Date.now()/1000;const temp=budgetFile+'.tmp';writeFileSync(temp,JSON.stringify(budget));try{require('node:fs').renameSync(temp,budgetFile)}catch(e){if(!['EPERM','EACCES','EBUSY'].includes(e.code))throw e;}};publish();
   toolLimitGate=()=>{Object.assign(budget.sessions[chat.id],{state:'over',cost:1,limit:.5,fraction:2});publish()};
   accountingTimer=setInterval(()=>{
    if(existsSync(join(data,'events.jsonl')))for(const line of readFileSync(join(data,'events.jsonl'),'utf8').split('\n')){let r;try{r=JSON.parse(line)}catch{continue}if(r.type==='message'&&r.finish){const t=r.tokens;budget.accounted[r.id]=`${t.input}/${t.output}/${t.reasoning}/${t.cache.read}/${t.cache.write}/${r.finish}`;if(atToolLimit&&r.sessionID===chat.id&&budget.sessions[chat.id].stage==='original')Object.assign(budget.sessions[chat.id],{state:'over',cost:1,limit:.5,fraction:2})}}
    const ack=join(data,`continue-status-${id}.json`);if(existsSync(ack)&&JSON.parse(readFileSync(ack)).status==='prepared'){Object.assign(budget.sessions[chat.id],{stage:'fallback',state:'ok',cost:0,limit:.1,fraction:0})}publish();
   },100);
   const original=api('/session/'+chat.id+'/message',{agent:'build',model:{providerID:'smoke',modelID:'default'},parts:[{type:'text',text:'AUTO_FALLBACK_TASK: Read sample.txt using read, then report its contents.'}]});
   // Attach rejection handling while the fake stream is deliberately held;
   // a failed assertion must still reach fixture cleanup and retain logs.
   void original.catch(()=>{});
   if(atToolLimit){await original;const stopped=await (await fetch(base+'/session/'+chat.id+'/message')).json();assert.ok(stopped.flatMap(m=>m.parts||[]).some(p=>p.type==='tool'&&p.state.status==='error'),'budget did not reject the original tool')}
   else {for(let n=0;n<150&&!originalHeld;n++)await wait(100);assert.ok(originalHeld,'original turn never reached the post-tool model request')}
   if(historyChurn){
    writeFileSync(join(data,'tui-smoke-open.json'),JSON.stringify({id:'smoke-open',pid:child.pid,session_id:chat.id,closed:false}));
    await wait(1100);
    for(let i=0;i<105;i++)await api('/session',{title:'Unrelated history '+i});
    await wait(1100);
    assert.ok(JSON.parse(readFileSync(join(data,`models-${child.pid}.json`))).sessions.includes(chat.id),'open TUI ownership lost after history churn');
   }
   Object.assign(budget.sessions[chat.id],{generation:id,stage:'switching',state:'over',cost:1,limit:.5,fraction:2});publish();
   const req={id,instance:inventory.instance,session_id:chat.id,key:'smoke/selected',children:[],issued:Date.now()/1000,input_price:freeFallback?0:.1,output_price:freeFallback?0:.1,context:unknownContext?0:10000,input_modalities:['text']};
   writeFileSync(join(data,`continue-${child.pid}-${id}.json`),JSON.stringify(req));
   let ack;for(let n=0;n<200;n++){try{ack=JSON.parse(readFileSync(join(data,`continue-status-${id}.json`)))}catch{}if(['confirmed','failed','completed'].includes(ack?.status))break;await wait(100)}
   assert.equal(ack?.status,'confirmed',JSON.stringify(ack));await original;
   const all=await (await fetch(base+'/session/'+chat.id+'/message')).json(),tools=all.flatMap(m=>m.parts||[]).filter(p=>p.type==='tool'&&p.tool==='read');
   assert.equal(tools.filter(p=>p.state.status==='completed').length,1,'tool executed more than once');assert.equal(tools.length,atToolLimit?2:1,'completed tool was repeated');
   const continuations=all.filter(m=>m.info.role==='user'&&(m.parts||[]).some(p=>p.text?.includes('[Odometer automatic budget continuation]')));assert.equal(continuations.length,1);
   const answer=all.findLast(m=>m.info.role==='assistant');assert.equal(answer.info.modelID,'selected');assert.equal(answer.info.error,undefined);
   writeFileSync(join(data,`continue-${child.pid}-${id}.json`),JSON.stringify(req));await wait(1200);const again=await (await fetch(base+'/session/'+chat.id+'/message')).json();assert.equal(again.filter(m=>m.info.role==='user').length,2,'duplicate continuation');
   const following=await prompt(chat.id);assert.equal(following.info.modelID,'selected');const afterManual=await (await fetch(base+'/session/'+chat.id+'/message')).json();assert.equal(afterManual.filter(m=>m.info.role==='user').at(-1).info.id,following.info.parentID,'continuation ID broke subsequent message ordering');
   const otherResponse=await prompt(other.id);assert.equal(otherResponse.info.modelID,'default');assert.equal(child.exitCode,null);
   const result={success:true,sameConversation:true,cancellationAcknowledged:true,completedToolPreserved:!atToolLimit,budgetBlockedToolResumed:atToolLimit,openTUIOwnershipAfterHistoryChurn:historyChurn,oneContinuation:true,actualFallbackConfirmed:true,duplicateSuppressed:true,otherChatUntouched:true,providerRequests:requested};writeFileSync(join(root,'results.json'),JSON.stringify(result,null,2));console.log(JSON.stringify(result));return;
  }
  if(freeBudget){
   await watchEvents();
   await prompt(other.id); // Warm OpenCode's isolated project before testing hooks.
   const budget={enabled:true,updated:Date.now()/1000,mode:'hard',free_models:['smoke/selected'],sessions:{[chat.id]:{state:'over',enforced:true,cost:2,limit:1,fraction:2,past_hard_stop:true}}};
   const budgetFile=join(data,'budget.json');writeFileSync(budgetFile,JSON.stringify(budget));queue(chat.id,'free-after-limit');
   const selected=await prompt(chat.id);assert.equal(selected.info.modelID,'selected');assert.equal(selected.info.error,undefined);
   const messages=await (await fetch(base+'/session/'+chat.id+'/message')).json();
   const reads=messages.flatMap(m=>m.parts||[]).filter(p=>p.type==='tool'&&p.tool==='read');
   assert.ok(reads.some(p=>p.state?.status==='completed'),'free tool was blocked by the exhausted budget');
   const later=await prompt(chat.id);assert.equal(later.info.modelID,'selected');assert.equal(later.info.error,undefined);
   const callsBefore=requested.length,toastsBefore=limitToasts().length;queue(chat.id,'back-to-paid','smoke/default');
   const blocked=await prompt(chat.id);
   assert.equal(blocked.info.error?.name,'MessageAbortedError','budget refusal escaped OpenCode response handling');
   assert.ok(blocked.info.error.data.message.includes('Choose a free model'));
   await waitForToasts(toastsBefore+1);
   const blockedAgain=await prompt(chat.id);assert.equal(blockedAgain.info.error?.name,'MessageAbortedError');
   await waitForToasts(toastsBefore+2);
   assert.equal(requested.length,callsBefore,'a paid model reached the provider after the exhausted limit');
   queue(chat.id,'free-again');const recovered=await prompt(chat.id);assert.equal(recovered.info.modelID,'selected');assert.equal(recovered.info.error,undefined,'blocked paid request broke the chat');
   const after=JSON.parse(readFileSync(budgetFile));assert.equal(after.enabled,true);assert.equal(after.sessions[chat.id].cost,2);assert.equal(after.sessions[chat.id].limit,1);
   queue(chat.id,'paid-after-raise','smoke/default');budget.sessions[chat.id]={state:'ok',enforced:true,cost:2,limit:5,fraction:.4,past_hard_stop:false};writeFileSync(budgetFile,JSON.stringify(budget));
   const resumed=await prompt(chat.id);assert.equal(resumed.info.modelID,'default');assert.equal(resumed.info.error,undefined,'raising the limit did not resume paid work');
   const untouched=await prompt(other.id);assert.equal(untouched.info.modelID,'default');assert.equal(untouched.info.error,undefined);
   assert.equal(child.exitCode,null,'OpenCode exited on a refused paid request');
   const result={success:true,freeAfterLimit:true,freeToolCompleted:true,laterFreeTurn:true,paidBlocked:true,handledCancellation:true,repeatSnackbar:true,freeAfterBlockedPaid:true,budgetPreserved:true,paidAfterRaise:true,otherChatUntouched:true,providerRequests:requested};writeFileSync(join(root,'results.json'),JSON.stringify(result,null,2));console.log(JSON.stringify(result));return;
  }
  queue(chat.id,'restart-choice');
  await stop();await start();assert.notEqual(child.pid,firstPID);
  // A different chat must retain its default and leave the selection intact.
  const otherResult=await prompt(other.id);assert.equal(otherResult.info.modelID,'default');
  assert.ok(existsSync(join(data,`switch-session-${chat.id}.json`)));
  const selected=await prompt(chat.id);assert.equal(selected.info.modelID,'selected');
  assert.equal(selected.info.providerID,'smoke');
  assert.ok(requested.includes('selected'),'the provider did not receive the chosen model');
  const status=join(data,'switch-status-restart-choice.json');
  for(let n=0;n<30&&(!existsSync(status)||JSON.parse(readFileSync(status)).status!=='confirmed');n++)await wait(100);
  assert.equal(JSON.parse(readFileSync(status)).status,'confirmed');
  assert.equal(existsSync(join(data,`switch-session-${chat.id}.json`)),true);
  const later=await prompt(chat.id);assert.equal(later.info.modelID,'selected','the next turn reverted to its default');
  await stop();await start();
  const resumed=await prompt(chat.id);assert.equal(resumed.info.modelID,'selected','restarting lost the active chat model');
  queue(chat.id,'replace-choice','smoke/default');
  const replaced=await prompt(chat.id);assert.equal(replaced.info.modelID,'default','choosing another model did not replace the chat choice');
  require('node:fs').rmSync(join(data,`switch-session-${chat.id}.json`));
  const following=await prompt(chat.id);assert.equal(following.info.modelID,'default');
  const result={success:true,restarted:true,firstPID,nextPID:child.pid,otherChat:otherResult.info.modelID,nextTurn:selected.info.modelID,laterTurn:later.info.modelID,resumedTurn:resumed.info.modelID,replacedTurn:replaced.info.modelID,followOpenCode:following.info.modelID,confirmed:true,providerRequests:requested};
  writeFileSync(join(root,'results.json'),JSON.stringify(result,null,2));console.log(JSON.stringify(result));
 }finally{clearInterval(accountingTimer);eventController?.abort();await eventTask;await stop();writeFileSync(join(root,'console.log'),logs);provider.closeAllConnections();await new Promise(r=>provider.close(r));}
})().catch(e=>{console.error(e);process.exitCode=1});
