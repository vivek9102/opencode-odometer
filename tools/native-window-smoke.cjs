// Exercise the built Windows/WebView2 app with isolated data and a fake server.
// Requires Playwright on NODE_PATH and OPENCODE_NATIVE_SMOKE_EXE. No inference.
// The test build needs a private Wails copy adding the browser arguments below
// to Chromium options; both loaders overwrite WebView environment flags.
const assert=require('node:assert/strict');
const {spawn}=require('node:child_process');
const {createServer}=require('node:http');
const {mkdirSync,writeFileSync,readFileSync}=require('node:fs');
const {resolve,join}=require('node:path');
const {chromium}=require('playwright');
const sleep=ms=>new Promise(r=>setTimeout(r,ms));
(async()=>{
 const exe=process.env.OPENCODE_NATIVE_SMOKE_EXE;
 assert.ok(exe,'Set OPENCODE_NATIVE_SMOKE_EXE to the review executable');
 const root=resolve(__dirname,'../build/native-window-smoke-'+Date.now()),data=join(root,'data');
 mkdirSync(data,{recursive:true});mkdirSync(join(root,'config','plugins'),{recursive:true});mkdirSync(join(root,'temp'),{recursive:true});
 const prefs={auto_start:false,auto_collapse:false,idle_dim:false,paused:false,remaining:false};
 writeFileSync(join(data,'preferences.json'),JSON.stringify(prefs));
 const entry={id:'native-window',name:'clear-fox-native',pid:process.pid,started:Date.now(),updated:Math.floor(Date.now()/1000),session_id:'native-chat',model:'mock/paid',active:false,closed:false};
 const presence=()=>{entry.updated=Math.floor(Date.now()/1000);writeFileSync(join(data,'tui-native-window.json'),JSON.stringify(entry));};
 presence();const timer=setInterval(presence,1000);
 const server=createServer((req,res)=>{
  if(req.url==='/event'){res.writeHead(200,{'Content-Type':'text/event-stream'});res.write(': isolated test\n\n');return;}
  res.setHeader('Content-Type','application/json');res.end(req.url?.startsWith('/session')?'[]':'{}');
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const reserve=createServer();await new Promise(r=>reserve.listen(0,'127.0.0.1',r));
 const debugPort=reserve.address().port;await new Promise(r=>reserve.close(r));
 // The app's instance lock is in its temp directory. Isolate that too, so
 // this fixture can run alongside the user's widget without touching its lock.
 const env={...process.env,TEMP:join(root,'temp'),TMP:join(root,'temp'),OPENCODE_ODOMETER_DIR:data,OPENCODE_PLUGIN_DIR:join(root,'config','plugins'),OPENCODE_ODOMETER_POINTER:join(data,'pointer.json'),OPENCODE_URL:'http://127.0.0.1:'+server.address().port,OPENCODE_NATIVE_SMOKE_BROWSER_ARGS:'--remote-debugging-port='+debugPort,OPENCODE_NATIVE_SMOKE_WEBVIEW_DIR:join(root,'webview')};
 const child=spawn(exe,[],{cwd:root,env,windowsHide:true,stdio:'ignore'});
 let browser;
 try{
  let endpoint;for(let n=0;n<120&&!endpoint;n++){
   if(child.exitCode!==null)throw Error('Native app exited: '+child.exitCode);
   try{endpoint=(await(await fetch('http://127.0.0.1:'+debugPort+'/json/version')).json()).webSocketDebuggerUrl;}catch{await sleep(250);}
  }
  assert.ok(endpoint,'WebView2 debugging endpoint did not appear');
  browser=await chromium.connectOverCDP(endpoint);
  let page;for(let n=0;n<100&&!page;n++){page=browser.contexts().flatMap(c=>c.pages())[0];if(!page)await sleep(100);}
  assert.ok(page,'Native WebView page did not appear');
  await page.waitForFunction(()=>window.go?.wservice?.Service?.ReconcileWindowLayout&&document.querySelector('.session-row'),{},{polling:100});
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  const dimensions=()=>page.evaluate(()=>({width:innerWidth,height:innerHeight,compact:snapshot.compact}));
  await page.locator('#bar-expand').click();await page.waitForFunction(()=>!snapshot.compact&&innerWidth===800,{},{polling:100});
  // The user's order: enter the amount, click ENABLE, then dock immediately.
  await page.locator('#bd-limit').fill('.20');await page.locator('#bd-enabled').click();
  await page.waitForFunction(()=>snapshot.open_sessions[0]?.enabled,{},{polling:100});
  assert.ok(await page.locator('#bd-enabled').isChecked());
  await page.locator('#bd-dock').click();await page.waitForFunction(()=>snapshot.compact&&innerWidth===340&&innerHeight===46,{},{polling:100});
  assert.equal(await page.locator('#bar-model').innerText(),'1 session · all within budget');
  for(let i=0;i<3;i++){
   await page.locator('#bar-expand').click();await page.waitForFunction(()=>!snapshot.compact&&innerWidth===800,{},{polling:100});await sleep(200);
   assert.equal((await dimensions()).compact,false,'native resize reversed Expand');
   assert.equal(await page.locator('#bd-limit').inputValue(),'0.2');assert.ok(await page.locator('#bd-enabled').isChecked());
   await page.locator('#bd-dock').click();await page.waitForFunction(()=>snapshot.compact&&innerWidth===340&&innerHeight===46,{},{polling:100});
  }
  // Compact toasts grow only to their measured content, leaving a 46px bar.
  await page.evaluate(()=>showRuleToast({name:'clear-fox-native',stopped:true,detail:'Fallback mock/paid stopped: authentication failed. No other provider was tried.'}));
  await page.waitForFunction(()=>snapshot.peek&&innerHeight===snapshot.peek_height,{},{polling:100});
  const barBox=await page.locator('#bar').evaluate(e=>({box:e.getBoundingClientRect().toJSON(),height:innerHeight,peek:snapshot.peek_height}));assert.ok(barBox.box.height===46&&Math.abs(barBox.box.bottom-barBox.height)<1,JSON.stringify(barBox));
  const toastBox=await page.locator('#rule-toast').evaluate(e=>({box:e.getBoundingClientRect().toJSON(),height:innerHeight,peek:snapshot.peek_height}));assert.ok(toastBox.box.top>=0,JSON.stringify(toastBox));assert.equal((await dimensions()).width,340);
  await page.screenshot({path:join(root,'native-docked-toast.png')});await page.evaluate(()=>dismissRuleToast());
  await page.waitForFunction(()=>!snapshot.peek&&innerHeight===46,{},{polling:100});
  await page.evaluate(()=>{showRuleToast({name:'clear-fox-native',stopped:true,detail:'Budget reached.'});dismissRuleToast();});await sleep(500);assert.equal((await dimensions()).height,46,'quick dismissal left a black window');
  // Reproduce auto-collapse while minimised, followed by native restore.
  await page.locator('#bar-expand').click();await page.waitForFunction(()=>!snapshot.compact,{},{polling:100});
  await page.evaluate(p=>window.go.wservice.Service.SetPreferences(p),{...prefs,auto_collapse:true});
  await page.evaluate(()=>window.runtime.WindowMinimise());
  await page.waitForFunction(async()=> (await window.go.wservice.Service.Snapshot()).compact,{},{polling:100});
  await page.evaluate(()=>window.runtime.WindowUnminimise());
  await page.waitForFunction(()=>snapshot.compact&&innerWidth===340&&innerHeight===46,{},{polling:100}).catch(async e=>{console.error(await page.evaluate(async()=>({snapshot:await window.go.wservice.Service.Snapshot(),width:innerWidth,height:innerHeight})));throw e;});
  await page.locator('#bar-expand').click();await page.waitForFunction(()=>!snapshot.compact&&innerWidth===800,{},{polling:100});await sleep(200);
  assert.ok(await page.locator('#bd-enabled').isChecked());assert.equal(await page.locator('#bd-limit').inputValue(),'0.2');
  await page.screenshot({path:join(root,'native-expanded.png')});
  const state=JSON.parse(readFileSync(join(data,'odometer_state.json')));
  assert.equal(state.open_policies['native-window'].enabled,true);assert.equal(state.open_policies['native-window'].limit,.2);
  assert.deepEqual(errors,[]);
  console.log('Native WebView2 passed: amount-first ENABLE, three expand/dock cycles, minimise/auto-collapse/restore and persisted allowance. Artifacts: '+root);
 }finally{
  clearInterval(timer);
  if(browser){try{const p=browser.contexts().flatMap(c=>c.pages())[0];await p?.evaluate(()=>window.runtime.Quit());await sleep(300);}catch{}try{await browser.close();}catch{}}
  if(child.exitCode===null)child.kill();
  server.closeAllConnections();await new Promise(r=>server.close(r));
 }
})().catch(e=>{console.error(e);process.exitCode=1;});
