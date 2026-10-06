// Run with Playwright available on NODE_PATH. Uses mock Wails services only.
const assert=require('node:assert/strict');
const {chromium}=require('playwright');
const {createServer}=require('node:http');
const {readFileSync}=require('node:fs');
const {join}=require('node:path');

(async()=>{
 const root=join(__dirname,'../frontend/dist');
 const server=createServer((req,res)=>{const file=req.url==='/'?'index.html':req.url.slice(1);if(!/^[\w.-]+$/.test(file)){res.writeHead(404).end();return}try{res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(readFileSync(join(root,file)))}catch{res.writeHead(404).end()}});
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE || chromium.executablePath()});
 const page=await browser.newPage({viewport:{width:360,height:46}});
 const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.addInitScript(()=>{
  window.testSnap={view:'TRIP',cost:.3,total_cost:.3,trip_cost:.3,saved:0,rate:.14,model:'claude-opus-5',tokens:100,seeded:true,active:false,budget_enabled:true,limit:1,session_cost:.3,session_id:'s1',fraction:.3,budget_state:'ok',enforced:true,mode:'hard',compact:true,dock:'bottom-center',docked:true,rows:[],sparkline:Array(30).fill(.01),charge:{sequence:0,cost:0},preferences:{auto_start:true,auto_collapse:false,idle_dim:true,paused:false,remaining:false},unpriced_models:0,connected:true};
  const emit=()=>window.refreshHandler?.({...testSnap});
  window.runtime={EventsOn:(name,cb)=>window.refreshHandler=cb};
  window.testModels=Array.from({length:54},(_,i)=>({key:'custom/model-'+i+(i<12?'-free':''),name:'Model '+i,provider:'custom',free:i<12,cheaper:i<40,category:i===53?'embedding':'chat',unknown:i===52,input_cost:1,output_cost:2}));
  window.go={wservice:{Service:{
   Snapshot:async()=>({...testSnap}),SetScreenSize:async()=>{},SetCompact:async v=>{testSnap.compact=v;testSnap.peek=false;emit()},SetPeek:async v=>{testSnap.peek=v;emit()},
   SetPreferences:async p=>{testSnap.preferences=p;emit()},UnpricedModels:async()=>[],PendingPrices:async()=>[],PricesAgeHours:async()=>0,
   AvailableModels:async()=>testModels,PendingModelSwitch:async sid=>window.testSwitch?.session_id===sid ? testSwitch : {},ClearModelSwitch:async()=>{window.testSwitch=null},SwitchModel:async(key,sid)=>{window.testSwitch={id:'req1',key,session_id:sid,persistent:true,issued:Date.now()/1000,status:'queued',detail:'Ready for next request'};return testSwitch},ModelSwitchStatus:async()=>testSwitch,
   Advice:async()=>({session_id:testSnap.session_id,limit:1,cost:1.1,rate:.2,tokens:1000,msgs:2,drivers:[{key:'custom/default',output:1.1}],cheaper:testModels.slice(0,2)}),ShowAlert:async()=>{},
  }}};
 });
 try {
  await page.goto(`http://127.0.0.1:${server.address().port}/`);
  await page.waitForFunction(()=>document.body.classList.contains('compact-mode'));
  assert.ok(await page.locator('body').evaluate(e=>e.classList.contains('quiet-idle')));
  await page.locator('#bar-side').hover();await page.waitForTimeout(800);
  assert.equal(await page.evaluate(()=>testSnap.compact),true,'hover must not resize the compact window');
  assert.equal(await page.locator('#hover-peek').count(),0);
  await page.locator('#bar-expand').click();await page.setViewportSize({width:620,height:580});
  await page.locator('#bd-reading').click();assert.equal(await page.locator('#bd-reading').innerText(),'remaining');
  await page.locator('#bd-switch').click();
  await page.locator('#model-modal').waitFor({state:'visible'});
  await page.evaluate(()=>{testSnap.session_id='other-chat';render(testSnap)});
  await page.locator('#model-filter').selectOption('all');assert.equal(await page.locator('#model-list .model-card').count(),54);
  await page.locator('#model-filter').selectOption('free');assert.equal(await page.locator('#model-list .model-card').count(),12);
  await page.locator('#model-filter').selectOption('all');await page.locator('#model-search').fill('model-53');assert.equal(await page.locator('#model-list .model-card').count(),1);assert.ok(await page.locator('#model-list button').isDisabled());
  await page.locator('#model-search').fill('model-0-free');await page.locator('#model-list button').click();
  assert.equal(await page.evaluate(()=>testSwitch.session_id),'s1','a background snapshot changed the picker target');
  await page.evaluate(async()=>{testModels[0].current=true;await chooseModel(testModels[0],'advice-chat')});
  assert.equal(await page.evaluate(()=>testSwitch.session_id),'advice-chat','the budget advice action used the picker chat instead of its own target');
  await page.evaluate(()=>render(testSnap));await page.waitForFunction(()=>document.getElementById('switch-status').textContent.includes('next message'));
  assert.equal(await page.evaluate(()=>testSnap.limit),1);
  await page.evaluate(()=>{testSwitch.status='confirmed';testSwitch.detail='OpenCode confirmed the response model';render(testSnap)});
  await page.waitForFunction(()=>document.getElementById('toast').textContent.includes('confirmed'));
  await page.locator('#modal-close').click();
  await page.evaluate(()=>{testSnap.session_id='advice-chat';render(testSnap)});
  await page.locator('#bd-switch').click();await page.locator('#model-modal').waitFor({state:'visible'});
  assert.ok((await page.locator('#model-list').innerText()).includes('SELECTED'),'reopening lost the visible selected model');
  assert.equal(await page.locator('#model-target').count(),0,'the raw chat ID is still displayed');
  assert.equal(await page.locator('#model-follow').evaluate(e=>getComputedStyle(e).backgroundColor),'rgba(0, 0, 0, 0)','reset is still a prominent filled button');
  await page.screenshot({path:join(__dirname,'../build/model-ui-cleanup.png')});
  await page.locator('#model-follow').click();
  assert.equal(await page.evaluate(()=>testSwitch),null);
  assert.ok((await page.locator('#switch-status').innerText()).includes('follows OpenCode'));
  await page.locator('#modal-close').click();await page.locator('#bd-settings').click();await page.locator('#pref-start').uncheck();
  assert.equal(await page.evaluate(()=>testSnap.preferences.auto_start),false);assert.equal(await page.evaluate(()=>testSnap.session_cost),.3);
  await page.locator('#settings-close').click();
  await page.evaluate(()=>{testSnap.budget_state='warn';testSnap.fraction=.85;render(testSnap)});
  assert.ok(!await page.locator('body').evaluate(e=>e.classList.contains('quiet-idle')),'warning was dimmed');
  await page.evaluate(()=>{testSnap.budget_state='over';testSnap.session_cost=1.1;testSnap.fraction=1.1;render(testSnap);openAdvice(true)});
  await page.locator('#advice-cheaper button').first().waitFor({state:'visible'});
  assert.equal(await page.locator('#advice-cheaper button').first().innerText(),'SWITCH MODEL');
  await page.screenshot({path:join(__dirname,'../build/budget-ui-cleanup.png')});
  assert.deepEqual(errors,[]);
  console.log('Frontend smoke passed: stable hover, click details, remaining, 54 models, filters/search, confirmation, preferences and warning visibility.');
 }finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(e=>{console.error(e);process.exit(1)});
