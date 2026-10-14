import {test} from "node:test"
import assert from "node:assert/strict"
import {mkdtempSync,mkdirSync,writeFileSync,readFileSync,existsSync,rmSync} from "node:fs"
import {join} from "node:path"
import {tmpdir} from "node:os"
import adapter,{normalizeModel,normalizeUsage} from "./odometer-v2.js"

const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms))
async function until(fn,ms=7000){const end=Date.now()+ms;while(Date.now()<end){const value=fn();if(value)return value;await delay(30)}throw Error("Timed out waiting for bridge")}
test("V2 schema normalization preserves IDs, cache costs and unknown pricing",()=>{
  const m=normalizeModel({id:"nested/model",providerID:"mock",modelID:"canonical",name:"Chat",capabilities:{tools:true,input:["text"]},cost:[{input:1,output:2,cache:{read:.1,write:.3}}],limit:{context:200000}})
  assert.equal(m.key,"mock/nested/model");assert.equal(m.rate.cache_write,.3)
  assert.equal(normalizeModel({id:"unknown",providerID:"mock",cost:[{input:0,output:0}]}).rate,undefined)
  const r=normalizeUsage({type:"assistant",id:"msg_a",model:{id:"nested/model",providerID:"mock"},tokens:{input:3,output:4,cache:{read:2}},cost:.03,finish:"stop",content:[{text:"PRIVATE TEXT"}],time:{created:123}},"ses_a")
  assert.equal(r.modelID,"nested/model");assert.equal(r.tokens.cache.read,2);assert.equal(r.timeCreated,123)
  assert.ok(!JSON.stringify(r).includes("PRIVATE TEXT"))
})

