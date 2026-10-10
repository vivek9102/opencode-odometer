// Selected-session settings save immediately; the fallback popup edits a draft.
const fallbackCache=new Map(),ruleEvents=new Map(),ruleWrites=new Map();
let fallbackDraft=null,fallbackOwner="",fallbackFocus=null,fallbackSaving=false,ruleToastTimer,ruleToastLayout=Promise.resolve(),ruleToastHeight=-1,ruleToastRevision=0;
const selectedRule=()=>snapshot?.open_sessions?.find(r=>r.id===snapshot.selected_session);
const ruleFromRow=r=>({rule:r.rule||"stop",fallback_model:r.fallback_model||"",fallback_limit:r.fallback_limit||0,then:r.then||"stop"});
const ruleMoney=n=>`$${Number(n||0).toFixed(2)}`;
const fallbackKey=r=>r.id+":"+(r.original_model||r.model);
const modelsFor=r=>fallbackCache.get(fallbackKey(r))?.models||[];
const popupRow=()=>snapshot?.open_sessions?.find(r=>r.id===fallbackOwner);

async function loadFallbackModels(row){
  if(!row)return;
  const key=fallbackKey(row),old=fallbackCache.get(key);
  if(old&&(old.pending||Date.now()-old.time<30000))return old.pending;
  const cached={models:old?.models||[],time:Date.now(),pending:null};fallbackCache.set(key,cached);
  cached.pending=(async()=>{
    try{cached.models=await Svc().FallbackModels(row.id)||[];}
    catch(e){cached.time=0;if(fallbackOwner===row.id)$("fallback-result").textContent=String(e);}
    finally{cached.pending=null;if(fallbackOwner===row.id)paintFallbackModels();}
  })();
  return cached.pending;
}
function updateFallbackChain(){
  const row=popupRow(),draft=fallbackDraft;if(!row||!draft)return;
  const m=modelsFor(row).find(m=>m.key===draft.fallback_model),free=!!m?.free;
  $("fallback-name").textContent=m?.name||bareModel(draft.fallback_model);
  $("fallback-allowance").classList.toggle("hidden",!m||free);
  $("fallback-free-note").classList.toggle("hidden",!free);
  document.querySelectorAll("[data-then]").forEach(e=>{e.classList.toggle("on",e.dataset.then===draft.then);e.setAttribute("aria-pressed",String(e.dataset.then===draft.then));});
  const first=`Use ${bareModel(row.original_model||row.model)||"the original model"} until ${ruleMoney(row.limit)}, then ${m?.name||"the fallback"}`;
  $("fallback-chain").textContent=!m?"Choose the model to use after your limit is reached.":free?`${first} for free, with no spending cap.`:draft.then==="go"?`${first}. Keep going even after its ${ruleMoney(draft.fallback_limit)} budget is used up.`:`${first} with an extra ${ruleMoney(draft.fallback_limit)}. Stop when that budget is used up.`;
  $("save-rule").disabled=fallbackSaving||!m||m.eligible===false||!row.enabled;
}
function paintFallbackModels(){
  const row=popupRow();if(!row||!fallbackDraft)return;
  const search=$("fallback-search").value.trim().toLowerCase(),cost=$("fallback-cost-filter").value,provider=$("fallback-provider-filter").value;
  const all=modelsFor(row),currentProvider=(row.original_model||row.model||"").split("/")[0];
  const shown=all.filter(m=>(cost==="all"||(cost==="free"?m.free:m.free||m.cheaper))&&(!search||`${m.name} ${m.key}`.toLowerCase().includes(search))&&(provider==="all"||m.provider===provider));
  shown.sort((a,b)=>Number(b.provider===currentProvider)-Number(a.provider===currentProvider)||Number(b.free)-Number(a.free)||Number(a.unknown)-Number(b.unknown)||(a.input+a.output)-(b.input+b.output)||a.key.localeCompare(b.key));
  const wrap=$("fallback-models"),scroll=wrap.scrollTop;
  wrap.replaceChildren(...shown.map(m=>{
    const specialised=m.category&&m.category!=="chat";
    const description=specialised?`${m.category} model · cannot run a coding chat`:m.eligible===false?m.reason:m.free?"Free model":`${m.estimated?"Estimated · ":""}${Math.round((m.ratio||0)*100)}% of original price at equal input/output tokens`;
    const card=modelRow({...m,description},()=>{fallbackDraft.fallback_model=m.key;if(!fallbackDraft.fallback_limit)fallbackDraft.fallback_limit=.05;$("fallback-limit").value=fallbackDraft.fallback_limit;paintFallbackModels();});
    card.classList.add("fallback-model");card.classList.toggle("on",m.key===fallbackDraft.fallback_model);
    const pick=card.querySelector(".model-pick-btn");pick.type="button";pick.dataset.model=m.key;pick.textContent=specialised?"SPECIALISED":m.key===fallbackDraft.fallback_model?"SELECTED":"SELECT";pick.disabled=fallbackSaving||m.eligible===false||(row.on_fallback&&m.key!==row.fallback_model);pick.title=m.reason||"Choose this provider and model";
    return card;
  }));
  if(!shown.length){const note=document.createElement("p");note.className="session-note";note.textContent="No configured models match these filters.";wrap.append(note);}
  wrap.scrollTop=scroll;$("fallback-count").textContent=`${shown.length} of ${all.length} configured models`;
  updateFallbackChain();
}
function closeBudgetPicker(){
  if(fallbackSaving)return;
  fallbackDraft=null;fallbackOwner="";$("fallback-modal").classList.add("hidden");if(fallbackFocus?.isConnected)fallbackFocus.focus();
}
async function openBudgetPicker(){
  let row=selectedRule();if(!row)return;
  const id=row.id;
  if(budgetWrites.has(id)){await budgetWrites.get(id).catch(()=>{});row=selectedRule();if(row?.id!==id)return;}
  fallbackFocus=document.activeElement;fallbackOwner=id;fallbackDraft={...ruleFromRow(row),rule:"switch"};
  $("fallback-search").value="";$("fallback-cost-filter").value="both";$("fallback-result").textContent=row.enabled?"":"Enable a session limit before saving a fallback.";
  $("fallback-limit").value=fallbackDraft.fallback_limit||.05;fallbackDraft.fallback_limit=fallbackDraft.fallback_limit||.05;
  $("fallback-subtitle").textContent=`${row.name} · at ${ruleMoney(row.limit)} it switches`;
  $("fallback-provider-filter").replaceChildren(new Option("All providers","all"));
  $("fallback-modal").classList.remove("hidden");paintFallbackModels();$("fallback-search").focus();
  await loadFallbackModels(row);if(fallbackOwner!==id)return;
  for(const provider of [...new Set(modelsFor(row).map(m=>m.provider||m.key.split("/")[0]))].sort())$("fallback-provider-filter").append(new Option(provider,provider));
  paintFallbackModels();
}
async function saveSessionChoice(id,choice){
  const prior=ruleWrites.get(id)||Promise.resolve();
  const request=prior.catch(()=>{}).then(()=>Svc().SetOpenSessionRule(id,choice));ruleWrites.set(id,request);
  try{await request;const fresh=await Svc().Snapshot();if(fresh)render({...fresh,selected_session:selectionTarget||snapshot?.selected_session||fresh.selected_session});}
  finally{if(ruleWrites.get(id)===request)ruleWrites.delete(id);if(snapshot)renderBudgetRules(snapshot);}
}
function renderBudgetRules(snap){
  if(!Array.isArray(snap.open_sessions))return;
  const row=selectedRule();$("budget-rules").classList.toggle("hidden",!row);
  if(row){
    document.querySelectorAll("[data-rule]").forEach(e=>{e.classList.toggle("on",e.dataset.rule===row.rule||(e.dataset.rule==="stop"&&!row.rule));e.setAttribute("aria-pressed",String(e.classList.contains("on")));e.disabled=ruleWrites.has(row.id)||row.stage==="switching";});
    const note=$("limit-choice-note");note.replaceChildren();
    if(!row.enabled)note.textContent="No limit set. Enable a limit to use this choice.";
    else if(row.stopped)note.textContent=`Stopped at ${ruleMoney(row.on_fallback?row.fallback_limit:row.limit)}. Raise or clear the limit to continue.`;
    else if(row.rule==="switch"&&row.fallback_model){
      const [provider,...parts]=row.fallback_model.split("/");
      note.textContent=`${row.on_fallback?"Using":"Switches to"} ${parts.join("/")} [${provider}]${row.on_fallback?"":" at "+ruleMoney(row.limit)}${row.fallback_free?" (free, no cap).":row.then==="go"?`. Keeps going after its ${ruleMoney(row.fallback_limit)} budget is used up.`:` for up to ${ruleMoney(row.fallback_limit)}, then stop.`} `;
      const change=document.createElement("button");change.type="button";change.className="text-btn";change.textContent="Change";change.addEventListener("click",openBudgetPicker);note.append(change);
    }else note.textContent=row.enabled?`Paid requests are blocked at ${ruleMoney(row.limit)}. Raise the limit to continue.`:"No limit set. New sessions start without a limit.";
  }
  if(fallbackOwner&&!snap.open_sessions.some(r=>r.id===fallbackOwner)){fallbackSaving=false;closeBudgetPicker();}
  for(const r of snap.open_sessions){const previous=ruleEvents.get(r.id);ruleEvents.set(r.id,r.rule_event||"");if(previous!==undefined&&r.rule_event&&previous!==r.rule_event)showRuleToast(r);}
  for(const id of ruleEvents.keys())if(!snap.open_sessions.some(r=>r.id===id))ruleEvents.delete(id);
  syncRuleToastWindow();
}
document.querySelector('[data-rule="switch"]').addEventListener("click",()=>void openBudgetPicker());
document.querySelector('[data-rule="stop"]').addEventListener("click",async()=>{const r=selectedRule();if(!r)return;try{await saveSessionChoice(r.id,{...ruleFromRow(r),rule:"stop"});}catch(e){toast(String(e),"over");}});
for(const id of ["fallback-close","fallback-cancel"])$(id).addEventListener("click",closeBudgetPicker);
$("fallback-modal").querySelector(".modal-backdrop").addEventListener("click",closeBudgetPicker);
$("fallback-modal").addEventListener("keydown",e=>{
  if(e.key==="Escape"){e.preventDefault();e.stopPropagation();closeBudgetPicker();}
  if(e.key==="Tab"){const controls=[...$("fallback-modal").querySelectorAll("button,input,select,a[href]")].filter(e=>!e.disabled&&e.getClientRects().length);const first=controls[0],last=controls.at(-1);if(e.shiftKey&&document.activeElement===first){e.preventDefault();last.focus();}else if(!e.shiftKey&&document.activeElement===last){e.preventDefault();first.focus();}}
});
$("fallback-search").addEventListener("input",paintFallbackModels);
for(const id of ["fallback-cost-filter","fallback-provider-filter"])$(id).addEventListener("change",paintFallbackModels);
$("fallback-limit").addEventListener("input",()=>{if(fallbackDraft){fallbackDraft.fallback_limit=Number($("fallback-limit").value);updateFallbackChain();}});
document.querySelectorAll("[data-then]").forEach(e=>e.addEventListener("click",()=>{if(fallbackDraft){fallbackDraft.then=e.dataset.then;updateFallbackChain();}}));
$("save-rule").addEventListener("click",async()=>{
  const r=popupRow(),draft=fallbackDraft;if(!r||!draft||fallbackSaving)return;
  const m=modelsFor(r).find(m=>m.key===draft.fallback_model);
  if(!m||m.eligible===false)return;
  if(!m.free&&(!(draft.fallback_limit>0)||!Number.isFinite(draft.fallback_limit))){$("fallback-result").textContent="Enter an extra budget greater than $0.";$("fallback-limit").focus();return;}
  const id=r.id;fallbackSaving=true;paintFallbackModels();$("fallback-result").textContent="Saving…";
  try{await saveSessionChoice(id,{...draft});fallbackSaving=false;closeBudgetPicker();}
  catch(e){fallbackSaving=false;if(fallbackOwner===id)$("fallback-result").textContent=String(e);paintFallbackModels();}
});
document.querySelectorAll("[data-counter]").forEach(e=>e.addEventListener("click",()=>Svc().SetCounter(e.dataset.counter)));
function syncRuleToastWindow(){
  const el=$("rule-toast"),height=snapshot?.compact&&!el.classList.contains("hidden")?Math.ceil(el.getBoundingClientRect().height)+55:0;
  if(height===ruleToastHeight)return;
  ruleToastHeight=height;const revision=++ruleToastRevision;
  ruleToastLayout=ruleToastLayout.catch(()=>{}).then(()=>{if(revision!==ruleToastRevision)return;return Svc().SetRuleToastSize?Svc().SetRuleToastSize(height):Svc().SetRuleToast?.(height>0);});
}
function dismissRuleToast(){clearTimeout(ruleToastTimer);$("rule-toast").classList.add("hidden");syncRuleToastWindow();}
function showRuleToast(row){
  clearTimeout(ruleToastTimer);const el=$("rule-toast"),actions=$("rule-toast-actions");actions.replaceChildren();
  const budgetStop=!row.detail||/^(Budget reached|Fallback allowance reached)\./.test(row.detail);
  $("rule-toast-text").textContent=row.stopped?(budgetStop?`${row.name} stopped. Raise the limit to continue.`:`${row.name} paused. ${row.detail}`):row.on_fallback?`${row.name} switched to ${bareModel(row.fallback_model)}`:row.detail==="Session resumed."?`${row.name} resumed.`:`${row.name} · ${row.detail}`;
  if(row.on_fallback&&!row.stopped){const stop=document.createElement("button");stop.className="ctl stop";stop.textContent="Stop session";stop.addEventListener("click",()=>{dismissRuleToast();stopSession(false,row.id);});actions.append(stop);}
  const dismiss=document.createElement("button");dismiss.className="ctl";dismiss.textContent="Dismiss";dismiss.addEventListener("click",dismissRuleToast);actions.append(dismiss);
  el.classList.toggle("stopped",row.stopped);el.classList.remove("hidden");syncRuleToastWindow();
  ruleToastTimer=setTimeout(dismissRuleToast,12000);
}
$("metadata-save").addEventListener("click",async()=>{try{await Svc().SetMetadataKey($("metadata-key").value);$("metadata-key").value="";$("metadata-result").textContent="Key saved locally";}catch(e){$("metadata-result").textContent=String(e);}});
$("metadata-refresh").addEventListener("click",async()=>{try{$("metadata-result").textContent=await Svc().RefreshMetadata();fallbackCache.clear();if(fallbackOwner)await loadFallbackModels(popupRow());}catch(e){$("metadata-result").textContent=String(e);}});
