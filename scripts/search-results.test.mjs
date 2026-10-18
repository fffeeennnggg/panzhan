import test from 'node:test';
import assert from 'node:assert/strict';
import {mergeSearchResults, selectResults, highlight, resultKey, contentBadge, contentDescription} from '../internal/web/assets/search-results.mjs';

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

test('quality prefers recently verified matching resources, then advertised resolution',()=>{
 const now=Date.now(),verified={availability:'accessible',checked_at:new Date(now).toISOString()};
 const rows=[{id:'unknown',title:'电影 4K'},{id:'1080',title:'电影 1080P',...verified},{id:'4k',title:'电影 4K HDR',...verified},{id:'exact',title:'电影',...verified},{id:'bad',title:'电影 4K',availability:'invalid'},{id:'pending',title:'电影',availability:'checking'}];
 assert.deepEqual(selectResults(rows,{query:'电影',now}).map(r=>r.id),['4k','1080','exact','unknown']);
 assert.equal(selectResults([{id:'stale',title:'电影 1080P',availability:'accessible',checked_at:new Date(now-86400000).toISOString()},{id:'fresh',title:'电影 4K'}],{query:'电影',now})[0].id,'fresh');
});

test('a corrected password triggers a fresh check instead of preserving a password failure',()=>{
 const [row]=mergeSearchResults([{url:'https://pan.quark.cn/s/abc?pwd=wrong',checked_at:new Date().toISOString(),availability:'password_required'}],[{url:'https://pan.quark.cn/s/abc?pwd=good'}]);
 assert.equal(row.checked_at,'');assert.equal(row.availability,'unknown');
});

test('content metadata shows actual types and sizes and labels partial totals',()=>{
 const row={content:{file_count:2,folder_count:1,total_bytes:3*1024**3,unknown_size:0,types:{video:2},extensions:['mkv','mp4'],complete:true}};
 assert.equal(contentBadge(row),'视频');assert.equal(contentDescription(row),'视频 2 个 · MKV / MP4 · 总大小 3.0 GiB');
 assert.match(contentDescription({content:{...row.content,complete:false}}),/未统计完整.*部分内容/);
 assert.match(contentDescription({content:{...row.content,unknown_size:1}}),/未统计完整/);
 assert.equal(contentBadge({content:{types:{video:1,document:1}}}),'混合文件');
 assert.match(contentDescription({content:{file_count:0,folder_count:2,total_bytes:0,unknown_size:0,types:{},complete:false}}),/文件夹.*大小暂未统计/);
 const [merged]=mergeSearchResults([{url:'https://pan.quark.cn/s/abc',checked_at:new Date().toISOString(),...row}],[{url:'https://pan.quark.cn/s/abc'}]);assert.deepEqual(merged.content,row.content);
});

test('file type filters use actual contents, include mixed shares and combine with source filters',()=>{
 const rows=[
  {id:'video',title:'教程',origins:[{id:'a'}],content:{file_count:2,folder_count:1,types:{video:2}}},
  {id:'mixed',title:'教程',origins:[{id:'b'}],content:{file_count:2,types:{video:1,document:1}}},
  {id:'archive',title:'4K 视频教程',content:{file_count:1,types:{archive:1}}},
  {id:'unknown',title:'视频教程'},
  {id:'invalid',title:'教程',availability:'invalid',content:{types:{video:1}}}
 ];
 assert.deepEqual(selectResults(rows,{type:'video'}).map(r=>r.id).sort(),['mixed','video']);
 assert.deepEqual(selectResults(rows,{type:'document'}).map(r=>r.id),['mixed']);
 assert.deepEqual(selectResults(rows,{type:'archive'}).map(r=>r.id),['archive']);
 assert.deepEqual(selectResults(rows,{type:'mixed'}).map(r=>r.id),['mixed']);
 assert.deepEqual(selectResults(rows,{type:'folder'}).map(r=>r.id),['video']);
 assert.deepEqual(selectResults(rows,{type:'unknown'}).map(r=>r.id),['unknown']);
 assert.deepEqual(selectResults(rows,{type:'video',source:'b'}).map(r=>r.id),['mixed']);
 assert.deepEqual(selectResults(rows,{type:'anything'}),[]);
});
