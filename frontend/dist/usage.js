// Local calendar reporting. Internal RUN/TRIP/TOTAL keys remain compatible
// with saved preferences; user-facing labels describe their actual scope.
(()=>{
  const modal=document.getElementById('usage-modal'),byId=id=>document.getElementById(id);
  let generation=0,loaded=null,previousFocus=null;
  const dateText=d=>`${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,'0')}-${String(d.getDate()).padStart(2,'0')}`;
  const money=n=>'$'+Number(n||0).toFixed(4),tokens=n=>Number(n||0).toLocaleString();
  const node=(tag,text,cls)=>{const e=document.createElement(tag);if(text!==undefined)e.textContent=text;if(cls)e.className=cls;return e;};
  function dates(){
    const end=new Date(),start=new Date(end),period=byId('usage-period').value;
    if(period==='custom')return;
    if(period==='week')start.setDate(start.getDate()-((start.getDay()+6)%7));
    if(period==='month')start.setDate(1);
    byId('usage-start').value=dateText(start);byId('usage-end').value=dateText(end);
  }
  function showReport(report){
    loaded=report;
    byId('usage-calendar').textContent=report.calendar;
    const summary=byId('usage-summary');summary.replaceChildren();
    for(const [label,value] of [['Spend',money(report.cost)],['Tokens',tokens(report.tokens)],['Messages',tokens(report.messages)],['Free messages',tokens(report.free_messages)]]){
      const card=node('div',undefined,'usage-stat');card.append(node('span',label),node('strong',value));summary.append(card);
    }
    const grouped=new Map(),daily=new Map();
    for(const r of report.rows||[]){
      daily.set(r.date,(daily.get(r.date)||0)+r.cost);
      const key=r.provider+'/'+r.model,old=grouped.get(key)||{cost:0,messages:0,tokens:0,estimated:false,unknown:false};
      old.cost+=r.cost;old.messages+=r.messages;old.tokens+=r.input+r.output+r.cache_read+r.cache_write;old.estimated||=r.estimated;old.unknown||=r.unknown;grouped.set(key,old);
    }
    const chart=byId('usage-chart');chart.replaceChildren();
    const values=[...daily].sort(([a],[b])=>a.localeCompare(b)),max=Math.max(...values.map(([,v])=>v),.000001);
    // Keep wide date ranges bounded: show up to 31 consecutive calendar
    // buckets. The CSV retains every day/model in the selected range.
    const from=new Date(report.start+'T12:00:00'),to=new Date(report.end+'T12:00:00');
    const days=Math.round((Date.UTC(to.getFullYear(),to.getMonth(),to.getDate())-Date.UTC(from.getFullYear(),from.getMonth(),from.getDate()))/86400000)+1,step=Math.max(1,Math.ceil(days/31));
    let peak=max,points=[];
    for(let offset=0;offset<days;offset+=step){const first=new Date(from);first.setDate(first.getDate()+offset);let total=0;for(let j=0;j<step&&offset+j<days;j++){const d=new Date(first);d.setDate(d.getDate()+j);total+=daily.get(dateText(d))||0;}points.push({date:dateText(first),cost:total});peak=Math.max(peak,total);}
    for(const p of points){const col=node('div',undefined,'usage-day');col.title=`${p.date}${step>1?' · '+step+'-day bucket':''}: ${money(p.cost)}`;const bar=node('div',undefined,'usage-day-bar');bar.style.height=Math.max(2,Math.round(p.cost/peak*78))+'px';col.append(bar,node('small',p.date.slice(5)));chart.append(col);}
    chart.setAttribute('aria-label',`Daily consumption ${report.start} to ${report.end}; ${money(report.cost)} total. Each bar has its date and amount in a tooltip.`);
    const table=byId('usage-rows');table.replaceChildren();
    for(const [key,r] of [...grouped].sort((a,b)=>b[1].cost-a[1].cost||a[0].localeCompare(b[0]))){const tr=node('tr'),name=node('td',key);if(r.unknown)name.append(node('small','Price unknown','usage-note'));else if(r.estimated)name.append(node('small','Includes estimated pricing','usage-note'));tr.append(name,node('td',tokens(r.messages),'num'),node('td',tokens(r.tokens),'num'),node('td',money(r.cost),'num'));table.append(tr);}
    if(!grouped.size){const tr=node('tr'),td=node('td','No recorded usage in this period.');td.colSpan=4;tr.append(td);table.append(tr);}
    const coverage=[];
    if(report.estimated_cost)coverage.push(`${money(report.estimated_cost)} uses estimated pricing.`);
    if(report.unknown_messages)coverage.push(`${report.unknown_messages} messages have unknown prices; spend may be understated.`);
    if(report.unallocated_cost)coverage.push(`${money(report.unallocated_cost)} of older lifetime spend has no recoverable date detail and is excluded from dated reports.`);
    if(report.undated_messages)coverage.push(`${report.undated_messages} undated messages are excluded.`);
    if(report.first_date)coverage.push(`Earliest available day: ${report.first_date}.`);
    byId('usage-coverage').textContent=coverage.join(' ');
    const s=report.storage||{},mib=n=>(Number(n||0)/1048576).toFixed(2)+' MiB';
    byId('usage-storage').textContent=`Local storage: history ${mib(s.ledger_bytes)} · telemetry ${mib(s.telemetry_bytes)} · protocol ${mib(s.protocol_bytes)} · exports ${mib(s.export_bytes)}. ${tokens(s.retained_messages)} detailed messages; ${tokens(s.archived_days)} archived days. Exports are kept.`;
  }
  async function load(){const token=++generation;loaded=null;byId('usage-export').disabled=true;byId('usage-result').textContent='Loading…';try{const report=await Svc().UsageReport(byId('usage-start').value,byId('usage-end').value);if(token!==generation)return;showReport(report);byId('usage-result').textContent='';byId('usage-export').disabled=false;}catch(e){if(token===generation){byId('usage-result').textContent=String(e);byId('usage-summary').replaceChildren();byId('usage-chart').replaceChildren();byId('usage-rows').replaceChildren();}}}
  function close(){generation++;modal.classList.add('hidden');previousFocus?.focus();}
  byId('bd-usage').addEventListener('click',()=>{previousFocus=document.activeElement;modal.classList.remove('hidden');dates();byId('usage-period').focus();void load();});
  byId('usage-close').addEventListener('click',close);modal.querySelector('.modal-backdrop').addEventListener('click',close);
  modal.addEventListener('keydown',e=>{
    if(e.key==='Escape'){e.preventDefault();e.stopPropagation();close();}
    if(e.key==='Tab'){const controls=[...modal.querySelectorAll('button,input,select')].filter(e=>!e.disabled&&e.getClientRects().length),first=controls[0],last=controls.at(-1);if(e.shiftKey&&document.activeElement===first){e.preventDefault();last.focus();}else if(!e.shiftKey&&document.activeElement===last){e.preventDefault();first.focus();}}
  });
  byId('usage-period').addEventListener('change',()=>{dates();if(byId('usage-period').value!=='custom')void load();});
  for(const id of ['usage-start','usage-end'])byId(id).addEventListener('change',()=>{byId('usage-period').value='custom';loaded=null;byId('usage-export').disabled=true;byId('usage-result').textContent='Press SHOW to apply these dates.';});
  byId('usage-apply').addEventListener('click',()=>void load());
  byId('usage-export').addEventListener('click',async()=>{if(!loaded)return;byId('usage-export').disabled=true;const token=generation;try{const path=await Svc().ExportUsageCsv(loaded.start,loaded.end);if(token===generation)byId('usage-result').textContent='Saved: '+path;}catch(e){if(token===generation)byId('usage-result').textContent=String(e);}finally{if(token===generation&&loaded)byId('usage-export').disabled=false;}});
})();
