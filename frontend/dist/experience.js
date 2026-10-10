// Live activity, preferences and confirmed next-turn choices.
function renderExperience(snap) {
  const remaining=!!snap.preferences?.remaining && snap.budget_enabled && snap.limit>0;
  const urgent=snap.budget_enabled && snap.budget_state !== "ok";
  document.body.classList.toggle("quiet-idle",!!snap.preferences?.idle_dim && !snap.active && !urgent && !snap.unpriced_models && !snap.preferences?.paused);
  $("bar").classList.toggle("hidden",!snap.compact);
  $("board").classList.toggle("hidden",snap.compact);
  $("bd-reading").textContent=remaining ? "remaining" : "spent";
  $("pause-status").classList.toggle("hidden",!snap.preferences?.paused);
  drawSpark($("bd-spark"),snap.sparkline||[]);
  renderCharge(snap);
  renderSwitchStatus();
}

function drawSpark(svg, values) {
  const w=svg.viewBox.baseVal.width,h=svg.viewBox.baseVal.height;
  const max=Math.max(...values,0.000001);
  svg.querySelector("polyline").setAttribute("points",values.map((v,i)=>`${i*w/Math.max(1,values.length-1)},${h-2-v/max*(h-5)}`).join(" "));
  svg.style.color=snapshot ? accentFor(snapshot) : "var(--green)";
  svg.setAttribute("aria-label","Spend over the last 10 minutes: $"+values.reduce((a,b)=>a+b,0).toFixed(4));
}
let seenCharge=null,chargeQueue=[],chargeTimer=null;
function renderCharge(snap) {
  const c=snap.charge;
  if(!c)return;
  if(seenCharge===null){seenCharge=c.sequence;return;}
  if(c.sequence<=seenCharge)return;
  for (const charge of (snap.charges || [c])) if(charge.sequence>seenCharge && charge.cost>0)chargeQueue.push(charge);
  seenCharge=c.sequence;showCharge();
}
function showCharge() {
  if(chargeTimer || !chargeQueue.length)return;
  const c=chargeQueue.shift(),el=$("cost-tick");
  el.textContent="+$"+c.cost.toFixed(3);el.title="Completed charge: "+c.id;
  el.classList.remove("hidden");
  chargeTimer=setTimeout(()=>{el.classList.add("hidden");chargeTimer=null;showCharge();},1800);
}

// Details open through the expand button. Mouse movement never resizes the
// native window, so its hit area and position remain stable.
function cancelPeek() {if(snapshot?.peek)Svc().SetPeek(false);}
$("bd-reading").addEventListener("click",toggleReading);
async function savePreferences(p) {await Svc().SetPreferences(p);if(snapshot)snapshot.preferences=p;if(!p.auto_collapse)cancelCollapse();}
async function toggleReading() {
  if(!snapshot?.budget_enabled || !(snapshot.limit>0)){toast("Set a chat limit to show remaining spend","warn");return;}
  const p={...snapshot.preferences,remaining:!snapshot.preferences?.remaining};
  try{await savePreferences(p);render(snapshot);}catch(e){toast("Could not save setting: "+e,"over");}
}

const settingsModal=$("settings-modal");
function openSettings() {
  setView(true);const p=snapshot?.preferences||{};
  $("pref-start").checked=p.auto_start!==false;
  $("pref-collapse").checked=!!p.auto_collapse;
  $("pref-dim").checked=p.idle_dim!==false;
  $("pref-pause").checked=!!p.paused;
  $("settings-result").textContent="";settingsModal.classList.remove("hidden");
  if(Svc().HasMetadataKey)Svc().HasMetadataKey().then(saved=>{$("metadata-result").textContent=saved?"Benchmark key configured; refresh to load scores.":"Optional key needed for Artificial Analysis scores.";}).catch(()=>{});
}
function closeSettings(){settingsModal.classList.add("hidden");}
$("bd-settings").addEventListener("click",openSettings);
$("bd-hide").addEventListener("click",()=>{cancelPeek();Svc().HideToTray();});
$("settings-close").addEventListener("click",closeSettings);
settingsModal.querySelector(".modal-backdrop").addEventListener("click",closeSettings);
for(const id of ["pref-start","pref-collapse","pref-dim","pref-pause"])$(id).addEventListener("change",async()=>{
  const p={...snapshot?.preferences,auto_start:$("pref-start").checked,auto_collapse:$("pref-collapse").checked,idle_dim:$("pref-dim").checked,paused:$("pref-pause").checked};
  try{await savePreferences(p);$("settings-result").textContent="Saved";}catch(e){$("settings-result").textContent="Could not save: "+e;}
});
let collapseTimer=null;
function cancelCollapse(){clearTimeout(collapseTimer);collapseTimer=null;}
function canAutoCollapse(){return snapshot?.preferences?.auto_collapse && !snapshot.compact && !adviceOpen && !document.activeElement?.matches("input,select,textarea") && !document.querySelector(".modal:not(.hidden)");}
let layoutFrame=null;
function reconcileWindowLayout(){
  if(layoutFrame!==null)return;
  // Windows restores the old native rectangle after a resize made while
  // minimised. Coalesce focus/resize events without changing the saved layout.
  // Do not submit a layout from an old snapshot: a native resize can arrive
  // before the snapshot acknowledging the user's Expand/DOCK click.
  layoutFrame=requestAnimationFrame(()=>{layoutFrame=null;if(snapshot)Svc().ReconcileWindowLayout();});
}
window.addEventListener("focus",()=>{
  cancelCollapse();reconcileWindowLayout();
});
window.addEventListener("resize",()=>{
  if(snapshot?.compact&&innerWidth>0&&innerHeight>0&&(innerWidth!==340||innerHeight!==(snapshot.peek?snapshot.peek_height||178:46)))reconcileWindowLayout();
});
window.addEventListener("blur",()=>{
  cancelPeek();
  cancelCollapse();
  if(!canAutoCollapse())return;
  collapseTimer=setTimeout(()=>{collapseTimer=null;if(!document.hasFocus() && canAutoCollapse())setView(false);},250);
});

