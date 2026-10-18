import test from 'node:test';
import assert from 'node:assert/strict';
import {taskProgress,formatDuration} from '../internal/web/assets/task-progress.mjs';

test('live buffers update progress before committed files and produce speed and ETA',()=>{
 const task={status:'downloading',files:[{size:1000,downloaded:0}],progress:{downloaded_bytes:400,total_bytes:1000,speed_bps:200,eta_seconds:3,elapsed_seconds:2,phase:'downloading',current_file:'episode.mkv',connections:2}};
 const p=taskProgress(task);assert.equal(p.percent,40);assert.equal(p.speed,200);assert.equal(p.eta,3);assert.equal(p.currentFile,'episode.mkv');
 for(const status of ['paused','completed','attention','verifying']){const p=taskProgress({...task,status});assert.equal(p.speed,0);assert.equal(p.eta,null);}
 assert.equal(taskProgress({...task,progress:{...task.progress,phase:'verifying'}}).speed,0);
});
test('unknown speeds have no ETA and durations are readable',()=>{
 assert.equal(taskProgress({status:'downloading',files:[]}).eta,null);
 assert.equal(formatDuration(null),'计算中');assert.equal(formatDuration(65),'1分 5秒');assert.equal(formatDuration(3700),'1小时 1分');assert.equal(formatDuration(90000),'1天 1小时');
 assert.equal(taskProgress({files:[{size:1000,downloaded:2000}]}).percent,100);
});
