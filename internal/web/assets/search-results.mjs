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
   if(old.checked_at&&old.checked_at>=next.checked_at){next.checked_at=old.checked_at;next.availability=old.availability;}
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

export function selectResults(results,{query='',source='',days=0,sort='relevance',now=Date.now()}={}) {
 return results.filter(r=>(!source||(r.origins||[]).some(o=>o.id===source))&&(!days||Date.parse(r.discovered_at)>=now-days*86400000)).slice().sort((a,b)=>{
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
