import test from 'node:test';
import assert from 'node:assert/strict';
import {mergeSearchResults, selectResults, highlight, resultKey} from '../internal/web/assets/search-results.mjs';

test('merge preserves passwords, origins, newer checks and distinct folders',()=>{
 const old={title:'Blender',url:'https://pan.quark.cn/s/abc?pwd=1234',local:true,origins:[{id:'a',name:'A'}],checked_at:'2026-10-15T00:00:00Z',availability:'accessible'};
 const results=mergeSearchResults([old],[{title:'Blender',url:'https://pan.quark.cn/s/abc',origins:[{id:'b',name:'B'}]}, {title:'Blender',url:'https://pan.quark.cn/s/def'}, {title:'Blender',url:'https://pan.quark.cn/s/abc#/list/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'}]);
 assert.equal(results.length,3);assert.equal(results[0].url,old.url);assert.equal(results[0].origins.length,2);assert.equal(results[0].checked_at,old.checked_at);assert.equal(results[0].local,false);
 assert.notEqual(resultKey(results[0]),resultKey(results[2]));
 assert.equal(mergeSearchResults([],[{url:'https://pan.quark.cn.evil.test/s/abc'}]).length,0);
});
test('source and discovery filters, relevance and recency sorting',()=>{
 const rows=[{id:'a',title:'其他介绍',origins:[{id:'a'}],discovered_at:'2026-10-01T00:00:00Z'}, {id:'b',title:'Blender',origins:[{id:'b'}],discovered_at:'2026-10-14T00:00:00Z'}, {id:'c',title:'Blender 教程',origins:[{id:'a'}],discovered_at:'2026-10-15T00:00:00Z'}];
 assert.equal(selectResults(rows,{query:'Blender'})[0].id,'b');
 assert.deepEqual(selectResults(rows,{source:'a',days:7,now:Date.parse('2026-10-16T00:00:00Z')}).map(r=>r.id),['c']);
 assert.equal(selectResults(rows,{sort:'newest'})[0].id,'c');
 assert.equal(rows[0].id,'a');
});
test('highlight escapes HTML and treats metacharacters literally',()=>{
 assert.equal(highlight('<img src=x onerror=evil()> Blender','blender'),'&lt;img src=x onerror=evil()&gt; <mark>Blender</mark>');
 assert.equal(highlight('C++ [课]','C++'),' <mark>C++</mark> [课]'.trim());
 assert.equal(highlight('Blender & 教程','Blender 教程'),'<mark>Blender</mark> &amp; <mark>教程</mark>');
});
