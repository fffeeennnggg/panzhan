import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFile} from 'node:fs/promises';
import {mergeSearchResults} from '../internal/web/assets/search-results.mjs';

const source=await readFile(new URL('../internal/web/assets/app.js',import.meta.url),'utf8');
const scheduler=source.slice(source.indexOf('function finishSearchIfIdle'),source.indexOf('function renderAuth'));
function harness(){
 const requests=[];
 const context=vm.createContext({AbortController,Map,Set,Date,setTimeout,clearTimeout,mergeSearchResults,renderSearch(){},localController:null,searchControllers:new Map(),state:{query:'',settings:{},sourceList:[{id:'a',name:'A',enabled:true}],results:[],report:[],searchRun:0},api(path,body,signal){return new Promise((resolve,reject)=>requests.push({path,body,signal,resolve,reject}));}});
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