test("V2 shared server supports two TUIs, hard budgets, model selection and one continuation",async()=>{
  const dir=mkdtempSync(join(tmpdir(),"odo-v2-"));mkdirSync(dir,{recursive:true})
  Object.assign(process.env,{OPENCODE_ODOMETER_DIR:dir,OPENCODE_ODOMETER_POINTER:join(dir,"pointer.json"),OPENCODE_ODOMETER_NOBLOCK:"0"})
  writeFileSync(join(dir,"preferences.json"),JSON.stringify({auto_start:false}))
  const json=(name,data)=>writeFileSync(join(dir,name),JSON.stringify(data))
  const load=name=>{try{return JSON.parse(readFileSync(join(dir,name)))}catch{return null}}
  let ready=false,aborts=[],prompts=[],switched=[],hooks={},queue=[],wake
  const info={ses_a:{id:"ses_a",model:{providerID:"mock",id:"original"}},ses_b:{id:"ses_b",model:{providerID:"mock",id:"original"}}}
  const history={ses_a:[{type:"user",id:"msg_user",prompt:{text:"Original deep task"}},{type:"assistant",id:"msg_old",model:info.ses_a.model,finish:"tool-calls",content:[],tokens:{input:20,output:10,cache:{read:0,write:0}},cost:.2,time:{created:Date.now()}}],ses_b:[]}
  const ctx={app:{version:"2.0.28"},location:{directory:"mock-project"},
    model:{list:async()=>({data:[{providerID:"mock",id:"original",name:"Original",cost:[{input:3,output:15}],capabilities:{tools:true,input:["text"]}},{providerID:"mock",id:"cheap",name:"Cheap",cost:[{input:.1,output:.2}],capabilities:{tools:true,input:["text"]}}]})},
    tool:{hook:async(name,fn)=>{hooks["tool"]=fn}},
    session:{
      hook:async(name,fn)=>{hooks[name]=fn},get:async({sessionID})=>info[sessionID],context:async({sessionID})=>history[sessionID],
      switchModel:async({sessionID,model})=>{switched.push({sessionID,model});info[sessionID].model=model},
      interrupt:async({sessionID})=>{aborts.push(sessionID);return{interrupted:true}},wait:async()=>{},
      prompt:async req=>{
        await hooks.prompt({sessionID:req.sessionID,messageID:req.id,prompt:{text:req.text}})
        prompts.push(req);history[req.sessionID].push({type:"user",id:req.id,prompt:{text:req.text}},{type:"assistant",id:"msg_fallback",model:info[req.sessionID].model,finish:"stop",content:[],tokens:{input:2,output:3,cache:{read:0,write:0}},cost:.01,time:{created:Date.now()}})
        return{id:req.id}
      },
    },event:{async *subscribe({signal}){while(!signal.aborted){if(queue.length){yield queue.shift();continue}await new Promise(resolve=>{wake=resolve;signal.addEventListener("abort",resolve,{once:true})})}}},
  }
  const presence=(pid,sid,id)=>json(`tui-${id}.json`,{id,pid,server_pid:process.pid,api_major:2,session_id:sid,location:"mock-project",closed:false,updated:Date.now()/1000})
  presence(9001,"ses_a","a");presence(9002,"ses_b","b")
  const stop=await adapter.setup(ctx)
  try{
    const inventory=await until(()=>load("models-9001.json"));assert.equal(inventory.server_pid,process.pid);assert.equal(inventory.continue_protocol,2)
    await until(()=>load("models-9002.json"));assert.equal(load("models-9002.json").sessions[0],"ses_b")
    const doc={enabled:true,updated:Date.now()/1000,free_models:[],accounted:{msg_old:"20/10/0/0/0/tool-calls"},sessions:{ses_a:{strict:true,stage:"original",state:"over",cost:.2,limit:.2,enforced:true,budget_session_id:"a"}}}
    json("budget.json",doc)
    await assert.rejects(hooks.context({sessionID:"ses_a",model:info.ses_a.model}),/Budget limit/)
    await hooks.context({sessionID:"ses_b",model:info.ses_b.model})
    // A model's friendly name never grants a free bypass.
    await assert.rejects(hooks.context({sessionID:"ses_a",model:{providerID:"mock",id:"sovereign-free"}}),/Budget limit/)
    const req={id:"transition-a",session_id:"ses_a",instance:inventory.instance,key:"mock/cheap",issued:Date.now()/1000,children:[],input_price:.1,output_price:.2,context:200000,input_modalities:["text"]}
    Object.assign(doc.sessions.ses_a,{stage:"switching",generation:req.id,fallback_model:req.key})
    json("budget.json",doc);json("continue-9001-transition-a.json",req)
    await until(()=>load("continue-status-transition-a.json")?.status==="prepared")
    assert.deepEqual(aborts,["ses_a"]);assert.equal(prompts.length,0)
    Object.assign(doc.sessions.ses_a,{stage:"fallback",state:"ok",cost:0,limit:.1});json("budget.json",doc)
    const ack=await until(()=>{const a=load("continue-status-transition-a.json");return ["confirmed","failed"].includes(a?.status)&&a})
    assert.equal(ack.status,"confirmed",ack.detail);assert.equal(prompts.length,1);assert.equal(info.ses_a.model.id,"cheap");assert.equal(info.ses_b.model.id,"original")
    assert.match(prompts[0].text,/scope, depth, deliverables/)
    json("continue-9001-transition-a.json",req);await delay(200);assert.equal(prompts.length,1)
    // The cap blocks the next paid step after accounting acknowledges it.
    doc.accounted.msg_fallback="2/3/0/0/0/stop";doc.sessions.ses_a.state="over";doc.sessions.ses_a.cost=.1;json("budget.json",doc)
    await assert.rejects(hooks.context({sessionID:"ses_a",model:info.ses_a.model}),/Budget limit/)
    // Clearing the budget pin restores the previous selection without
    // altering the other session or making an inference request.
    doc.sessions={};json("budget.json",doc);rmSync(join(dir,"switch-session-ses_a.json"));await until(()=>info.ses_a.model.id==="original")
    assert.equal(prompts.length,1)
    const rows=readFileSync(join(dir,"events.jsonl"),"utf8").trim().split("\n").map(JSON.parse).filter(r=>r.id==="msg_old")
    assert.equal(rows.length,1,"history polling duplicated identical usage")
    ready=true
  }finally{stop();wake?.();await delay(100);rmSync(dir,{recursive:true,force:true})}
  assert.ok(ready)
})
