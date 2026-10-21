import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFile} from 'node:fs/promises';
import {taskProgress,formatDuration} from '../internal/web/assets/task-progress.mjs';

const source=await readFile(new URL('../internal/web/assets/app.js',import.meta.url),'utf8');
const renderers=source.slice(source.indexOf('function taskTelemetry'),source.indexOf('function accountPage'));
function harness(status='retrying'){
 const context=vm.createContext({Date,Number,taskProgress,formatDuration,bytes:String,esc:value=>String(value??'').replaceAll('<','&lt;'),fullPath:String,head:()=>'',empty:()=>'',taskStatsHTML:()=>'',btn:(title,attrs)=>title+' '+attrs,labels:{retrying:'自动重试中',attention:'需要处理'},state:{filter:'进行中',tasks:[{id:'test',title:'Retry fixture',status,enumerated:true,next_retry_at:'2026-10-19T10:00:00Z',retry_count:3,error:'<unsafe>',files:[]}]}});
 vm.runInContext(renderers,context);
 return context;
}
test('automatic retry remains in active list with deadline and stop controls',()=>{
 const html=harness().tasksPage();
 assert.ok(html.includes('Retry fixture'));
 assert.ok(html.includes('自动重试中'));
 assert.ok(html.includes('第 3 次自动重试'));
 assert.ok(html.includes('下次重试'));
 assert.ok(html.includes('data-job-action="pause"'));
 assert.ok(html.includes('data-job-action="cancel"'));
 assert.ok(!html.includes('data-job-action="retry"'));
 assert.ok(html.includes('&lt;unsafe>'));
});
