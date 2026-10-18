import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFile} from 'node:fs/promises';
import {mergeSearchResults,selectResults} from '../internal/web/assets/search-results.mjs';

const source=await readFile(new URL('../internal/web/assets/app.js',import.meta.url),'utf8');
const scheduler=source.slice(source.indexOf('function finishSearchIfIdle'),source.indexOf('function renderAuth'));
function harness({manualChecks=false}={}){
 const requests=[];
 const context=vm.createContext({AbortController,Map,Set,Date,setTimeout,clearTimeout,mergeSearchResults,selectResults,renderSearch(){},localController:null,checkControllers:new Set(),checkQueue:[],checkQueued:new Map(),searchControllers:new Map(),state:{query:'',settings:{},sourceList:[{id:'a',name:'A',enabled:true}],results:[],report:[],searchRun:0},api(path,body,signal){if(path==='search/check'&&!manualChecks)return Promise.resolve({checks:body.urls.map(url=>({url,availability:'accessible',checked_at:new Date().toISOString()}))});return new Promise((resolve,reject)=>requests.push({path,body,signal,resolve,reject}));}});
 vm.runInContext(scheduler,context);
 return {context,requests};
}
const flush=()=>new Promise(resolve=>setImmediate(resolve));

test('local results arrive before external requests and survive their failure',async()=>{
 const {context:c,requests:r}=harness();c.beginSearch('Blender');
 assert.equal(r[0].path,'search/local');assert.equal(r.length,1);
 r[0].resolve({results:[{title:'Blender',url:'https://pan.quark.cn/s/abc',local:true}],sources:[]});await flush();
 assert.equal(c.state.results.length,1);assert.equal(r[1].path,'search');
 r[1].reject(new Error('来源暂不可用'));await flush();
 assert.equal(c.state.results.length,1);assert.equal(c.state.searching,false);assert.equal(c.state.report.length,1);
});

test('local catalog warning is visible even when all external sources are disabled',async()=>{
 const {context:c,requests:r}=harness();c.state.sourceList=[];c.beginSearch('Blender');
 r[0].resolve({results:[],warning:'本地资源库无法读取'});await flush();
 assert.equal(c.state.searchError,'本地资源库无法读取');assert.equal(r.length,1);assert.equal(c.state.searching,false);
});

test('new query ignores late local and remote responses from an old run',async()=>{
 const {context:c,requests:r}=harness();c.beginSearch('old');const old=r[0];c.beginSearch('new');
 old.resolve({results:[{title:'old',url:'https://pan.quark.cn/s/old'}]});await flush();assert.equal(c.state.results.length,0);
 r[1].resolve({results:[]});await flush();const remote=r[2];c.beginSearch('latest');
 remote.resolve({results:[{title:'new',url:'https://pan.quark.cn/s/new'}],sources:[]});await flush();assert.equal(c.state.results.length,0);assert.equal(c.state.query,'latest');
 c.cancelSearch();r[3].resolve({results:[]});await flush();assert.equal(c.state.searching,false);
});

test('pause during local lookup prevents remote scheduling until resumed',async()=>{
 const {context:c,requests:r}=harness();c.beginSearch('Blender');c.pauseSearch();
 r[0].resolve({results:[]});await flush();assert.equal(r.length,1);
 c.resumeSearch();assert.equal(r.length,2);assert.equal(r[1].path,'search');
 c.cancelSearch();r[1].reject(Object.assign(new Error('abort'),{name:'AbortError'}));await flush();assert.equal(c.state.searchActive,0);
});

test('unverified links stay hidden, invalid links disappear and network uncertainty stays visible',async()=>{
 const {context:c,requests:r}=harness({manualChecks:true});c.state.sourceList=[];c.beginSearch('资源');
 r[0].resolve({results:['good','bad','uncertain'].map(id=>({title:id,url:`https://pan.quark.cn/s/${id}`}))});await flush();
 assert.equal(selectResults(c.state.results).length,0);assert.equal(r[1].path,'search/check');assert.equal(c.state.searching,true);
 r[1].resolve({checks:r[1].body.urls.map((url,i)=>({url,availability:['accessible','invalid','unknown'][i],checked_at:new Date().toISOString()}))});await flush();
 assert.deepEqual(selectResults(c.state.results).map(r=>r.title),['good','uncertain']);assert.equal(c.state.searching,false);
});

test('a new query cancels validation and ignores stale responses',async()=>{
 const {context:c,requests:r}=harness({manualChecks:true});c.state.sourceList=[];c.beginSearch('old');
 r[0].resolve({results:[{title:'old',url:'https://pan.quark.cn/s/old'}]});await flush();const check=r[1];
 c.beginSearch('new');assert.equal(check.signal.aborted,true);
 check.resolve({checks:[{url:'https://pan.quark.cn/s/old',availability:'invalid'}]});await flush();
 assert.equal(c.state.results.length,0);r[2].resolve({results:[]});await flush();assert.equal(c.state.searching,false);
});
