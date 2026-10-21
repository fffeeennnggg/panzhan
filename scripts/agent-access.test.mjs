import test from 'node:test';
import assert from 'node:assert/strict';
import {agentAccessHTML} from '../internal/web/assets/agent-access.mjs';
test('agent grant UI renders inherited directory and configured category mappings',()=>{
 const grant={id:'g',name:'<img src=x>',scopes:['resources:read'],active:true,download_root:'/media',directory:'docs',expires_at:'2027-01-01',revoked:false};
 grant.category_dirs={movie:'docs/Movies'};
 const html=agentAccessHTML([grant],null,'/media','docs','http://nas:9867',{movie:'docs/Movies'});
 assert.ok(html.includes('http://nas:9867/mcp'));assert.ok(html.includes('&lt;img src=x&gt;'));assert.ok(!html.includes('<img src=x>'));assert.ok(html.includes('搜索与只读查询'));assert.ok(!html.includes('value="private-token"'));assert.ok(html.includes('data-revoke-agent="g"'));
 assert.ok(!html.includes('name="agent_directory"'));assert.ok(html.includes('电影'));assert.ok(html.includes('电视剧'));assert.ok(html.includes('动漫'));assert.ok(html.includes('音乐'));assert.ok(html.includes('学习资料'));assert.ok(html.includes('软件'));assert.ok(html.includes('data-agent-category-browse="movie"'));assert.ok(html.includes('docs/Movies'));assert.ok(html.includes('未设置'));
 const fresh=agentAccessHTML([], {token:'secret<&"'},'/media','','http://nas:9867');assert.ok(fresh.includes('secret&lt;&amp;&quot;'));assert.ok(fresh.includes('readonly'));assert.ok(fresh.includes('完整令牌只在创建时返回'));
});
