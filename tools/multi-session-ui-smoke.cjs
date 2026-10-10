// Browser rendering and interactions with synthetic Wails data only.
const assert=require('node:assert/strict');
const {chromium}=require('playwright');
const {createServer}=require('node:http');
const {readFileSync,mkdirSync}=require('node:fs');
const {join}=require('node:path');
(async()=>{
 const root=join(__dirname,'../frontend/dist'),out=join(__dirname,'../build/multi-session-ui');mkdirSync(out,{recursive:true});
 const server=createServer((req,res)=>{const file=req.url==='/'?'index.html':req.url.slice(1);if(!/^[\w.-]+$/.test(file)){res.writeHead(404).end();return;}try{res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(readFileSync(join(root,file)));}catch{res.writeHead(404).end();}});
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 let browser;try{browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE||chromium.executablePath()});}catch(e){await new Promise(r=>server.close(r));throw e;}
 const page=await browser.newPage({viewport:{width:800,height:780}}),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.addInitScript(()=>{
  const row=(id,name,cost,limit,mode='hard')=>({id,name,session_id:'chat-'+id,model:'mock/model-'+id,cost,spent:cost,limit,mode,enabled:limit>0,fraction:limit?cost/limit:0,state:!limit?'ok':cost>=limit?'over':cost>=limit*.75?'warn':'ok',enforced:limit>0,active:false});
  window.testSnap={view:'TRIP',cost:2.6883,open_cost:2.6883,total_cost:2.6883,trip_cost:2.6883,saved:132.05232,rate:0,tokens:52341000,seeded:true,active:false,compact:false,connected:true,session_id:'chat-one',selected_session:'one',open_sessions:[row('one','auth-refactor',1.07,.5),row('two','migration-db',.41,.5,'soft'),row('three','api-cleanup',1.2083,0)],rows:Array.from({length:30},(_,i)=>({key:'mock/model-'+i,msgs:20,in:1800000,out:120400,cache:41200000,cost:.1,saved:0})),sparkline:Array(30).fill(.01),preferences:{idle_dim:true,remaining:false},unpriced_models:0,charge:{sequence:0,cost:0}};
  window.calls=[];window.budgetResolvers=[];window.selectionResolvers=[];const emit=()=>window.refreshHandler?.({...testSnap});
  window.runtime={EventsOn:(name,cb)=>window.refreshHandler=cb};
  window.go={wservice:{Service:{Snapshot:async()=>({...testSnap}),SetScreenSize:async()=>{},SetCompact:async on=>{calls.push(['compact',on]);testSnap.compact=on;emit();},SetPeek:async()=>{},FallbackModels:async()=>[],SelectOpenSession:async id=>{calls.push(['select',id]);if(window.holdSelections)await new Promise(resolve=>selectionResolvers.push(resolve));testSnap.selected_session=id;emit();},
   ReconcileWindowLayout:async()=>{calls.push(['reconcile']);},
   SetOpenSessionBudget:async(id,limit,mode,enabled)=>{calls.push(['budget',id,limit,mode,enabled]);if(window.holdBudgets)await new Promise(resolve=>budgetResolvers.push(resolve));const r=testSnap.open_sessions.find(r=>r.id===id);Object.assign(r,{limit,mode,enabled,fraction:limit?r.cost/limit:0,state:!enabled?'ok':r.cost>=limit?'over':r.cost>=limit*.75?'warn':'ok'});emit();},
   FallbackModels:async()=>[{key:'mock/cheap',provider:'mock',category:'chat',eligible:true,cheaper:true,name:'Cheap coding model',input:.1,output:.4,ratio:.05,metadata:{coding:51.2,intelligence:48.1,reasoning:true,speed:100,source:'Artificial Analysis',benchmark_id:'example-benchmark'},tags:['tools','coding','reasoning']},{key:'mock/unrated',provider:'mock',category:'chat',eligible:true,cheaper:true,name:'Compact chat model',input:.05,output:.2,ratio:.025,metadata:{},tags:['tools']},{key:'mock/free',provider:'mock',category:'chat',eligible:true,cheaper:true,name:'Free model',input:0,output:0,free:true,metadata:{},tags:['tools','free']},{key:'other/embedding',provider:'other',category:'embedding',eligible:false,cheaper:true,name:'Embedding model',input:.01,output:0,metadata:{},reason:'Specialised model; cannot continue a chat.'},{key:'mock/expensive',provider:'mock',category:'chat',eligible:false,cheaper:false,name:'Expensive model',input:10,output:30,metadata:{},reason:'This configured route is not cheaper.'}],
   SetCounter:async view=>{testSnap.view=view;testSnap.cost=view==='RUN'?testSnap.open_cost:view==='TRIP'?testSnap.trip_cost:testSnap.total_cost;emit();},
   SetOpenSessionRule:async(id,rule)=>{calls.push(['rule',id,rule]);Object.assign(testSnap.open_sessions.find(r=>r.id===id),rule,{fallback_free:rule.fallback_model==='mock/free'});emit();},
   SetRuleToastSize:async height=>{calls.push(['rule-toast-size',height]);testSnap.peek=height>0;testSnap.peek_height=height||46;},
   ResumeOpenSession:async id=>{calls.push(['resume',id]);const r=testSnap.open_sessions.find(r=>r.id===id);Object.assign(r,{limit:r.limit+.5,stopped:false,manual_stop:false,on_fallback:false,stage:'original',fallback_spent:0,detail:'Budget increased'});r.fraction=r.cost/r.limit;r.state=r.fraction>=1?'over':r.fraction>=.75?'warn':'ok';emit();},
   IncreaseOpenSessionBudget:async(id,amount)=>{calls.push(['increase',id,amount]);const r=testSnap.open_sessions.find(r=>r.id===id);r.limit+=amount;r.fraction=r.cost/r.limit;emit();},
   StopOpenSession:async id=>{calls.push(['stop',id]);},SetPreferences:async p=>{testSnap.preferences=p;emit();},
   UnpricedModels:async()=>[],PendingPrices:async()=>[],PendingPriceSuggestions:async()=>[],PricesAgeHours:async()=>0,
   AvailableModels:async()=>[],PendingModelSwitch:async()=>({}),ToggleViewMode:async()=>{},ResetTrip:async()=>{},RefreshPrices:async()=>'',HideToTray:async()=>{},ExportCsv:async()=>'',
  }}};
 });
 try{
  await page.goto(`http://127.0.0.1:${server.address().port}/`);await page.locator('.session-row').first().waitFor();
  assert.equal(await page.locator('.session-row').count(),3);assert.equal(await page.locator('#selected-name').innerText(),'auth-refactor');
  assert.equal(await page.locator('.utility-row button:visible').count(),7,'requested footer action missing');
  const globalDigits=await page.locator('#bd-odometer').innerText();
  await page.evaluate(()=>{testSnap.preferences.remaining=true;render(testSnap);});
  assert.equal(await page.locator('#bd-odometer').innerText(),globalDigits,'selected cap replaced the global readout');
  assert.equal(await page.locator('#bd-reading').innerText(),'spent');
  await page.evaluate(()=>{testSnap.preferences.remaining=false;render(testSnap);});
  await page.locator('.session-row[data-id="two"]').click();assert.equal(await page.locator('#selected-name').innerText(),'migration-db');
  assert.equal(await page.locator('.session-row.selected .session-tag').innerText(),'▸ SELECTED');assert.equal(await page.locator('#bd-tbody tr').count(),30);
  await page.locator('#bd-limit').fill('2');await page.locator('#bd-limit').press('Enter');await page.waitForFunction(()=>calls.some(c=>c[0]==='budget'));
  assert.deepEqual(await page.evaluate(()=>calls.find(c=>c[0]==='budget')),['budget','two',2,'hard',true]);
  await page.locator('#bd-stop').click();assert.deepEqual(await page.evaluate(()=>calls.find(c=>c[0]==='stop')),['stop','two']);
  // Draft popup, immediate settings and Esc-like cancellation at the original size.
  await page.evaluate(()=>window.beforeRules=JSON.stringify(testSnap));
  await page.locator('[data-counter="RUN"]').click();assert.equal(await page.locator('#bd-mode').innerText(),'RUN');assert.equal(await page.locator('#bd-reset').isVisible(),false);
  await page.locator('[data-counter="TRIP"]').click();assert.ok(await page.locator('#bd-reset').isVisible());
  assert.equal(await page.locator('#bd-mode-sel').isVisible(),false);
  const openPicker=async()=>{await page.locator('[data-rule="switch"]').click();await page.locator('#fallback-provider-filter option[value="other"]').waitFor({state:'attached'});};
  await openPicker();assert.equal(await page.locator('.fallback-model').count(),4);
  assert.doesNotMatch(await page.locator('#fallback-models').innerText(),/Coding 51.2|Not rated|Benchmarks/);
  assert.equal(await page.locator('#fallback-models .model-card .model-desc').count(),4);
  assert.match(await page.locator('[data-model="mock/cheap"]').locator('..').locator('.model-price').innerText(),/\$0.10 in \/ \$0.40 out\s+per 1M/);
  assert.ok(await page.locator('[data-model="other/embedding"]').isDisabled());assert.equal(await page.locator('#fallback-count').innerText(),'4 of 5 configured models');
  await page.locator('#fallback-cost-filter').selectOption('all');assert.equal(await page.locator('.fallback-model').count(),5);assert.ok(await page.locator('[data-model="mock/expensive"]').isDisabled());await page.locator('#fallback-cost-filter').selectOption('both');
  await page.locator('#fallback-provider-filter').selectOption('other');assert.equal(await page.locator('.fallback-model').count(),1);await page.locator('#fallback-provider-filter').selectOption('all');
  await page.locator('#fallback-search').fill('coding');assert.equal(await page.locator('.fallback-model').count(),1);await page.locator('#fallback-search').fill('');
  await page.locator('[data-model="mock/cheap"]').click();assert.ok(await page.locator('#fallback-allowance').isVisible());assert.match(await page.locator('#fallback-chain').innerText(),/extra \$0.05/);
  assert.equal(await page.evaluate(()=>calls.filter(c=>c[0]==='rule').length),0,'draft saved before confirmation');
  await page.evaluate(()=>document.getElementById('toast').classList.add('hidden'));await page.screenshot({path:join(out,'fallback-picker.png')});
  await page.locator('#fallback-cancel').click();assert.equal(await page.locator('#fallback-modal').isVisible(),false);assert.ok(await page.locator('[data-rule="stop"]').evaluate(e=>e.classList.contains('on')));
  for(const close of ['x','escape','backdrop']){await openPicker();await page.locator('[data-model="mock/free"]').click();if(close==='x')await page.locator('#fallback-close').click();else if(close==='escape')await page.locator('#fallback-search').press('Escape');else await page.locator('#fallback-modal .modal-backdrop').click({position:{x:5,y:5}});assert.equal(await page.locator('#fallback-modal').isVisible(),false);}
  assert.equal(await page.evaluate(()=>calls.filter(c=>c[0]==='rule').length),0);
  await openPicker();await page.locator('[data-model="mock/cheap"]').click();await page.locator('#fallback-limit').fill('.05');await page.locator('[data-then="stop"]').click();await page.locator('#save-rule').click();await page.waitForFunction(()=>calls.some(c=>c[0]==='rule'));await page.locator('#fallback-modal').waitFor({state:'hidden'});
  assert.deepEqual(await page.evaluate(()=>calls.find(c=>c[0]==='rule')),['rule','two',{rule:'switch',fallback_model:'mock/cheap',fallback_limit:.05,then:'stop'}]);
  const footerFits=await page.evaluate(()=>Object.fromEntries(['board','session-list','bd-table-wrap'].map(id=>[id,document.getElementById(id).getBoundingClientRect().toJSON()]).concat(['.utility-row','.fixed-head-region','.fixed-footer-region','.budget-row'].map(sel=>[sel,document.querySelector(sel).getBoundingClientRect().toJSON()]))));assert.ok(footerFits['.utility-row'].bottom<=780&&footerFits['.fixed-head-region'].top>=0,'compact controls overflow: '+JSON.stringify(footerFits));
  await page.evaluate(()=>document.getElementById('toast').classList.add('hidden'));await page.screenshot({path:join(out,'expanded-rules.png')});
  await page.locator('#limit-choice-note button').click();assert.ok(await page.locator('#fallback-modal').isVisible());await page.locator('#fallback-cancel').click();
  await page.locator('[data-rule="stop"]').click();await page.waitForFunction(()=>testSnap.open_sessions[1].rule==='stop');assert.equal(await page.locator('#fallback-modal').isVisible(),false);
  await openPicker();await page.locator('#fallback-cost-filter').selectOption('free');assert.equal(await page.locator('.fallback-model').count(),1);await page.locator('[data-model="mock/free"]').click();assert.ok(await page.locator('#fallback-free-note').isVisible());assert.equal(await page.locator('#fallback-allowance').isVisible(),false);await page.locator('#save-rule').click();await page.locator('#fallback-modal').waitFor({state:'hidden'});assert.match(await page.locator('#limit-choice-note').innerText(),/free, no cap/);
  await page.evaluate(()=>{testSnap.open_sessions[1].enabled=false;render(testSnap);});assert.match(await page.locator('#limit-choice-note').innerText(),/No limit set/);assert.doesNotMatch(await page.locator('#limit-choice-note').innerText(),/Switches/);await page.evaluate(()=>{testSnap.open_sessions[1].enabled=true;render(testSnap);});
  await page.setViewportSize({width:800,height:660});assert.ok(await page.locator('.utility-row').evaluate(e=>e.getBoundingClientRect().bottom<=innerHeight));await openPicker();assert.ok(await page.locator('.fallback-dialog').evaluate(e=>e.getBoundingClientRect().top>=0&&e.getBoundingClientRect().bottom<=innerHeight));await page.locator('#fallback-cancel').click();await page.setViewportSize({width:800,height:780});
  await page.evaluate(()=>{Object.assign(testSnap.open_sessions[0],{enabled:false,state:'ok',fraction:0});Object.assign(testSnap.open_sessions[1],{on_fallback:true,stage:'fallback',state:'fallback',fallback_spent:.02,fallback_limit:.05,fallback_model:'mock/cheap',fallback_free:false});testSnap.compact=true;render(testSnap);});await page.setViewportSize({width:340,height:46});
  await page.evaluate(()=>{testSnap.compact=false;render(testSnap);});await page.setViewportSize({width:800,height:780});
  assert.equal(await page.locator('#bd-limit-kind').innerText(),'ORIGINAL $');assert.equal(await page.locator('#bd-limit').inputValue(),'2');assert.match(await page.locator('#bd-budget-label').innerText(),/Fallback.*\$0.02.*\$0.05/);
  await page.evaluate(()=>{testSnap.compact=true;render(testSnap);});await page.setViewportSize({width:340,height:46});
  assert.match(await page.locator('#bar-model').innerText(),/↓ migration-db/);assert.ok(await page.locator('#bar').evaluate(e=>e.getBoundingClientRect().width===340&&e.getBoundingClientRect().height===46));await page.screenshot({path:join(out,'docked-fallback.png')});
  await page.evaluate(()=>{Object.assign(testSnap.open_sessions[1],{stopped:true,rule_event:'test-stopped-event',detail:'Fallback allowance reached.'});render(testSnap);});await page.setViewportSize({width:340,height:178});assert.ok(await page.locator('#rule-toast').isVisible());assert.equal(await page.locator('#bar-rate').innerText(),'$0.02 / $0.05');assert.equal(await page.locator('#bar').evaluate(e=>e.getBoundingClientRect().height),46);await page.screenshot({path:join(out,'docked-stopped-toast.png')});
  assert.doesNotMatch(await page.locator('#rule-toast-actions').innerText(),/Resume|Undo/);await page.locator('#rule-toast-actions button').filter({hasText:'Dismiss'}).click();assert.equal(await page.locator('#rule-toast').isVisible(),false);
  await page.evaluate(()=>{Object.assign(testSnap.open_sessions[1],{stopped:false,rule_event:'test-switch-event',detail:'Switched to cheap · continuing.'});render(testSnap);});await page.locator('#rule-toast-actions button').filter({hasText:'Stop session'}).click();assert.deepEqual(await page.evaluate(()=>calls.filter(c=>c[0]==='stop').at(-1)),['stop','two']);assert.equal(await page.evaluate(()=>testSnap.open_sessions[1].limit),2,'manual cancellation changed allowance');
  await page.evaluate(()=>{testSnap=JSON.parse(beforeRules);render(testSnap);});await page.setViewportSize({width:800,height:780});
  // Polling retains row nodes/focus, even if a charge updates their text.
  assert.ok(await page.locator('.session-row[data-id="two"]').evaluate(button=>{button.focus();const original=button;testSnap.open_sessions[1].cost=.42;render(testSnap);return original===document.querySelector('.session-row[data-id="two"]')&&document.activeElement===original;}));
  // Late selection events cannot flash the previously selected footer.
  await page.evaluate(()=>{window.holdSelections=true;});
  await page.locator('.session-row[data-id="one"]').click();await page.waitForFunction(()=>selectionResolvers.length===1);
  await page.locator('.session-row[data-id="two"]').click();
  await page.evaluate(()=>render({...testSnap,selected_session:'one'}));
  assert.equal(await page.locator('#selected-name').innerText(),'migration-db');
  await page.evaluate(()=>selectionResolvers.shift()());await page.waitForFunction(()=>selectionResolvers.length===1);
  assert.equal(await page.locator('#selected-name').innerText(),'migration-db');
  await page.evaluate(()=>{window.holdSelections=false;selectionResolvers.shift()();});await page.waitForFunction(()=>testSnap.selected_session==='two');
  // A checked empty limit stays selected while waiting for its amount.
  await page.evaluate(()=>{const base=testSnap.open_sessions[2];testSnap.open_sessions.push({...base,id:'draft',name:'checkbox-test',session_id:'draft-chat',spent:0,cost:0});render(testSnap);});
  await page.locator('.session-row[data-id="draft"]').click();
  await page.locator('#bd-enabled').check();await page.evaluate(()=>render(testSnap));
  assert.ok(await page.locator('#bd-enabled').isChecked());assert.match(await page.locator('#session-limit-note').innerText(),/greater than zero/);
  assert.equal(await page.evaluate(()=>calls.some(c=>c[0]==='budget'&&c[1]==='draft')),false,'checking an empty amount silently saved off');
  await page.evaluate(()=>{window.holdBudgets=true;});await page.locator('#bd-limit').fill('.75');await page.locator('#bd-limit').press('Enter');
  await page.waitForFunction(()=>budgetResolvers.length===1);await page.evaluate(()=>render(testSnap));
  assert.ok(await page.locator('#bd-enabled').isChecked());assert.equal(await page.locator('#bd-limit').inputValue(),'.75');
  await page.locator('#bd-enabled').uncheck();await page.evaluate(()=>budgetResolvers.shift()());await page.waitForFunction(()=>budgetResolvers.length===1);
  assert.equal(await page.locator('#bd-enabled').isChecked(),false,'old enable acknowledgement flickered the checkbox');
  await page.evaluate(()=>{window.holdBudgets=false;budgetResolvers.shift()();});await page.waitForFunction(()=>!budgetEdits.has('draft'));
  assert.equal(await page.locator('#bd-enabled').isChecked(),false);
  // Entering an allowance commits when DOCK blurs the field. Focus/typing
  // after restoring the compact window must never save a disabled allowance.
  await page.locator('#bd-enabled').check();await page.locator('#bd-limit').fill('.25');
  await page.locator('#bd-dock').click();await page.waitForFunction(()=>testSnap.open_sessions.find(r=>r.id==='draft').enabled&&!budgetEdits.has('draft'));
  await page.setViewportSize({width:340,height:46});
  const beforeRestore=await page.evaluate(()=>calls.length);
  await page.evaluate(()=>{document.hasFocus=()=>true;window.dispatchEvent(new Event('focus'));});
  await page.waitForFunction(n=>calls.slice(n).some(c=>c[0]==='reconcile'),beforeRestore);
  await page.keyboard.type('continue task ');
  assert.equal(await page.evaluate(()=>testSnap.open_sessions.find(r=>r.id==='draft').limit),.25);
  assert.equal(await page.evaluate(()=>calls.slice(calls.findLastIndex(c=>c[0]==='budget')).filter(c=>c[0]==='budget'&&c[4]===false).length),0,'docking/typing cleared the allowance');
  // Restore can resize the native rectangle before WebView receives focus.
  const beforeResize=await page.evaluate(()=>calls.length);
  await page.setViewportSize({width:800,height:780});
  await page.waitForFunction(n=>calls.slice(n).some(c=>c[0]==='reconcile'),beforeResize);
  assert.equal(await page.evaluate(()=>testSnap.open_sessions.find(r=>r.id==='draft').enabled),true);
  await page.locator('#bar-expand').click();await page.setViewportSize({width:800,height:780});
  // Amount first, then ENABLE: blur must not auto-check before click toggles.
  await page.locator('.session-row[data-id="draft"]').click();
  await page.locator('#bd-enabled').uncheck();await page.waitForFunction(()=>!budgetEdits.has('draft'));
  const beforeAmount=await page.evaluate(()=>calls.length);
  await page.locator('#bd-limit').fill('.2');await page.locator('#bd-enabled').click();
  await page.waitForFunction(()=>!budgetEdits.has('draft'));
  assert.ok(await page.locator('#bd-enabled').isChecked(),'amount blur reversed the ENABLE click');
  assert.deepEqual(await page.evaluate(n=>calls.slice(n).filter(c=>c[0]==='budget'),beforeAmount),[['budget','draft',.2,'hard',true]]);
  await page.locator('#bd-dock').click();assert.equal(await page.evaluate(()=>testSnap.open_sessions.find(r=>r.id==='draft').enabled),true);
  // Reproduce WebView's resize-before-snapshot ordering. Recovery must never
  // replay the stale compact value as a user command.
  await page.setViewportSize({width:340,height:46});
  await page.evaluate(()=>{
    window.originalSetCompact=window.go.wservice.Service.SetCompact;
    window.go.wservice.Service.SetCompact=async on=>{
      calls.push(['delayed-compact',on]);window.dispatchEvent(new Event('resize'));
      await new Promise(r=>setTimeout(r,100));testSnap.compact=on;window.refreshHandler({...testSnap});
    };
  });
  const beforeExpand=await page.evaluate(()=>calls.length);
  await page.locator('#bar-expand').click();await page.setViewportSize({width:800,height:780});
  await page.waitForFunction(()=>testSnap.compact===false);await page.waitForTimeout(150);
  assert.ok(await page.locator('#board').isVisible(),'resize recovery overrode Expand');
  assert.deepEqual(await page.evaluate(n=>calls.slice(n).filter(c=>c[0]==='delayed-compact'),beforeExpand),[['delayed-compact',false]]);
  await page.locator('#bd-dock').click();await page.setViewportSize({width:340,height:46});await page.waitForFunction(()=>testSnap.compact);
  assert.equal(await page.evaluate(()=>testSnap.open_sessions.find(r=>r.id==='draft').enabled),true);
  await page.locator('#bar-expand').click();await page.setViewportSize({width:800,height:780});await page.waitForFunction(()=>!testSnap.compact);
  await page.evaluate(()=>{window.go.wservice.Service.SetCompact=originalSetCompact;});
  await page.evaluate(()=>{testSnap.open_sessions=testSnap.open_sessions.filter(r=>r.id!=='draft');testSnap.selected_session='two';render(testSnap);});
  await page.evaluate(()=>{testSnap.open_sessions[1].limit=.5;testSnap.open_sessions[1].fraction=.82;render(testSnap);});
  await page.locator('.session-row[data-id="one"]').click();await page.waitForFunction(()=>document.getElementById('toast').classList.contains('hidden'));await page.evaluate(()=>{testSnap.model='mock/model-one';testSnap.open_sessions[1].state='warn';render(testSnap);});await page.screenshot({path:join(out,'expanded.png')});
  await page.locator('#bd-dock').click();await page.setViewportSize({width:340,height:46});
  await page.evaluate(()=>{testSnap.open_sessions[0].name='ujjwal-keen-robin-a1b2c3d4';testSnap.selected_session='two';render(testSnap);});
  assert.equal(await page.locator('#bar-actions').count(),0);assert.equal(await page.locator('#bar-model').innerText(),'keen-robin-a1b2c3d4');
  assert.match(await page.locator('#bar-model').getAttribute('title'),/ujjwal-keen-robin-a1b2c3d4/);
  const compact=await page.locator('#bar').boundingBox();assert.equal(compact.width,340);assert.equal(compact.height,46);
  assert.ok(await page.locator('#bar-expand').isVisible());assert.ok(await page.locator('#bar-model').evaluate(e=>e.clientWidth>75),'session summary was squeezed out');
  await page.screenshot({path:join(out,'docked-over.png')});
  await page.locator('#bar-expand').click();await page.setViewportSize({width:800,height:780});assert.equal(await page.locator('#selected-name').innerText(),'ujjwal-keen-robin-a1b2c3d4');
  await page.locator('#bd-stop').click();assert.deepEqual(await page.evaluate(()=>calls.filter(c=>c[0]==='stop').at(-1)),['stop','one']);
  // More open rows and models must scroll without moving the fixed footer.
  await page.evaluate(()=>{const base=testSnap.open_sessions[2];testSnap.open_sessions.push(...Array.from({length:12},(_,i)=>({...base,id:'extra-'+i,name:'project-bright-otter-'+i})));render(testSnap);});
  const regions=await page.evaluate(()=>['session-list','bd-table-wrap'].map(id=>{const e=document.getElementById(id);return {id,scroll:e.scrollHeight>e.clientHeight,x:e.scrollWidth>e.clientWidth};}));
  for(const r of regions){assert.ok(r.scroll,r.id+' did not scroll');assert.equal(r.x,false,r.id+' scrolls sideways');}
  await page.locator('#session-list').evaluate(e=>e.scrollTop=e.scrollHeight);assert.ok(await page.locator('#bd-stop').isVisible());
  await page.screenshot({path:join(out,'expanded-many.png')});
  // Closed selection falls back, no limit inherited, singleton and empty states.
  await page.evaluate(()=>{testSnap.open_sessions=[testSnap.open_sessions[2]];testSnap.selected_session='three';testSnap.open_cost=testSnap.open_sessions[0].spent;render(testSnap);});
  assert.equal(await page.locator('.session-row').count(),1);assert.equal(await page.locator('#bd-limit').inputValue(),'');
  await page.screenshot({path:join(out,'expanded-single.png')});
  await page.evaluate(()=>{testSnap.compact=true;testSnap.open_sessions[0].name='keen-robin-c22b85c4';testSnap.open_rate=.18;testSnap.rate=99;testSnap.cost=700;testSnap.view='TOTAL';render(testSnap);});await page.setViewportSize({width:340,height:46});assert.equal(await page.locator('#bar-model').innerText(),'1 session · no limits');assert.equal(await page.locator('#bar-edge').isVisible(),false);
  assert.equal(await page.locator('#bar-rate').innerText(),'$0.18/hr','closed/global work changed the dock rate');
  assert.equal(await page.locator('#bar-odometer .digit').allTextContents().then(d=>d.join('')),'00012083','global TRIP/TOTAL selection changed the open-session total');
  await page.screenshot({path:join(out,'docked-single.png')});
  const dockFits=async()=>{const layout=await page.locator('#bar-model').evaluate(model=>{
    const bar=document.getElementById('bar').getBoundingClientRect(),box=model.getBoundingClientRect(),expand=document.getElementById('bar-expand').getBoundingClientRect();
    return {fits:box.top>=bar.top+1&&box.bottom<=bar.bottom-3&&box.right<=expand.left&&model.scrollWidth<=model.clientWidth,text:model.textContent,box:box.toJSON(),bar:bar.toJSON(),expand:expand.toJSON(),scroll:model.scrollWidth,width:model.clientWidth};
  });assert.ok(layout.fits,'complete dock caption overflowed or overlapped the edge/expand button: '+JSON.stringify(layout));};
  await page.evaluate(()=>{testSnap.active=true;render(testSnap);});
  assert.equal(await page.locator('#bar-odometer .pulse').count(),0,'closed-chat activity leaked into the dock');
  await page.evaluate(()=>{testSnap.open_sessions[0].active=true;render(testSnap);});
  assert.equal(await page.locator('#bar-odometer .pulse').count(),1,'open-session activity was hidden');
  await dockFits();
  await page.evaluate(()=>{testSnap.open_sessions[0].enabled=true;testSnap.open_sessions[0].cost=12345;testSnap.open_sessions[0].limit=.5;testSnap.open_sessions[0].fraction=24690;render(testSnap);});
  assert.ok(await page.locator('#bar-rate').evaluate(rate=>rate.getBoundingClientRect().right<=document.getElementById('bar-expand').getBoundingClientRect().left),'long budget text collided with expand');
  await dockFits();
  await page.evaluate(()=>{testSnap.active=false;testSnap.open_sessions[0].active=false;testSnap.open_sessions[0].cost=1.2083;render(testSnap);});
  await page.evaluate(()=>{testSnap.open_sessions[0].enabled=true;testSnap.open_sessions[0].limit=2;testSnap.open_sessions[0].cost=.4;testSnap.open_sessions[0].fraction=.2;render(testSnap);});
  assert.equal(await page.locator('#bar-model').innerText(),'1 session · all within budget');await dockFits();
  // Reproduce clear-fox doing work while clear-owl is selected: the dock is aggregate.
  await page.evaluate(()=>{const base=testSnap.open_sessions[0];testSnap.open_sessions=[{...base,id:'fox',name:'clear-fox-930e1799',enabled:false,active:true,spent:.14},{...base,id:'owl',name:'clear-owl-99dd683d',enabled:false,active:false,spent:0}];testSnap.selected_session='owl';testSnap.open_cost=.14;render(testSnap);});
  assert.equal(await page.locator('#bar-model').innerText(),'2 sessions · no limits');assert.equal(await page.locator('#bar-edge').isVisible(),false);await dockFits();
  assert.match(await page.locator('#bar-model').getAttribute('title'),/clear-fox-930e1799/);assert.match(await page.locator('#bar-model').getAttribute('title'),/clear-owl-99dd683d/);
  await page.screenshot({path:join(out,'docked-no-limit.png')});
  const aggregateDigits=await page.locator('#bar-odometer .digit').allTextContents();
  await page.evaluate(()=>{testSnap.selected_session='fox';render(testSnap);});
  assert.equal(await page.locator('#bar-model').innerText(),'2 sessions · no limits');assert.deepEqual(await page.locator('#bar-odometer .digit').allTextContents(),aggregateDigits);
  await page.evaluate(()=>{testSnap.selected_session='owl';Object.assign(testSnap.open_sessions[0],{enabled:true,limit:.5,cost:.1,fraction:.2});render(testSnap);});
  assert.equal(await page.locator('#bar-model').innerText(),'2 sessions · all within budget');await dockFits();assert.ok(await page.locator('#bar-edge').isVisible());await page.screenshot({path:join(out,'docked-ok.png')});
  await page.evaluate(()=>{Object.assign(testSnap.open_sessions[0],{cost:.41,fraction:.82});render(testSnap);});
  assert.equal(await page.locator('#bar-model').innerText(),'⚠ clear-fox-930e1799 82%');await dockFits();await page.screenshot({path:join(out,'docked-warning.png')});
  await page.evaluate(()=>{Object.assign(testSnap.open_sessions[0],{cost:.55,fraction:1.1});render(testSnap);});
  assert.equal(await page.locator('#bar-model').innerText(),'clear-fox-930e1799');assert.equal(await page.locator('#bar-rate').innerText(),'$0.55 / $0.50');assert.ok(await page.locator('#bar').evaluate(e=>e.classList.contains('session-over')));assert.doesNotMatch(await page.locator('#bar').innerText(),/OVER/);await dockFits();await page.screenshot({path:join(out,'docked-over.png')});
  await page.locator('#bar-expand').click();await page.setViewportSize({width:800,height:780});assert.equal(await page.locator('#selected-name').innerText(),'clear-fox-930e1799','expanding failed to select the actual breach');
  await page.evaluate(()=>{testSnap.compact=true;testSnap.preferences.paused=true;render(testSnap);});await page.setViewportSize({width:340,height:46});assert.equal(await page.locator('#bar-rate').innerText(),'PAUSED','breach hid the pause indication');
  await page.evaluate(()=>{testSnap.preferences.paused=false;testSnap.open_sessions[0].name='clear-panda-87654321';testSnap.open_sessions[1].name='clear-panda-12345678';render(testSnap);});
  assert.equal(await page.locator('#bar-model').innerText(),'clear-panda-87654321');await dockFits();
  await page.evaluate(()=>{Object.assign(testSnap.open_sessions[0],{on_fallback:true,stage:'fallback',stopped:false,state:'fallback',fallback_spent:.01,fallback_limit:.10,fallback_model:'companyhub/gemini-3.8-flash',fallback_free:false});testSnap.active=true;render(testSnap);});
  assert.equal(await page.locator('#bar-model .dock-line').count(),2);assert.ok(await page.locator('#bar-model .dock-line').evaluateAll(es=>es.every(e=>e.getBoundingClientRect().height<=12&&getComputedStyle(e).whiteSpace==='nowrap')));await dockFits();
  await page.evaluate(()=>{showRuleToast({...testSnap.open_sessions[0],stopped:true,detail:'Fallback companyhub/gemini stopped: authentication failed. No other provider was tried.'});});
  assert.doesNotMatch(await page.locator('#rule-toast-text').innerText(),/Raise the limit/);await page.waitForFunction(()=>calls.some(c=>c[0]==='rule-toast-size'&&c[1]>46));
  await page.evaluate(()=>{dismissRuleToast();showRuleToast(testSnap.open_sessions[0]);dismissRuleToast();});await page.waitForFunction(()=>calls.filter(c=>c[0]==='rule-toast-size').at(-1)?.[1]===0);
  await page.evaluate(()=>{testSnap.open_sessions=[];testSnap.selected_session='';testSnap.open_cost=99;render(testSnap);});assert.equal(await page.locator('#bar-model').innerText(),'No open sessions');assert.equal(await page.locator('#bar-rate').innerText(),'$0.00/hr');assert.equal(await page.locator('#bar-odometer .digit').allTextContents().then(d=>d.join('')),'00000000');assert.equal(await page.locator('#bar-edge').isVisible(),false);
  await page.evaluate(()=>{testSnap.compact=false;render(testSnap);});await page.setViewportSize({width:800,height:780});assert.ok(await page.locator('#bd-limit').isDisabled());assert.ok(await page.locator('#bd-stop').isDisabled());
  assert.deepEqual(errors,[]);console.log('Multi-session UI passed: 340x46 dock flows, focus/resize reconciliation, budget retained after docking and typing, concise picker count, aggregate captions, worst-budget expansion, stable selection/checkbox saves, both scroll regions, singleton and empty states.');
 }finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(e=>{console.error(e);process.exitCode=1;});
