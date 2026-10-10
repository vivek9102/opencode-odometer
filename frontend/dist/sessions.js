// Open-TUI selection and aggregate dock. Historical model usage stays global.
const budgetEdits=new Map(),budgetWrites=new Map();
let selectionTarget="",selectionRevision=0,selectionQueue=Promise.resolve(),budgetRevision=0,footerSession="";
function selectedSnapshot(snap) {
  if(!Array.isArray(snap.open_sessions))return snap;
  if(selectionTarget&&snap.open_sessions.some(r=>r.id===selectionTarget))snap={...snap,selected_session:selectionTarget};
  const row=snap.open_sessions.find(r=>r.id===snap.selected_session);
  const fb=!!row?.on_fallback,limit=fb?row.fallback_limit:row?.limit||0,cost=fb?row.fallback_spent||0:row?.cost||0;
  return {...snap,session_id:row?.session_id||"",session_cost:cost,limit,
    mode:row?.mode||"hard",budget_enabled:!!row?.enabled,budget_state:row?.state||"ok",
    fraction:fb?row.fallback_free?0:limit?cost/limit:0:row?.fraction||0,enforced:!!row?.enforced,grace_remaining:row?.grace_remaining||0,
    past_hard_stop:!!row?.past_hard_stop,hard_stop_at:1,warn_at:.75};
}
function renderOpenBudgetControls(snap){
  const row=snap.open_sessions.find(r=>r.id===snap.selected_session),draft=budgetEdits.get(row?.id),switched=footerSession!==row?.id;
  footerSession=row?.id||"";
  $("bd-enabled").checked=draft?draft.enabled:!!row?.enabled;
  const value=draft?draft.raw:row?.limit?String(row.limit):"";
  if((switched||draft||document.activeElement!==$("bd-limit"))&&$("bd-limit").value!==value)$("bd-limit").value=value;
  if(switched||draft||document.activeElement!==$("bd-mode-sel"))$("bd-mode-sel").value=draft?.mode||row?.mode||"hard";
}
function sessionColour(row){return row?.stopped?"var(--red)":row?.on_fallback||row?.stage==="switching"?"#60a5fa":row?.fraction>=1?"var(--red)":row?.fraction>=.75?"var(--amber)":"var(--green)";}
function sessionRank(r){return r.stopped||r.state==="over"?0:r.on_fallback||r.stage==="switching"?1:r.state==="warn"?2:r.enabled?3:r.session_id?4:5;}
function worstSession(snap){return [...(snap.open_sessions||[])].sort((a,b)=>sessionRank(a)-sessionRank(b)||b.fraction-a.fraction||a.name.localeCompare(b.name))[0];}
function dockName(name){
  const parts=(name||"").split("-");
  return parts.length>=3&&/^[a-f0-9]{8}$/i.test(parts.at(-1))?parts.slice(-3).join("-"):name;
}
function renderOpenSessions(snap){
  if(!Array.isArray(snap.open_sessions))return;
  const rows=snap.open_sessions,list=$("session-list"),scroll=list.scrollTop;
  const existing=new Map(Array.from(list.querySelectorAll(".session-row"),button=>[button.dataset.id,button]));
  $("session-count").textContent=`${rows.length} open`;
  if(!rows.length&&!list.querySelector(".sessions-empty")){const empty=document.createElement("div");empty.className="sessions-empty";empty.textContent="Open an OpenCode TUI to see its session here. Restart OpenCode after installing the companion plugin.";list.append(empty);}
  if(rows.length)list.querySelector(".sessions-empty")?.remove();
  for(const [index,row] of rows.entries()){
    const button=existing.get(row.id)||document.createElement("button"),fresh=!existing.has(row.id);
    existing.delete(row.id);button.type="button";button.classList.add("session-row");button.dataset.id=row.id;
    const selected=row.id===snap.selected_session;button.classList.toggle("selected",selected);button.setAttribute("aria-pressed",String(selected));
    if(fresh){
      const info=document.createElement("span");info.className="session-info";
      const title=document.createElement("span");title.className="session-name";
      const model=document.createElement("span");model.className="session-model";info.append(title,model);
      const budget=document.createElement("span");budget.className="session-measure";
      const spent=document.createElement("span"),bar=document.createElement("span");bar.className="session-progress";bar.append(document.createElement("i"));budget.append(spent,bar);
      const tag=document.createElement("span");tag.className="session-tag";
      button.append(info,budget,tag);button.addEventListener("click",()=>selectOpenSession(row.id));
    }
    const [info,budget,tag]=button.children,[title,model]=info.children,[spent,bar]=budget.children,fill=bar.firstElementChild;
    title.textContent=row.name;title.title=row.name;model.textContent=row.on_fallback?`↓ ${row.fallback_model}`:row.model||"idle · no model used yet";model.title=model.textContent;
    spent.textContent=row.enabled?`$${row.cost.toFixed(2)} / $${row.limit.toFixed(2)}`:`$${row.spent.toFixed(2)} · no limit`;
    fill.style.width=`${Math.min(100,(row.fraction||0)*100)}%`;fill.style.background=sessionColour(row);bar.classList.toggle("hidden",!row.enabled);budget.style.color=row.enabled?sessionColour(row):"var(--dim)";
    tag.textContent=selected?"▸ SELECTED":row.stopped?"STOPPED":row.stage==="switching"?"SWITCHING":row.on_fallback?`↓ ${bareModel(row.fallback_model)}`:row.state==="over"?"STOPPED":row.state==="warn"?"NEAR LIMIT":!row.session_id?"IDLE":row.enabled?"OK":"NO LIMIT";
    let extra=budget.querySelector(".session-fallback");if(row.on_fallback){if(!extra){extra=document.createElement("span");extra.className="session-fallback";budget.append(extra);}extra.textContent=row.fallback_free?"fallback is free":`fallback $${row.fallback_spent.toFixed(2)} / $${row.fallback_limit.toFixed(2)}`;extra.title=row.detail||extra.textContent;let mini=extra.querySelector("i");if(!row.fallback_free){mini=document.createElement("i");mini.style.width=`${Math.min(100,row.fallback_limit?row.fallback_spent/row.fallback_limit*100:0)}%`;extra.append(mini);}}else extra?.remove();
    if(list.children[index]!==button)list.insertBefore(button,list.children[index]||null);
  }
  for(const button of existing.values())button.remove();
  list.scrollTop=scroll;
  const selected=rows.find(r=>r.id===snap.selected_session);
  $("selected-name").textContent=selected?.name||"no open session";$("selected-name").title=selected?.name||"";
  $("selected-stage").textContent=selected?.on_fallback?`· fallback: ${bareModel(selected.fallback_model)}`:"";
  $("bd-limit-kind").textContent=selected?.on_fallback?"ORIGINAL $":"$";
  $("bd-limit").title=selected?.on_fallback?`Original-model limit: $${selected.limit.toFixed(2)}. The bar shows the separate fallback budget; Change edits that cap.`:"Original-model limit for this session";
  if(selected?.on_fallback)$("bd-budget-label").textContent=`Fallback · ${$("bd-budget-label").textContent}`;
  if(selected?.on_fallback&&selected.fallback_free){$("bd-budget-label").textContent="Fallback · free · no cap";$("bd-bar-fill").style.width="0%";}
  for(const id of ["bd-limit","bd-enabled","bd-mode-sel"])$(id).disabled=!selected;
  $("bd-limit").placeholder="No limit";
  $("bd-stop").disabled=stoppingSession||!selected?.session_id;
  $("bd-stop").textContent="STOP SESSION";
  const draft=budgetEdits.get(selected?.id);
  $("session-limit-note").textContent=draft?.needsAmount?"Enter an amount greater than zero to enable this limit.":draft?.pending?"Saving this session’s limit…":"Selected session only · every limit is hard · no grace turn · changing the cap keeps counted spending.";
  for(const id of budgetEdits.keys())if(!rows.some(r=>r.id===id))budgetEdits.delete(id);
}
async function selectOpenSession(id){
  const controls=[$("bd-limit"),$("bd-mode-sel")];controls.forEach(e=>e.blur());
  const revision=++selectionRevision;
  selectionTarget=id;
  if(snapshot)render({...snapshot,selected_session:id});
  const request=selectionQueue.catch(()=>{}).then(()=>Svc().SelectOpenSession(id));
  selectionQueue=request;
  try{await request;if(revision===selectionRevision){selectionTarget="";if(snapshot)render({...snapshot,selected_session:id});}}
  catch(e){if(revision===selectionRevision){selectionTarget="";toast(String(e),"over");}}
}
async function saveOpenBudget(enabled){
  const id=snapshot?.selected_session;
  if(!id)return;
  const raw=$("bd-limit").value.trim(),limit=raw===""?0:Number(raw),mode="hard";
  if(raw===""&&enabled&&snapshot.open_sessions.find(r=>r.id===id)?.enabled)enabled=false;
  const draft={raw,mode,enabled,revision:++budgetRevision,pending:false,needsAmount:enabled&&!(limit>0)};
  budgetEdits.set(id,draft);
  if(!Number.isFinite(limit)||limit<0){toast("Enter a finite, non-negative limit","over");render(snapshot);return;}
  if(draft.needsAmount){render(snapshot);$("bd-limit").focus();return;}
  draft.pending=true;render(snapshot);
  const request=(budgetWrites.get(id)||Promise.resolve()).catch(()=>{}).then(()=>Svc().SetOpenSessionBudget(id,limit,mode,enabled));
  budgetWrites.set(id,request);
  try{
    await request;
    const fresh=await Svc().Snapshot();
    if(budgetEdits.get(id)===draft){budgetEdits.delete(id);if(fresh)render({...fresh,selected_session:selectionTarget||snapshot?.selected_session||fresh.selected_session});}
  }catch(e){if(budgetEdits.get(id)===draft){budgetEdits.delete(id);toast(String(e),"over");if(snapshot)render(snapshot);}}
  finally{if(budgetWrites.get(id)===request)budgetWrites.delete(id);}
}
document.getElementById("bd-limit").addEventListener("input",()=>{
  const id=snapshot?.selected_session;if(!id||!Array.isArray(snapshot?.open_sessions))return;
  const previous=budgetEdits.get(id);
  budgetEdits.set(id,{...previous,raw:$("bd-limit").value,mode:$("bd-mode-sel").value,enabled:previous?.enabled??$("bd-enabled").checked});
});
function renderSessionDock(snap){
  if(!Array.isArray(snap.open_sessions))return;
  const candidate=worstSession(snap),worst=candidate?.enabled?candidate:null,rows=snap.open_sessions,n=rows.length,fb=worst?.on_fallback||worst?.stage==="switching",over=worst?.stopped||worst?.state==="over"&&!fb||!fb&&worst?.fraction>=1,near=!fb&&worst?.fraction>=.75;
  const active=rows.some(r=>r.active),count=`${n} session${n===1?"":"s"}`;
  const dockSnap={...snap,view:"RUN",cost:n?snap.open_cost||0:0,active,budget_enabled:!!worst,limit:worst?.limit||0,fraction:fb?0:worst?.fraction||0,mode:"hard",preferences:{...snap.preferences,remaining:false}};
  renderOdometer($("bar-odometer"),"bar",dockSnap);
  const state=worst?`${worst.name} · $${worst.cost.toFixed(2)} / $${worst.limit.toFixed(2)} · ${Math.round(worst.fraction*100)}%`:"no limits";
  const summary=over?"stopped":fb?"on fallback":near?"budget warning":worst?"all within budget":"no limits";
  $("bar").classList.add("session-dock");
  const caption=$("bar-model");caption.replaceChildren();
  if(fb&&!over){
    const name=document.createElement("span"),model=document.createElement("span");
    name.className=model.className="dock-line";name.textContent=`↓ ${dockName(worst.name)}`;
    model.textContent=worst.stage==="switching"?"Switching…":`→ ${bareModel(worst.fallback_model)}`;
    caption.append(name,model);
  }else caption.textContent=!n?"No open sessions":over?dockName(worst.name):near?`⚠ ${dockName(worst.name)} ${Math.round(worst.fraction*100)}%`:`${count} · ${summary}`;
  const fallbackDetail=fb?`\nFallback: ${worst.fallback_model} · ${worst.fallback_free?"free":`$${(worst.fallback_spent||0).toFixed(2)} / $${(worst.fallback_limit||0).toFixed(2)}`}`:"";
  $("bar-model").title=`${count} open · ${summary}\n${snap.preferences?.paused?"Enforcement paused · ":""}${state}${fallbackDetail}\n${rows.map(r=>r.name).join("\n")}`;
  $("bar-model").style.color=worst?sessionColour(worst):"var(--dim)";
  const rate=n?(Number.isFinite(snap.open_rate)?snap.open_rate:snap.rate||0):0;
  $("bar-rate").textContent=snap.preferences?.paused?"PAUSED":over?`$${(worst.on_fallback?worst.fallback_spent:worst.cost).toFixed(2)} / $${(worst.on_fallback?worst.fallback_limit:worst.limit).toFixed(2)}`:`$${rate.toFixed(2)}/hr`;
  $("bar-rate").title=over?`${worst.name} stopped. Raise or clear the limit to continue.`:`Burn rate across open sessions: $${rate.toFixed(2)}/hr`;
  $("bar-rate").style.color=over&&!snap.preferences?.paused?"var(--red)":rate>5?"var(--red)":rate>0?"#e4e4e7":"var(--dim)";
  $("bar").classList.toggle("session-over",!!over);
  $("bar-edge").classList.toggle("hidden",!worst);
  $("bar-edge").firstElementChild.style.width=`${over?100:Math.min(100,(worst?.fraction||0)*100)}%`;
  $("bar-edge").firstElementChild.style.background=sessionColour(worst);
  document.body.classList.toggle("quiet-idle",!!snap.preferences?.idle_dim&&!active&&!(worst?.fraction>=.75)&&!snap.unpriced_models&&!snap.preferences?.paused);
}
async function expandSessions(){
  const worst=worstSession(snapshot||{});
  if(worst)await selectOpenSession(worst.id);
  setView(true);
}