let availableModels=[],modelTargetSession="",choosingModel=false;
function modelDisplayName(key){return availableModels.find(m=>m.key===key)?.name || bareModel(key);}
function showModelSwitchSummary(status){
  const el=$("switch-status"),name=modelDisplayName(status.key);
  el.classList.toggle("confirmed",status.status==="confirmed");
  el.classList.toggle("failed",status.status==="failed");
  el.textContent=status.status==="confirmed" ? name+" · Selected for this chat" : status.status==="queued" ? name+" · Starts with your next message" : status.status==="applied" ? name+" · Waiting for OpenCode's response" : status.detail;
  el.title=status.key+" · "+status.detail;
}
function paintModels() {
  const term=$("model-search").value.trim().toLowerCase(),filter=$("model-filter").value;
  const shown=availableModels.filter(m => (!term || (m.key+" "+m.name).toLowerCase().includes(term)) && (filter==="all" || (filter==="free" ? m.free : m.free || m.cheaper)));
  modelList.replaceChildren(...shown.map(m=>modelRow({...m,chat_selected:switchRequest?.persistent && switchRequest.session_id===modelTargetSession && switchRequest.key===m.key},chooseModel)));
  $("model-follow").classList.toggle("hidden",!(switchRequest?.persistent && switchRequest.session_id===modelTargetSession));
  $("model-count").textContent=shown.length+" of "+availableModels.length+" configured models · cheaper uses equal input/output tokens";
  if(!shown.length) {const p=document.createElement("p");p.className="modal-sub";p.textContent=availableModels.length ? "No match. Try All configured or another search." : "No configured models received. Restart OpenCode to load the updated plugin.";modelList.appendChild(p);}
}
function loadModelPicker() {
  modelTargetSession=snapshot?.session_id || "";
  if(switchRequest?.session_id!==modelTargetSession){switchRequest=null;$("switch-status").textContent="";}
  setView(true);
  const target=modelTargetSession;
  Promise.all([Svc().AvailableModels(),Svc().PendingModelSwitch(target)]).then(([models,pending])=>{if(target!==modelTargetSession)return;availableModels=models||[];if(pending?.id){switchRequest=pending;showModelSwitchSummary(pending);}paintModels();modal.classList.remove("hidden");$("model-search").focus();}).catch(e=>toast("Could not load models: "+e,"over"));
}
$("model-search").addEventListener("input",paintModels);
$("model-filter").addEventListener("change",paintModels);
$("model-follow").addEventListener("click",async()=>{
  if(!modelTargetSession || choosingModel)return;
  choosingModel=true;
  try{await Svc().ClearModelSwitch(modelTargetSession);switchRequest=null;$("switch-status").textContent="This chat now follows OpenCode's model selection.";paintModels();toast("Following OpenCode's selection","ok");}
  catch(e){toast(String(e),"over");}
  finally{choosingModel=false;}
});
let switchRequest=null,switchPollBusy=false,lastSwitchNotice="";
async function chooseModel(m,targetSession=modelTargetSession) {
  if(choosingModel || (m.category && m.category!=="chat"))return;
  if(!targetSession){toast("Send a message in the intended OpenCode chat first","warn");return;}
  choosingModel=true;
  try {switchRequest=await Svc().SwitchModel(m.key,targetSession);showModelSwitchSummary(switchRequest);paintModels();toast("Model selected: "+modelDisplayName(m.key),"ok");return true;}
  catch(e){switchRequest=null;toast(String(e),"over");const pending=await Svc().PendingModelSwitch(targetSession).catch(()=>null);$("switch-status").textContent=String(e)+(pending?.id ? " · Still saved: "+pending.key : "");return false;}
  finally{choosingModel=false;}
}
async function renderSwitchStatus() {
  if(!switchRequest || switchPollBusy)return;
  switchPollBusy=true;
  try {
    const status=await Svc().ModelSwitchStatus(switchRequest.id);
    if(!switchRequest || status?.id!==switchRequest.id) {
      if(switchRequest && Date.now()/1000-switchRequest.issued>35){$("switch-status").textContent="OpenCode did not acknowledge the choice. Restart it to load the updated plugin.";switchRequest=null;}
      return;
    }
    showModelSwitchSummary(status);
    if(["confirmed","failed","expired","cancelled","unconfirmed"].includes(status.status)) {
      const notice=status.id+":"+status.message_id+":"+status.status;
      if(notice!==lastSwitchNotice){lastSwitchNotice=notice;toast(status.status==="confirmed" ? "OpenCode confirmed: "+bareModel(status.key) : status.detail,status.status==="confirmed" ? "ok" : "warn");}
      if(!status.persistent || status.status==="cancelled")switchRequest=null;
    }
  }catch{ /* retry on next snapshot */ }finally{switchPollBusy=false;}
}
