// Shared, testable presence transport; no credentials or conversation text.
import {randomUUID} from "node:crypto"
import {mkdirSync,readFileSync,writeFileSync,renameSync} from "node:fs"
import {join} from "node:path"
import {homedir} from "node:os"
import {spawn} from "node:child_process"

export function createPresence() {
  const id=randomUUID(),started=Date.now()
  const words=["calm","bright","swift","clear","steady","keen"]
  const animals=["fox","owl","panda","lynx","otter","robin"]
  const n=parseInt(id.slice(0,2),16)
  const name=`${words[n%words.length]}-${animals[Math.floor(n/words.length)%animals.length]}-${id.slice(0,8)}`
  const fallback=process.env.OPENCODE_ODOMETER_DIR || (process.platform==="win32"?join(process.env.LOCALAPPDATA||homedir(),"OpenCodeOdometer"):process.platform==="darwin"?join(homedir(),"Library","Application Support","OpenCodeOdometer"):join(process.env.XDG_DATA_HOME||join(homedir(),".local","share"),"OpenCodeOdometer"))
  let lastDir="",closed=false
  function publish(state={}) {
    try {
      let pointer
      try{pointer=JSON.parse(readFileSync(process.env.OPENCODE_ODOMETER_POINTER||join(homedir(),".opencode-odometer.json"),"utf8"))}catch{}
      const dir=pointer?.data_dir||fallback
      const body={id,name,pid:process.pid,started,updated:Math.floor(Date.now()/1000),session_id:state.sessionID||"",model:state.model||"",active:!!state.active,closed,continue_protocol:state.continueProtocol||1,
        ...(state.apiMajor===2?{api_major:2,server_pid:state.serverPID||0,location:state.location||""}:{})}
      const write=(d,entry)=>{mkdirSync(d,{recursive:true});const file=join(d,`tui-${id}.json`),tmp=file+".tmp";writeFileSync(tmp,JSON.stringify(entry));renameSync(tmp,file)}
      if(lastDir && lastDir!==dir)write(lastDir,{...body,closed:true})
      write(dir,body);lastDir=dir
      return body
    }catch{return null}
  }
  function startV2Widget(){
    try{
      const p=JSON.parse(readFileSync(process.env.OPENCODE_ODOMETER_POINTER||join(homedir(),".opencode-odometer.json"),"utf8"))
      if(p.api_major!==2||!p.executable||!p.data_dir)return
      try{if(JSON.parse(readFileSync(join(p.data_dir,"preferences.json"),"utf8")).auto_start===false)return}catch{}
      try{if(JSON.parse(readFileSync(join(p.data_dir,"launch-suppressed.json"),"utf8")).instances?.includes(id))return}catch{}
      spawn(p.executable,["--autostart"],{detached:true,stdio:"ignore",windowsHide:true}).unref()
    }catch{}
  }
  return {id,name,publish,directory:()=>lastDir,startV2Widget,close(){closed=true;publish()}}
}
