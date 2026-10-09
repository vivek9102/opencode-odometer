// Open-TUI selection and aggregate dock. Historical model usage stays global.
const budgetEdits=new Map(),budgetWrites=new Map();
let selectionTarget="",selectionRevision=0,selectionQueue=Promise.resolve(),budgetRevision=0,footerSession="";
function selectedSnapshot(snap) {
  if(!Array.isArray(snap.open_sessions))return snap;
  if(selectionTarget&&snap.open_sessions.some(r=>r.id===selectionTarget))snap={...snap,selected_session:selectionTarget};
  const row=snap.open_sessions.find(r=>r.id===snap.selected_session);
  return {...snap,session_id:row?.session_id||"",session_cost:row?.cost||0,limit:row?.limit||0,
    mode:row?.mode||"hard",budget_enabled:!!row?.enabled,budget_state:row?.state||"ok",
    fraction:row?.fraction||0,enforced:!!row?.enforced,grace_remaining:row?.grace_remaining||0,
    past_hard_stop:!!row?.past_hard_stop,hard_stop_at:1.5,warn_at:.75};
}
function renderOpenBudgetControls(snap){
  const row=snap.open_sessions.find(r=>r.id===snap.selected_session),draft=budgetEdits.get(row?.id),switched=footerSession!==row?.id;
  footerSession=row?.id||"";
  $("bd-enabled").checked=draft?draft.enabled:!!row?.enabled;
  const value=draft?draft.raw:row?.limit?String(row.limit):"";
  if((switched||draft||document.activeElement!==$("bd-limit"))&&$("bd-limit").value!==value)$("bd-limit").value=value;
  if(switched||draft||document.activeElement!==$("bd-mode-sel"))$("bd-mode-sel").value=draft?.mode||row?.mode||"hard";
}
function sessionColour(row){return row?.fraction>=1?"var(--red)":row?.fraction>=.75?"var(--amber)":"var(--green)";}
function worstSession(snap){return (snap.open_sessions||[]).filter(r=>r.enabled).sort((a,b)=>b.fraction-a.fraction||a.name.localeCompare(b.name))[0];}
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
    title.textContent=row.name;title.title=row.name;model.textContent=row.model||"idle · no model used yet";model.title=model.textContent;
    spent.textContent=row.enabled?`$${row.cost.toFixed(2)} / $${row.limit.toFixed(2)}`:`$${row.spent.toFixed(2)} · no limit`;
    fill.style.width=`${Math.min(100,(row.fraction||0)*100)}%`;fill.style.background=sessionColour(row);bar.classList.toggle("hidden",!row.enabled);budget.style.color=row.enabled?sessionColour(row):"var(--dim)";
    tag.textContent=selected?"▸ SELECTED":row.state==="over"?"OVER":row.state==="warn"?`${Math.round(row.fraction*100)}%`:!row.session_id?"IDLE":row.enabled?"OK":"NO LIMIT";
    if(list.children[index]!==button)list.insertBefore(button,list.children[index]||null);
  }
  for(const button of existing.values())button.remove();
  list.scrollTop=scroll;
  const selected=rows.find(r=>r.id===snap.selected_session);
  $("selected-name").textContent=selected?.name||"no open session";$("selected-name").title=selected?.name||"";
  for(const id of ["bd-limit","bd-enabled","bd-mode-sel"])$(id).disabled=!selected;
  $("bd-limit").placeholder="No limit";
  $("bd-stop").disabled=stoppingSession||!selected?.session_id;
  const draft=budgetEdits.get(selected?.id);
  $("session-limit-note").textContent=draft?.needsAmount?"Enter an amount greater than zero to enable this limit.":draft?.pending?"Saving this session’s limit…":selected?.enabled?`Selected session only · ${selected.mode==="soft"?"one grace turn, hard stop at 150%":"paid requests block at 100%"}. Changing the cap keeps counted spending.`:"New sessions have no limit. Enter an amount to set a limit for this session.";
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
  const raw=$("bd-limit").value.trim(),limit=raw===""?0:Number(raw),mode=$("bd-mode-sel").value;
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
  const worst=worstSession(snap),rows=snap.open_sessions,n=rows.length,over=worst?.fraction>=1,near=worst?.fraction>=.75;
  const active=rows.some(r=>r.active),count=`${n} session${n===1?"":"s"}`;
  const dockSnap={...snap,cost:n?snap.open_cost||0:0,active,budget_enabled:!!worst,limit:worst?.limit||0,fraction:worst?.fraction||0,mode:"hard",preferences:{...snap.preferences,remaining:false}};
  renderOdometer($("bar-odometer"),"bar",dockSnap);
  const state=worst?`${worst.name} · $${worst.cost.toFixed(2)} / $${worst.limit.toFixed(2)} · ${Math.round(worst.fraction*100)}%`:"no limits";
  const summary=over?"budget exceeded":near?"budget warning":worst?"all within budget":"no limits";
  $("bar").classList.add("session-dock");
  $("bar-model").textContent=!n?"No open sessions":over?dockName(worst.name):near?`⚠ ${dockName(worst.name)} ${Math.round(worst.fraction*100)}%`:`${count} · ${summary}`;
  $("bar-model").title=`${count} open · ${summary}\n${snap.preferences?.paused?"Enforcement paused · ":""}${state}\n${rows.map(r=>r.name).join("\n")}`;
  $("bar-model").style.color=worst?sessionColour(worst):"var(--dim)";
  const rate=n?(Number.isFinite(snap.open_rate)?snap.open_rate:snap.rate||0):0;
  $("bar-rate").textContent=snap.preferences?.paused?"PAUSED":over?`$${worst.cost.toFixed(2)} / $${worst.limit.toFixed(2)}`:`$${rate.toFixed(2)}/hr`;
  $("bar-rate").style.color=over&&!snap.preferences?.paused?"var(--red)":rate>5?"var(--red)":rate>0?"#e4e4e7":"var(--dim)";
  $("bar").classList.toggle("session-over",!!over);
  $("bar-edge").classList.toggle("hidden",!worst);
  $("bar-edge").firstElementChild.style.width=`${Math.min(100,(worst?.fraction||0)*100)}%`;
  $("bar-edge").firstElementChild.style.background=sessionColour(worst);
  document.body.classList.toggle("quiet-idle",!!snap.preferences?.idle_dim&&!active&&!(worst?.fraction>=.75)&&!snap.unpriced_models&&!snap.preferences?.paused);
}
async function expandSessions(){
  const worst=worstSession(snapshot||{});
  if(worst?.fraction>=1)await selectOpenSession(worst.id);
  setView(true);
}
