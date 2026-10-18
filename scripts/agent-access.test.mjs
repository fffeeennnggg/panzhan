import test from 'node:test';
import assert from 'node:assert/strict';
import {agentAccessHTML} from '../internal/web/assets/agent-access.mjs';
test('agent grant UI shows scoped state and escapes untrusted names and credentials',()=>{
 const grant={id:'g',name:'<img src=x>',scopes:['resources:read'],active:true,download_root:'/media',directory:'docs',expires_at:'2027-01-01',revoked:false};
 const html=agentAccessHTML([grant],null,'/media','','http://nas:9867');
 assert.ok(html.includes('http://nas:9867/mcp'));assert.ok(html.includes('&lt;img src=x&gt;'));assert.ok(!html.includes('<img src=x>'));assert.ok(html.includes('搜索与只读查询'));assert.ok(!html.includes('value="private-token"'));assert.ok(html.includes('data-revoke-agent="g"'));
 const fresh=agentAccessHTML([], {token:'secret<&"'},'/media','','http://nas:9867');assert.ok(fresh.includes('secret&lt;&amp;&quot;'));assert.ok(fresh.includes('readonly'));assert.ok(fresh.includes('完整令牌只在创建时返回'));
});
