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
  window.go={wservice:{Service:{Snapshot:async()=>({...testSnap}),SetScreenSize:async()=>{},SetCompact:async on=>{testSnap.compact=on;emit();},SetPeek:async()=>{},SelectOpenSession:async id=>{calls.push(['select',id]);if(window.holdSelections)await new Promise(resolve=>selectionResolvers.push(resolve));testSnap.selected_session=id;emit();},
   SetOpenSessionBudget:async(id,limit,mode,enabled)=>{calls.push(['budget',id,limit,mode,enabled]);if(window.holdBudgets)await new Promise(resolve=>budgetResolvers.push(resolve));const r=testSnap.open_sessions.find(r=>r.id===id);Object.assign(r,{limit,mode,enabled,fraction:limit?r.cost/limit:0,state:!enabled?'ok':r.cost>=limit?'over':r.cost>=limit*.75?'warn':'ok'});emit();},
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
  assert.deepEqual(await page.evaluate(()=>calls.find(c=>c[0]==='budget')),['budget','two',2,'soft',true]);
  await page.locator('#bd-stop').click();assert.deepEqual(await page.evaluate(()=>calls.find(c=>c[0]==='stop')),['stop','two']);
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
  assert.equal(await page.locator('#bar-model').innerText(),'clear-fox-930e1799');assert.equal(await page.locator('#bar-rate').innerText(),'$0.55 / $0.50');assert.ok(await page.locator('#bar').evaluate(e=>e.classList.contains('session-over')));assert.doesNotMatch(await page.locator('#bar').innerText(),/STOP|OVER/);await dockFits();await page.screenshot({path:join(out,'docked-over.png')});
  await page.locator('#bar-expand').click();await page.setViewportSize({width:800,height:780});assert.equal(await page.locator('#selected-name').innerText(),'clear-fox-930e1799','expanding failed to select the actual breach');
  await page.evaluate(()=>{testSnap.compact=true;testSnap.preferences.paused=true;render(testSnap);});await page.setViewportSize({width:340,height:46});assert.equal(await page.locator('#bar-rate').innerText(),'PAUSED','breach hid the pause indication');
  await page.evaluate(()=>{testSnap.preferences.paused=false;testSnap.open_sessions[0].name='clear-panda-87654321';testSnap.open_sessions[1].name='clear-panda-12345678';render(testSnap);});
  assert.equal(await page.locator('#bar-model').innerText(),'clear-panda-87654321');await dockFits();
  await page.evaluate(()=>{testSnap.open_sessions=[];testSnap.selected_session='';testSnap.open_cost=99;render(testSnap);});assert.equal(await page.locator('#bar-model').innerText(),'No open sessions');assert.equal(await page.locator('#bar-rate').innerText(),'$0.00/hr');assert.equal(await page.locator('#bar-odometer .digit').allTextContents().then(d=>d.join('')),'00000000');assert.equal(await page.locator('#bar-edge').isVisible(),false);
  await page.evaluate(()=>{testSnap.compact=false;render(testSnap);});await page.setViewportSize({width:800,height:780});assert.ok(await page.locator('#bd-limit').isDisabled());assert.ok(await page.locator('#bd-stop').isDisabled());
  assert.deepEqual(errors,[]);console.log('Multi-session UI passed: 340x46 reference dock flows, complete identifiers, aggregate captions independent of footer selection, worst-budget alerts/expansion, pause/no-limit logic, stable rows/checkbox saves, both scroll regions, singleton and empty states.');
 }finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(e=>{console.error(e);process.exitCode=1;});
