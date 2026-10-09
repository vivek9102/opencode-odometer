// Shared, testable presence transport; no credentials or conversation text.
import {randomUUID} from "node:crypto"
import {mkdirSync,readFileSync,writeFileSync,renameSync} from "node:fs"
import {join} from "node:path"
import {homedir} from "node:os"

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
      const body={id,name,pid:process.pid,started,updated:Math.floor(Date.now()/1000),session_id:state.sessionID||"",model:state.model||"",active:!!state.active,closed}
      const write=(d,entry)=>{mkdirSync(d,{recursive:true});const file=join(d,`tui-${id}.json`),tmp=file+".tmp";writeFileSync(tmp,JSON.stringify(entry));renameSync(tmp,file)}
      if(lastDir && lastDir!==dir)write(lastDir,{...body,closed:true})
      write(dir,body);lastDir=dir
      return body
    }catch{return null}
  }
  return {id,name,publish,directory:()=>lastDir,close(){closed=true;publish()}}
}
