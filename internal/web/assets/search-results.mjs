// Pure search presentation helpers, shared by the browser and Node tests.
const escapeHTML = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));

export function resultKey(result) {
 try {
  const url = new URL(result.url);
  if(!['http:','https:'].includes(url.protocol)||url.hostname!=='pan.quark.cn'||url.username||url.password)return '';
  const id=url.pathname.match(/^\/s\/([A-Za-z0-9]+)(?:\/|$)/)?.[1];
  if(!id)return '';
  let parent='0';
  for(const part of decodeURIComponent(url.hash.slice(1)).split('/')){const folder=part.match(/^([A-Za-z0-9]{32})(?:-|$)/);if(folder)parent=folder[1];}
  return `quark:${id}:${parent}`;
 } catch { return ''; }
}

export function mergeSearchResults(existing,incoming) {
 const merged=new Map();
 for(const row of [...existing,...incoming]) {
  const id=resultKey(row);if(!id)continue;
  const old=merged.get(id),next={...row,id,provider:'quark',origins:[...(row.origins||[])],local:!!row.local,cached:!!row.cached};
  if(old) {
   next.origins=[...new Map([...old.origins,...next.origins].map(o=>[o.id,o])).values()];
   if(!new URL(next.url).searchParams.get('pwd')&&new URL(old.url).searchParams.get('pwd'))next.url=old.url;
   for(const field of ['discovered_at','last_seen_at','published_at','checked_at'])if(!next[field])next[field]=old[field];
   if(old.discovered_at&&old.discovered_at<next.discovered_at)next.discovered_at=old.discovered_at;
   if(old.last_seen_at&&old.last_seen_at>next.last_seen_at)next.last_seen_at=old.last_seen_at;
   if(old.checked_at&&old.checked_at>=next.checked_at){for(const field of ['checked_at','availability','check_message','file_count','content'])next[field]=old[field];}
   if(new URL(next.url).searchParams.get('pwd')!==new URL(old.url).searchParams.get('pwd')&&next.availability!=='invalid')Object.assign(next,{checked_at:'',availability:'unknown',check_message:'',file_count:0,content:null});
  }
  merged.set(id,next);
 }
 return [...merged.values()];
}

function relevance(title,query) {
 const text=String(title||'').toLowerCase(),q=String(query||'').trim().toLowerCase();
 if(!q)return 0;
 return (text===q?1000:0)+(text.startsWith(q)?200:0)+(text.includes(q)?100:0)+q.split(/\s+/).filter(t=>text.includes(t)).length*10;
}

const contentLabels={video:'视频',audio:'音频',image:'图片',document:'文档',archive:'压缩包 / 镜像',application:'应用程序',subtitle:'字幕',other:'其他文件'};
export const contentFilters=[['','全部类型'],...Object.entries(contentLabels),['mixed','混合文件'],['folder','含文件夹'],['unknown','类型未知']];
export function matchesContentType(result,type='') {
 if(!type)return true;
 const info=result.content,kinds=Object.keys(contentLabels).filter(k=>info?.types?.[k]>0);
 if(type==='unknown')return kinds.length===0;
 if(!info)return false;
 if(type==='mixed')return kinds.length>1;
 if(type==='folder')return info.folder_count>0;
 return Object.hasOwn(contentLabels,type)&&info.types?.[type]>0;
}
export function contentBadge(result) {
 const info=result.content;if(!info)return '夸克';
 const kinds=Object.keys(contentLabels).filter(k=>info.types?.[k]>0);
 return kinds.length>1?'混合文件':kinds.length===1?(kinds[0]==='archive'?'压缩包':contentLabels[kinds[0]]):info.folder_count?'文件夹':'文件';
}
function contentBytes(value){
 let n=Number(value);if(!Number.isFinite(n)||n<0)return '大小未知';
 if(n<1024)return `${n} B`;
 const units=['KiB','MiB','GiB','TiB'];let i=-1;do{n/=1024;i++;}while(n>=1024&&i<3);
 return `${n.toFixed(1)} ${units[i]}`;
}
export function contentDescription(result){
 const info=result.content;if(!info)return result.availability==='checking'?'正在读取类型和大小…':'类型、大小暂未读取';
 const kinds=Object.keys(contentLabels).filter(k=>info.types?.[k]>0).map(k=>`${contentLabels[k]} ${info.types[k]} 个`);
 if(!kinds.length)kinds.push(info.folder_count?'文件夹':'文件类型未知');
 const ext=(info.extensions||[]).slice(0,5).map(e=>String(e).toUpperCase()).join(' / ');if(ext)kinds.push(ext);
 if(info.complete&&info.unknown_size===0)kinds.push(`总大小 ${contentBytes(info.total_bytes)}`);
 else if(info.file_count>0)kinds.push(`已统计 ${contentBytes(info.total_bytes)}（大小未统计完整）`);
 else kinds.push('大小暂未统计');
 if(!info.complete)kinds.push('部分内容');
 return kinds.join(' · ');
}

// Metadata can suggest a preference, but cannot guarantee the file's quality.
export function qualityScore(result,query,now=Date.now()) {
 const title=String(result.title||'');
 const verified=result.availability==='accessible'&&now-(Date.parse(result.checked_at)||0)<30*60000;
 let score=(verified?10000:0)+Math.min(relevance(title,query),100)*4;
 if(title.trim().toLowerCase()===String(query).trim().toLowerCase())score+=25;
 if(result.availability==='password_required')score-=500;
 if(/(?:2160p|\b4k\b|\buhd\b|蓝光|remux)/i.test(title))score+=120;
 else if(/(?:1080p|高清)/i.test(title))score+=60;
 if(/(?:杜比|\bhdr\b|高码率|无损)/i.test(title))score+=30;
 if(/(?:抢先|枪版|\bts\b|\bcam\b)/i.test(title))score-=150;
 score+=Math.min(new Set((result.origins||[]).map(o=>o.id)).size,4)*15;
 return score;
}

export function selectResults(results,{query='',source='',type='',days=0,sort='quality',now=Date.now()}={}) {
 return results.filter(r=>!['invalid','checking'].includes(r.availability)&&matchesContentType(r,type)&&(!source||(r.origins||[]).some(o=>o.id===source))&&(!days||Date.parse(r.discovered_at)>=now-days*86400000)).slice().sort((a,b)=>{
  if(sort==='quality'){const delta=qualityScore(b,query,now)-qualityScore(a,query,now);if(delta)return delta;}
  if(sort==='relevance'){const delta=relevance(b.title,query)-relevance(a.title,query);if(delta)return delta;}
  const delta=(Date.parse(b.discovered_at)||0)-(Date.parse(a.discovered_at)||0);
  return delta||String(a.id).localeCompare(String(b.id));
 });
}

export function highlight(title,query) {
 const tokens=[...new Set(String(query||'').trim().split(/\s+/).filter(Boolean))].sort((a,b)=>b.length-a.length);
 if(!tokens.length)return escapeHTML(title);
 const pattern=new RegExp(tokens.map(t=>t.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')).join('|'),'gi');
 let output='',offset=0;const text=String(title||'');
 for(const match of text.matchAll(pattern)){output+=escapeHTML(text.slice(offset,match.index))+`<mark>${escapeHTML(match[0])}</mark>`;offset=match.index+match[0].length;}
 return output+escapeHTML(text.slice(offset));
}
