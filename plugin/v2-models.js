// Public catalog facts only. Missing prices remain unknown.
export function normalizeModel(m){
  const name=m.name||m.id,description=`${m.id} ${m.modelID||""} ${name}`.toLowerCase()
  const category=/embed|rerank/.test(description)?"embedding":/whisper|\basr\b|transcri|\btts\b/.test(description)?"audio":/image.?gen|dall.e|stable.?diffusion/.test(description)?"image":"chat"
  const rate=(m.cost||[]).find(r=>!r.tier)||(m.cost||[])[0]
  return{key:m.providerID&&m.id?`${m.providerID}/${m.id}`:"",name,category,canonical_model_id:m.modelID||"",tool_call:m.capabilities?.tools,
    context:m.limit?.context||0,input_modalities:m.capabilities?.input||[],
    ...(rate&&(rate.input>0||rate.output>0)?{rate:{input:rate.input,output:rate.output,cache_read:rate.cache?.read||0,cache_write:rate.cache?.write||0}}:{})}
}
