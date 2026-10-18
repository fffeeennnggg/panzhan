const finite=value=>Number.isFinite(Number(value))?Math.max(0,Number(value)):0;
export function taskProgress(task){
 const files=task.files||[];
 let total=files.reduce((sum,f)=>sum+finite(f.size),0),done=files.reduce((sum,f)=>sum+Math.min(finite(f.size),finite(f.done?f.size:f.downloaded)),0);
 const live=task.progress,active=['downloading','verifying'].includes(task.status);
 if(active&&live){total=finite(live.total_bytes);done=Math.min(total,finite(live.downloaded_bytes));}
 const phase=live?.phase||task.status;
 const speed=task.status==='downloading'&&phase==='downloading'?finite(live?.speed_bps):0;
 const eta=speed>0&&live?.eta_seconds!=null?finite(live.eta_seconds):null;
 return {total,done,percent:total?Math.min(100,Math.floor(done/total*100)):0,speed,eta,phase,elapsed:live?finite(live.elapsed_seconds):null,currentFile:live?.current_file||'',connections:live?finite(live.connections):0,fileDone:finite(live?.file_downloaded_bytes),fileTotal:finite(live?.file_total_bytes)};
}
export function formatDuration(value){
 if(value==null||!Number.isFinite(Number(value)))return '计算中';
 const seconds=Math.max(0,Math.ceil(Number(value))),days=Math.floor(seconds/86400),hours=Math.floor(seconds%86400/3600),minutes=Math.floor(seconds%3600/60);
 if(days)return `${days}天 ${hours}小时`;
 if(hours)return `${hours}小时 ${minutes}分`;
 if(minutes)return `${minutes}分 ${seconds%60}秒`;
 return `${seconds}秒`;
}
