import {test} from "node:test"
import assert from "node:assert/strict"
import {conversationIdentity,openConversations,hasConversation} from "./v2-sessions.js"

test("V2 identity follows conversations across internal tabs and terminal instances",()=>{
  let focused="a"
  const ctx={location:{directory:"demo"},ui:{router:{current:()=>focused?{type:"session",sessionID:focused}:{type:"home"}},tabs:{list:()=>[{sessionID:"a",title:"Greeting"},{sessionID:"b",title:"Greeting"}]}},data:{session:{root:id=>id==="child"?"a":id,get:id=>({id,title:"Greeting",time:{created:1000},model:{providerID:"mock",id:"chat"}}),status:id=>id==="a"?"running":"idle"}}}
  const identity=conversationIdentity("a")
  let rows=openConversations(ctx);assert.equal(rows.length,2);assert.ok(rows.find(r=>r.session_id==="a").focused)
  focused="b";rows=openConversations(ctx);assert.equal(rows.length,2);assert.ok(!rows.find(r=>r.session_id==="a").focused)
  assert.equal(rows.find(r=>r.session_id==="a").active,true)
  assert.deepEqual(conversationIdentity("a"),identity);assert.notEqual(conversationIdentity("b").id,identity.id)
  assert.ok(hasConversation({session_id:"b",tabs:rows},"a"));assert.ok(!hasConversation({tabs:rows},"closed"))
  focused="";assert.equal(openConversations(ctx).length,2)
  focused="child";assert.equal(openConversations(ctx).length,2)
  assert.deepEqual(conversationIdentity("chat-a"),{id:"v2-6b6e7f77762ce8de",name:"keen-robin-6b6e7f77"})
})
