import {agentAccessHTML} from './agent-access.mjs';
import {mergeSearchResults, selectResults, highlight, resultKey, contentBadge, contentDescription, contentFilters} from './search-results.mjs';
import {taskProgress, formatDuration} from './task-progress.mjs';
import {icon} from './icons.mjs';
import {patchHTML, stableResultOrder} from './live-dom.mjs';
const $ = s => document.querySelector(s);
const app = $('#app'), dialog = $('#dialog');
const esc = v => String(v ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const bytes = n => { n = Number(n || 0); if(n<1024) return `${Math.round(n)} B`; const units=['KiB','MiB','GiB','TiB']; let i=-1; do{n/=1024;i++;}while(n>=1024&&i<3); return `${n.toFixed(1)} ${units[i]}`; };
const labels={queued:'排队中',transferring:'转存中',downloading:'下载中',verifying:'校验中',completed:'已完成',attention:'需要处理',paused:'已暂停',cancelled:'已取消'};
const state={searchType:'',searchSource:'',searchDays:0,searchSort:'quality',localPending:false,localCount:0,firstResultMS:null,searchStarted:0,searchOrder:[],searchOrderKey:'',page:['search','tasks','account','sources','settings'].includes(location.hash.slice(1))?location.hash.slice(1):'search',status:null,settings:null,root:'',downloadRoot:'',account:false,tasks:[],results:[],report:[],searched:false,query:'',filter:'全部',searching:false,searchPaused:false,searchRun:0,searchRemaining:[],searchQueue:[],searchActive:0,searchActivePriority:0,searchActiveChannels:0,searchActiveProviders:0,searchError:'',settingsDir:'',sourceList:[],sourceDraft:null,sourceSaving:false,sourceError:'',modal:null,browser:null};
state.agents=[];state.newAgent=null;
let toastTimer, searchControllers=new Map(), polling=false, directoryController, localController;
let checkControllers=new Set(), checkQueue=[], checkQueued=new Map();
const buttonIcons={'导入分享链接':'link','查看资源':'arrow','退出':'logout','暂停':'pause','继续':'play','×':'close'};
const btn=(text,attrs='',kind='')=>`<button type="button" class="${kind}" ${attrs}>${text==='查看资源'?'':buttonIcons[text]?icon(buttonIcons[text]):''}${text==='×'?'':text}${text==='查看资源'?icon('arrow'):''}</button>`;
const pageTitles={search:'资源搜索',tasks:'下载任务',account:'网盘账号',sources:'搜索来源',settings:'设置'};
function brandHTML(){return '<a href="/" class="brand"><img src="/mark.svg" alt="" width="36" height="36"><span class="brand-copy"><strong translate="no">盘栈</strong><small>资源工作台</small></span></a>';}
function resultKind(result){const types=result.content?.types||{};return ['video','audio','document','archive','image'].find(type=>types[type]>0)||'file';}
function taskStatsHTML(){const active=state.tasks.filter(t=>['queued','transferring','downloading','verifying'].includes(t.status)).length,attention=state.tasks.filter(t=>t.status==='attention').length,completed=state.tasks.filter(t=>t.status==='completed').length,speed=state.tasks.reduce((n,t)=>n+(t.status==='downloading'?Number(t.progress?.speed_bps)||0:0),0);return '<div class="task-stats">'+[['进行中',active],['需要处理',attention],['已完成',completed],['当前速度',bytes(speed)+'/s']].map(([label,value])=>'<div><p class="stat-label">'+label+'</p><strong>'+value+'</strong></div>').join('')+'</div>';}
const head=(title,desc,action='')=>`<div class="page-head"><div><h1>${title}</h1><p class="sub">${desc}</p></div>${action}</div>`;
const empty=(title,desc,action='')=>`<div class="empty"><div class="empty-symbol" aria-hidden="true">${icon('folder')}</div><h2>${title}</h2><p>${desc}</p>${action}</div>`;
function toast(text){$('#toast').textContent=text;$('#toast').classList.add('show');clearTimeout(toastTimer);toastTimer=setTimeout(()=>$('#toast').classList.remove('show'),5000);}
async function api(path,body,signal){const headers=body===undefined?{}:{'Content-Type':'application/json'};if(body!==undefined&&state.status?.request_token)headers['X-Panzhan-Request-Token']=state.status.request_token;const res=await fetch('/api/'+path,{method:body===undefined?'GET':'POST',headers,body:body===undefined?undefined:JSON.stringify(body),signal,credentials:'same-origin'});let data;try{data=await res.json();}catch{throw new Error('服务响应无法解析，请检查程序是否运行。');}if(!res.ok){if(res.status===401&&state.status?.authenticated){cancelSearch();state.status.authenticated=false;renderAuth();}throw new Error(data.error||`请求失败 (${res.status})`);}return data;}
async function boot(){try{state.status=await api('status');if(!state.status.authenticated){renderAuth();return;}await load();render();}catch(e){app.innerHTML=empty('暂时无法连接盘栈',esc(e.message),btn('重新连接','data-action="reload"'));}}
async function load(){const [config,account,tasks,sources,grants]=await Promise.all([api('settings'),api('account'),api('tasks'),api('sources'),api('agents')]);state.agents=grants.agents;state.settings=config.settings;state.root=config.root;state.downloadRoot=config.settings.download_root||config.root;state.settingsDir=config.settings.default_dir;state.account=account.configured;state.tasks=tasks;state.sourceList=sources;}
function renderSearch(){
 if(state.page!=='search'||!state.status?.authenticated)return;
 const feedback=$('#search-feedback'),controls=$('#search-controls');
 if(!feedback||!controls){renderPage();return;}
 patchHTML(feedback,searchFeedback());
 patchHTML(controls,searchControls());
}
function finishSearchIfIdle(){
 if(state.searching&&!state.localPending&&!state.searchPaused&&state.searchActive===0&&state.searchQueue.length===0&&checkQueue.length===0&&checkControllers.size===0){state.searching=false;state.searched=true;}
 renderSearch();
}
function searchJobKey(job){return job.id+(job.channel?':'+job.channel:'');}
function buildSearchJobs(){
 const sources=state.sourceList.filter(s=>s.enabled),telegram=sources.find(s=>s.id==='telegram');
 const plugins=sources.filter(s=>s.id!=='telegram').map(s=>({id:s.id}));
 const priority=telegram?(telegram.priority_channels||[]).filter(c=>(telegram.channels||[]).includes(c)).map(channel=>({id:'telegram',channel,priority:true})):[];
 const channels=telegram?(telegram.channels||[]).filter(channel=>!priority.some(job=>job.channel===channel)).map(channel=>({id:'telegram',channel,priority:false})):[];
 return [...plugins.filter(j=>['pansearch','qupansou'].includes(j.id)),...priority,...plugins.filter(j=>!['pansearch','qupansou'].includes(j.id)),...channels];
}
function nextSearchJobIndex(){return state.searchQueue.findIndex(job=>job.channel?job.priority?state.searchActivePriority<(state.settings.search_priority_concurrency||3):state.searchActiveChannels<(state.settings.search_channel_concurrency||8):state.searchActiveProviders<5);}
function acceptResults(results){
 state.results=mergeSearchResults(state.results,results||[]);
 for(const row of state.results){
  const ttl=row.availability==='accessible'?30*60000:row.availability==='invalid'?10*60000:60000;
  if(Date.now()-(Date.parse(row.checked_at)||0)<ttl&&(row.availability!=='accessible'||row.content))continue;
  if(checkQueued.get(row.id)===row.url)continue;
  checkQueued.set(row.id,row.url);row.availability='checking';checkQueue.push({id:row.id,url:row.url});
 }
 scheduleChecks();
 if(selectResults(state.results).length&&state.firstResultMS===null)state.firstResultMS=Date.now()-state.searchStarted;
}
function resetChecks(){for(const c of checkControllers)c.abort();checkControllers.clear();checkQueue=[];checkQueued.clear();}
function scheduleChecks(){
 if(state.searchPaused)return;
 while(checkQueue.length&&checkControllers.size<2){
  const jobs=checkQueue.splice(0,3),run=state.searchRun,controller=new AbortController();checkControllers.add(controller);
  api('search/check',{urls:jobs.map(j=>j.url)},controller.signal).then(report=>{
   if(run!==state.searchRun||controller.signal.aborted)return;
   for(const job of jobs){
    const row=state.results.find(r=>r.id===job.id&&r.url===job.url);if(!row)continue;
    const check=report.checks?.find(c=>c.url===job.url);
    Object.assign(row,check||{availability:'unknown',check_message:'暂时无法验证'});
   }
   if(report.warning)state.searchError=report.warning;
  }).catch(error=>{
   if(run!==state.searchRun||controller.signal.aborted)return;
   for(const job of jobs){const row=state.results.find(r=>r.id===job.id&&r.url===job.url);if(row)Object.assign(row,{availability:'unknown',check_message:'验证暂不可用，未判定为失效'});}
  }).finally(()=>{
   checkControllers.delete(controller);if(run!==state.searchRun)return;
   if(selectResults(state.results).length&&state.firstResultMS===null)state.firstResultMS=Date.now()-state.searchStarted;
   scheduleChecks();finishSearchIfIdle();
  });
 }
}
function acceptStatus(status,id){
 if(!status)return;
 const old=state.report.find(s=>s.id===id);
 if(old&&id==='telegram'){
  old.count+=(status.count||0);old.channel_count+=(status.channel_count||0);old.channel_errors+=(status.channel_errors||0);
  old.error=old.channel_errors?`${old.channel_errors} 个频道请求失败`:'';
  old.checked=status.checked;old.cached=old.cached&&!!status.cached;
  if(status.retry_at)old.retry_at=status.retry_at;
 }else{state.report=state.report.filter(s=>s.id!==id);state.report.push({...status,channel_errors:status.channel_errors||0,channel_count:status.channel_count||0});}
}
function scheduleSearch(){
 if(state.searchPaused||state.localPending||!state.searching)return;
 let index;
 while((index=nextSearchJobIndex())>=0){
  const [job]=state.searchQueue.splice(index,1),id=job.id,key=searchJobKey(job),run=state.searchRun;
  state.searchActive++;
  if(job.channel){if(job.priority)state.searchActivePriority++;else state.searchActiveChannels++;}else state.searchActiveProviders++;
  const controller=new AbortController();searchControllers.set(controller,job);
  api('search',{query:state.query,source_id:id,channel:job.channel||''},controller.signal).then(report=>{
   if(run!==state.searchRun||controller.signal.aborted)return;
   acceptStatus(report.sources?.[0],id);acceptResults(report.results);
   if(report.warning)state.searchError=report.warning;
   state.searchRemaining=state.searchRemaining.filter(item=>searchJobKey(item)!==key);
   renderSearch();
  }).catch(error=>{
   if(run!==state.searchRun||controller.signal.aborted||error.name==='AbortError')return;
   acceptStatus({id,name:state.sourceList.find(s=>s.id===id)?.name||id,error:error.message,count:0,channel_count:job.channel?1:0,channel_errors:job.channel?1:0},id);
   state.searchRemaining=state.searchRemaining.filter(item=>searchJobKey(item)!==key);renderSearch();
  }).finally(()=>{
   searchControllers.delete(controller);if(run!==state.searchRun)return;
   state.searchActive--;if(job.channel){if(job.priority)state.searchActivePriority--;else state.searchActiveChannels--;}else state.searchActiveProviders--;
   if(controller.signal.aborted&&!state.searchQueue.some(item=>searchJobKey(item)===key)&&state.searchRemaining.some(item=>searchJobKey(item)===key))state.searchQueue.push(job);
   if(state.searchPaused)renderSearch();else{scheduleSearch();finishSearchIfIdle();}
  });
 }
 finishSearchIfIdle();
}
async function beginSearch(query){
 query=query.trim();if(!query)return;
 resetChecks();
 localController?.abort();for(const controller of searchControllers.keys())controller.abort();searchControllers.clear();
 const run=++state.searchRun;
 Object.assign(state,{query,results:[],searchOrder:[],searchOrderKey:'',report:[],searchError:'',searched:false,searchPaused:false,searching:true,localPending:true,localCount:0,firstResultMS:null,searchStarted:Date.now(),searchActive:0,searchActivePriority:0,searchActiveChannels:0,searchActiveProviders:0,searchSource:'',searchType:'',searchDays:0});
 state.searchRemaining=buildSearchJobs();state.searchQueue=[...state.searchRemaining];renderSearch();
 const controller=new AbortController();localController=controller;
 const timer=setTimeout(()=>controller.abort(),3000);
 try{
  const report=await api('search/local',{query},controller.signal);
  if(run!==state.searchRun||controller.signal.aborted)return;
  acceptResults(report.results);state.localCount=state.results.length;
  if(report.warning)state.searchError=report.warning;
  renderSearch();
 }catch(error){if(run===state.searchRun)state.searchError='本地资源暂未读取，继续从外部来源搜索。';}
 finally{clearTimeout(timer);if(run===state.searchRun){state.localPending=false;localController=null;scheduleSearch();}}
}
function pauseSearch(){
 state.searchPaused=true;
 for(const [controller,job] of searchControllers){const key=searchJobKey(job);if(!state.searchQueue.some(item=>searchJobKey(item)===key)&&state.searchRemaining.some(item=>searchJobKey(item)===key))state.searchQueue.push(job);controller.abort();}
 renderSearch();
}
function resumeSearch(){state.searchPaused=false;scheduleChecks();if(state.searchActive===0)scheduleSearch();}
function cancelSearch(){
 resetChecks();
 state.searchRun++;state.searchPaused=false;state.localPending=false;state.searchQueue=[];state.searchRemaining=[];
 localController?.abort();localController=null;for(const controller of searchControllers.keys())controller.abort();searchControllers.clear();
 state.searchActive=0;state.searchActivePriority=0;state.searchActiveChannels=0;state.searchActiveProviders=0;state.searching=false;state.searched=true;renderSearch();
}
function renderAuth(){
 state.modal=null;state.browser=null;if(dialog.open)dialog.close();const setup=!state.status.configured;
 app.innerHTML=`<div class="auth-shell"><main class="auth app-auth"><a class="auth-logo" href="/" aria-label="盘栈"><img src="/mark.svg" alt="" width="36" height="36"></a><h1>${setup?'初始化盘栈':'解锁盘栈'}</h1><p>${setup?'设置至少 8 个字符的管理密码，首次安装建议在飞牛安装向导中完成。':'输入管理密码，进入资源工作台。'}</p><form id="auth-form">${setup?'<label class="field">安装令牌<input name="token" required autocomplete="off" placeholder="粘贴 setup-token.txt 中的令牌…" spellcheck="false"><small>通用安装方式需要一次性令牌；飞牛安装包可在安装向导中直接设置密码。</small></label>':''}<label class="field">管理密码<input type="password" name="password" required minlength="8" maxlength="128" autocomplete="${setup?'new-password':'current-password'}" placeholder="${setup?'至少 8 个字符…':'输入管理密码…'}"></label><button class="primary" type="submit">${setup?'创建工作台':'解锁工作台'}</button><div class="login-error" role="alert"></div></form><p class="auth-note">${icon('shield')}保护你的网盘凭据与下载操作。</p></main></div>`;
}
function render(){
 if(!state.status?.authenticated)return;
 const active=state.tasks.filter(t=>!['completed','cancelled'].includes(t.status)).length;
 app.innerHTML=`<a class="skip-link" href="#main">跳到主要内容</a><div class="shell"><aside class="sidebar">${brandHTML()}<div class="workspace-mark"><span class="status-dot"></span>本地工作台</div><nav class="main-nav" aria-label="主导航">${Object.entries(pageTitles).map(([id,label])=>`<a href="#${id}" data-page="${id}" class="${state.page===id?'active':''}" ${state.page===id?'aria-current="page"':''}>${icon(id)}<span>${label}</span>${id==='tasks'?`<span class="count" ${active?'':'hidden'}>${active}</span>`:''}</a>`).join('')}</nav><div class="sidebar-bottom">当前设备<p>本机工作区</p>${btn(icon('account')+'管理账号','data-page="account"')}<span class="sidebar-version">盘栈 · v0.2.0</span></div></aside><header class="top"><div class="top-context"><span>工作台</span><span class="slash">/</span><strong>${pageTitles[state.page]||'资源搜索'}</strong></div><div class="top-actions">${btn('<span class="status-dot"></span>'+(state.account?'夸克已配置':'连接夸克'),'data-page="account"','account-pill')}${btn('退出','data-action="logout"','logout')}</div></header><main id="main" tabindex="-1" data-page="${state.page}"></main><footer class="footer"><span><strong translate="no">盘栈</strong> · 把资源留在自己手里</span><span>云端文件保留，本地同名文件不覆盖</span></footer></div>`;
 renderPage();
}
function renderPage(){
 const main=$('#main');if(!main)return;
 const pages={search:searchPage,tasks:tasksPage,account:accountPage,sources:sourcesPage,settings:settingsPage};
 if(state.page==='tasks')patchHTML(main,tasksPage());else main.innerHTML=pages[state.page]();
}
function searchControls(){
 return `${btn('导入分享链接','data-action="import"','ghost import-link')}${btn(state.searchPaused?'继续搜索':'暂停搜索',`data-action="${state.searchPaused?'resume-search':'pause-search'}" ${state.searching?'':'disabled'}`,'ghost import-link')}${btn('停止搜索',`data-action="cancel-search" ${state.searching?'':'disabled'}`,'ghost import-link')}`;
}
function searchPage(){
 const enabled=state.sourceList.filter(s=>s.enabled).length;
 return `<section class="search-intro"><div class="search-heading"><div><h1>找到资源，交给 NAS</h1><p class="search-description">搜索网盘资源，验证文件后保存到自己的设备。</p></div><div class="search-source-note"><span class="status-dot"></span>${enabled} 个来源已启用</div></div><form id="search-form" class="search-form"><label class="search-input">${icon('search')}<input name="query" type="search" autocomplete="off" spellcheck="false" aria-label="资源关键词或夸克分享链接" value="${esc(state.query)}" placeholder="搜索名称或分享链接…" required maxlength="600"></label><button type="submit" class="primary">搜索 ${icon('arrow')}</button></form><div id="search-controls" class="actions">${searchControls()}</div></section>${!state.account?'<p class="alert">可以先搜索资源。查看分享内容和下载前，请在“网盘账号”连接夸克。</p>':''}<div id="search-feedback">${searchFeedback()}</div>`;
}
function discoveryLabel(r){
 if(!r.discovered_at)return '发现时间未知';
 const date=new Date(r.discovered_at);if(!Number.isFinite(date.getTime()))return '发现时间未知';
 return '首次发现 '+date.toLocaleDateString('zh-CN');
}
function searchFeedback(){
 const ranked=selectResults(state.results,{query:state.query,source:state.searchSource,type:state.searchType,days:state.searchDays,sort:state.searchSort});
 const orderKey=JSON.stringify([state.query,state.searchSource,state.searchType,state.searchDays,state.searchSort]);
 if(orderKey!==state.searchOrderKey){state.searchOrder=[];state.searchOrderKey=orderKey;}
 const ordered=stableResultOrder(ranked,state.searchOrder);state.searchOrder=ordered.ids;
 const visible=ordered.rows;
 const errors=state.report.filter(s=>s.error),hasRun=state.searching||state.searched;
 const pending=state.results.filter(r=>r.availability==='checking').length,hidden=state.results.filter(r=>r.availability==='invalid').length,total=state.results.length-pending-hidden;
 const status=state.localPending?'正在读取本地资源…':state.searchPaused?'搜索已暂停':state.searching?(state.searchRemaining.length?`外部搜索中 · ${state.searchRemaining.length} 项待完成`:'分享验证中…'):state.searched?'搜索已结束':'从关键词或分享链接开始';
 const first=state.firstResultMS===null?'':` · 首条 ${(state.firstResultMS/1000).toFixed(2)} 秒`;
 const filters=`<div class="search-filters"><span class="badge">夸克网盘</span><label>文件类型<select name="file_type" data-search-option="type" aria-label="筛选文件类型">${contentFilters.map(([value,label])=>`<option value="${value}" ${state.searchType===value?'selected':''}>${label}</option>`).join('')}</select></label><label>来源<select name="source" data-search-option="source" aria-label="筛选来源"><option value="">全部来源</option>${state.sourceList.filter(s=>s.enabled).map(s=>`<option value="${esc(s.id)}" ${state.searchSource===s.id?'selected':''}>${esc(s.name)}</option>`).join('')}</select></label><label>发现时间<select name="days" data-search-option="days" aria-label="筛选发现时间">${[[0,'不限时间'],[3,'近 3 天'],[7,'近 7 天'],[30,'近 30 天']].map(([v,t])=>`<option value="${v}" ${state.searchDays===v?'selected':''}>${t}</option>`).join('')}</select></label><label>排序<select name="sort" data-search-option="sort" aria-label="结果排序"><option value="quality" ${state.searchSort==='quality'?'selected':''}>优质优先</option><option value="relevance" ${state.searchSort==='relevance'?'selected':''}>相关度优先</option><option value="newest" ${state.searchSort==='newest'?'selected':''}>最近发现优先</option></select></label></div>`;
 const list=visible.length?`<ul class="rows">${visible.map(r=>`<li class="result" data-dom-key="result:${esc(r.id)}"><span class="file-mark kind-${resultKind(r)}" aria-hidden="true">${icon(resultKind(r))}<span>${esc(contentBadge(r))}</span></span><div class="row-body"><h2 title="${esc(r.title)}">${highlight(r.title,state.query)}</h2><p class="result-content">${esc(contentDescription(r))}</p><div class="meta"><span class="badge">${r.local?'本地记录':r.cached?'近期缓存':'本次发现'}</span><span>${esc((r.origins||[]).map(o=>o.name).join(' · ')||r.source)}</span><span>${discoveryLabel(r)}</span></div><p class="result-check">${r.availability==='accessible'?`已验证可读取 · ${esc(new Date(r.checked_at).toLocaleString('zh-CN'))}${r.file_count?` · ${r.file_count} 个顶层项目`:''}`:esc(r.check_message||(r.availability==='password_required'?'需要正确的提取码':'暂未确认有效'))}${r.published_at?` · 来源发布 ${esc(new Date(r.published_at).toLocaleDateString('zh-CN'))}`:''}</p></div>${btn('查看资源',`data-result="${esc(r.id)}"`)}</li>`).join('')}</ul>`:pending?'<div class="loading" role="status">正在验证分享有效性，确认后显示资源…</div>':total?empty('没有符合筛选条件的结果','调整来源或发现时间，查看其他结果。'):state.searching?'<div class="loading" role="status">正在查找资源，找到后会立即显示…</div>':empty(state.searched?'暂未找到匹配资源':'把网盘资源，存进自己的空间',state.searched?(errors.length?'部分来源暂时不可用，可以换个关键词或导入分享链接。':'试试更短的关键词，或直接导入夸克分享。'):'搜索成功的结果会保存在本机，下次搜索可优先显示。',btn('导入分享链接','data-action="import"'));
 return `<div class="section-head" data-dom-key="search-status"><span role="status">${status}</span><span>${hasRun?`${visible.length} / ${total} 条${pending?` · 验证中 ${pending} 条`:""}${hidden?` · 已隐藏 ${hidden} 条失效分享`:""}${first}`:'支持搜索中更换关键词'}</span></div>${state.searchError?`<p class="alert" role="alert">${esc(state.searchError)}</p>`:''}${filters}<div class="result-order"><span>新结果追加到列表末尾，阅读位置保持不变。</span>${btn('更新排序',`data-action="refresh-order" ${ordered.changed?'':'disabled'}`,'ghost')}</div><div id="search-results">${list}</div>${state.report.length?`<details class="source-diagnostics"><summary>搜索来源状态 · ${state.report.length-errors.length} 个正常返回${errors.length?`，${errors.length} 个来源受影响`:''}</summary>${state.report.map(s=>`<p><strong>${esc(s.name)}</strong> · ${s.cached?'缓存命中':`${s.count||0} 条`}${s.duration_ms?` · ${(s.duration_ms/1000).toFixed(1)} 秒`:''}${s.error?` · ${esc(s.error)}`:''}${s.retry_at?` · ${esc(new Date(s.retry_at).toLocaleTimeString('zh-CN'))} 后重试`:''}</p>`).join('')}</details>`:''}<p class="tip">${icon('shield')}<span>确认失效的分享自动隐藏；验证超时或需要提取码会保留并标记。优质排序综合有效性、关键词匹配、标题画质信息与来源数量，不保证文件实际质量。</span></p>`;
}
function taskTelemetry(t){
 if(!['downloading','verifying','paused'].includes(t.status))return '';
 const p=taskProgress(t),verifying=t.status==='verifying'||p.phase==='verifying';
 const remaining=t.status==='paused'?'已暂停':verifying?'正在校验':p.speed>0?'约 '+formatDuration(p.eta):'计算中';
 return `<div class="task-telemetry" aria-label="下载实时信息"><span class="telemetry-speed">实时速度 <strong>${bytes(p.speed)}/s</strong></span><span class="telemetry-eta">预计剩余 <strong>${remaining}</strong></span>${p.elapsed!==null?`<span class="telemetry-elapsed">已用时 <strong>${formatDuration(p.elapsed)}</strong></span>`:''}${t.status==='downloading'?`<span>连接数 <strong>${p.connections}</strong></span>`:''}</div>${p.currentFile?`<p class="path">当前文件：${esc(p.currentFile)} · ${bytes(p.fileDone)} / ${bytes(p.fileTotal)}</p>`:''}`;
}
function tasksPage(){const filtered=state.tasks.filter(t=>state.filter==='全部'||state.filter==='进行中'&&['queued','transferring','downloading','verifying','paused'].includes(t.status)||state.filter==='需要处理'&&t.status==='attention'||state.filter==='已完成'&&t.status==='completed');return head('下载任务','从转存到本地校验，掌握每一步的实际状态。',btn('＋ 新建任务','data-action="import"'))+`<div class="tabs" aria-label="任务状态">${['全部','进行中','需要处理','已完成'].map(f=>btn(f,`data-filter="${f}" aria-pressed="${state.filter===f}"`,state.filter===f?'active':'')).join('')}</div><p class="sr-only" role="status">${state.tasks.filter(t=>['queued','transferring','downloading','verifying'].includes(t.status)).length} 项进行中，${state.tasks.filter(t=>t.status==='attention').length} 项需要处理，${state.tasks.filter(t=>t.status==='completed').length} 项已完成</p><div id="task-results">${taskStatsHTML()}${filtered.length?filtered.map(t=>{const p=taskProgress(t);return `<article class="task" data-dom-key="task:${esc(t.id)}"><div class="between"><div class="row-body"><h2 title="${esc(t.title)}">${esc(t.title)}</h2><p class="path">${esc(fullPath(t.directory))}</p></div><span class="badge ${t.status==='attention'?'warning':['paused','cancelled'].includes(t.status)?'neutral':''}">${labels[p.phase==='verifying'&&t.status==='downloading'?'verifying':t.status]||esc(t.status)}</span></div>${t.enumerated?`<div class="progress" role="progressbar" aria-label="已下载字节进度" aria-valuenow="${p.percent}" aria-valuemin="0" aria-valuemax="100"><span style="width:${p.percent}%"></span></div><div class="meta"><span>${bytes(p.done)} / ${bytes(p.total)}</span><span>${t.files.filter(f=>f.done).length} / ${t.files.length} 个文件已校验</span></div>`:`<p class="sub">${t.status==='queued'?'等待执行':t.status==='transferring'?'正在核对云端目录与转存任务':'文件清单将在转存完成后建立'}</p>`}${taskTelemetry(t)}${t.error?`<p class="sub">${esc(t.error)}</p>`:''}<div class="actions">${btn('查看详情',`data-task="${t.id}"`)}${['queued','transferring','downloading','verifying'].includes(t.status)?btn('暂停',`data-job-action="pause" data-id="${t.id}"`):t.status==='paused'?btn('继续',`data-job-action="resume" data-id="${t.id}"`):t.status==='attention'&&!t.uncertain?btn('重试',`data-job-action="retry" data-id="${t.id}"`):''}${!['completed','cancelled'].includes(t.status)?btn('取消',`data-job-action="cancel" data-id="${t.id}"`,'ghost'):''}</div></article>`;}).join(''):empty('这里暂时没有任务','选择资源、确认目录后，任务会出现在这里。')}</div>`;}
function accountPage(){return head('网盘账号','连接自己的夸克账号，管理转存与下载。')+`<div class="settings-layout account-layout"><section class="section"><div class="account-logo">${icon('account')}</div><div class="between"><div><h2>夸克网盘</h2><p class="sub">网页端 Cookie 连接</p></div><span class="badge ${state.account?'':'warning'}">${state.account?'已配置 · 可重新验证':'未连接'}</span></div><div class="summary"><p>云端转存目录 <span class="path">/盘栈转存/任务编号</span></p><p class="sub">每个任务使用独立目录。下载完成后保留云端文件。</p></div><div class="actions">${btn('验证连接','data-action="verify"','primary')}${state.account?btn('断开账号','data-action="disconnect"','ghost danger'):''}</div><div id="verify-result" aria-live="polite"></div></section><section class="section"><h2>${state.account?'更新连接凭据':'连接夸克'}</h2><p class="sub">在自己登录的夸克网页中获取 Cookie。保存后页面不会回显。</p><form id="account-form"><label class="field">夸克 Cookie<textarea name="cookie" placeholder="粘贴网页端请求中的 Cookie…" required autocomplete="off" spellcheck="false"></textarea><small>仅由 NAS 服务加密保存；请勿在截图或公开日志中分享。</small></label><button class="primary" type="submit">保存连接凭据</button><p class="login-error" role="alert" tabindex="-1"></p></form></section></div>`;}
function sourceSelection(){return state.sourceDraft??state.sourceList.filter(s=>s.enabled).map(s=>s.id);}
function sourcesDirty(){const selected=new Set(sourceSelection());return state.sourceList.some(s=>selected.has(s.id)!==s.enabled);}
function renderSources(){if(state.page==='sources'&&state.status?.authenticated&&$('#main'))patchHTML($('#main'),sourcesPage());}
function selectSourceGroup(group,enabled){
 if(state.sourceSaving)return;
 const selected=new Set(sourceSelection());
 for(const s of state.sourceList){if(group==='all'||(group==='plugins')===s.id.startsWith('panplugin:')){if(enabled)selected.add(s.id);else selected.delete(s.id);}}
 state.sourceDraft=[...selected];state.sourceError='';renderSources();
}
async function saveSources(){
 if(state.sourceSaving||!sourcesDirty())return;
 const ids=[...sourceSelection()],settings={...state.settings,sources:ids};
 state.sourceSaving=true;state.sourceError='';renderSources();
 try{
  await api('settings',settings);state.settings=settings;
  for(const source of state.sourceList)source.enabled=ids.includes(source.id);
  state.sourceDraft=null;toast('来源设置已保存，将用于下一次搜索。');
 }catch(error){state.sourceError=error.message;}
 finally{state.sourceSaving=false;renderSources();}
}
function sourcesPage(){
 const selected=new Set(sourceSelection()),dirty=sourcesDirty(),busy=state.sourceSaving;
 const telegram=state.sourceList.find(s=>s.id==='telegram'),channelCount=telegram?.channels?.length||0;
 const groups=[['aggregate','聚合来源',state.sourceList.filter(s=>!s.id.startsWith('panplugin:'))],['plugins','站点插件',state.sourceList.filter(s=>s.id.startsWith('panplugin:'))]];
 const errors=state.report.filter(s=>s.error).length,disabled=busy?'disabled':'';
 const save=btn(busy?'正在保存…':'保存来源',`data-action="save-sources" ${busy||!dirty?'disabled':''}`,'primary');
 const choices=list=>list.map(source=>{
  const last=state.report.find(r=>r.id===source.id),channelInfo=source.id==='telegram'?`${source.channels?.length||0} 个频道 · ${source.priority_channels?.length||0} 个优先`:'',status=last?(last.error?'最近搜索未返回':`最近返回 ${last.count||0} 条`):source.needs_config?'需要账号或专属配置':'';
  return `<label class="source-option" data-dom-key="source:${esc(source.id)}"><input type="checkbox" name="source" data-source="${esc(source.id)}" ${selected.has(source.id)?'checked':''} ${disabled}><span><strong>${esc(source.name)}</strong>${channelInfo?`<small>${channelInfo}</small>`:''}${status?`<small>${esc(status)}</small>`:''}</span></label>`;
 }).join('');
 return head('搜索来源','批量选择，一次保存。具体来源按需展开。',save)+`<div class="source-manager" aria-busy="${busy}"><section class="source-plan"><div class="source-plan-top"><div><strong>${selected.size}</strong><span> / ${state.sourceList.length} 个来源已选择</span></div><span class="source-save-state" role="status">${busy?'正在保存…':dirty?'有未保存的修改':'已保存'}</span></div><div class="source-presets" aria-label="快捷选择">${btn('全部启用',`data-source-preset="all" ${disabled}`)}${btn('仅聚合来源',`data-source-preset="aggregate" ${disabled}`)}${btn('全部关闭',`data-source-preset="none" ${disabled}`,'ghost')}${btn('撤销修改',`data-source-preset="reset" ${busy||!dirty?'disabled':''}`,'ghost')}</div><p class="sub">${selected.size?'搜索会同时使用所选来源，无需逐个操作。':'关闭全部来源后，仅搜索本地已保存的资源。'}</p></section>${state.sourceError?`<p class="alert error" role="alert">${esc(state.sourceError)}，修改仍然保留，请重试保存。</p>`:''}<div class="source-choice-groups">${groups.filter(([, ,list])=>list.length).map(([id,title,list])=>{
 const count=list.filter(s=>selected.has(s.id)).length;
 return `<section class="source-choice-group" data-dom-key="source-group:${id}"><div class="source-choice-heading"><div><h2>${title}</h2><p>${count} / ${list.length} 个已选择${id==='aggregate'&&channelCount?` · TG ${channelCount} 个频道`:""}</p></div><div class="source-group-actions">${btn('启用本组',`data-source-group="${id}" data-enable="true" aria-label="启用全部${title}" ${disabled}`)}${btn('关闭本组',`data-source-group="${id}" data-enable="false" aria-label="关闭全部${title}" ${disabled}`,'ghost')}</div></div><details class="source-choices" data-dom-key="source-details:${id}"><summary>选择具体${title}</summary><div class="source-options">${choices(list)}</div></details></section>`;
 }).join('')}</div><p class="tip">${icon('shield')}<span>${state.report.length?`最近搜索有 ${errors} 个来源受影响；`:'发起搜索后可查看各来源的返回情况。'}部分来源需要额外配置，启用不代表一定能返回结果。</span></p></div>`;
}
function fullPath(rel,root){if(!root&&rel){root=state.tasks.find(t=>t.directory===rel)?.download_root;}root=root||state.downloadRoot;return root+(rel?'/'+rel:'');}
function dirField(dir,context){const root=context==='preview'?state.modal?.downloadRoot||state.downloadRoot:state.downloadRoot;return `<div class="field"><span>NAS 保存目录</span><div class="directory-value"><span class="path" title="${esc(fullPath(dir,root))}">${icon('folder')} ${esc(dir||root)}</span>${btn('选择目录',`data-browse="${context}"`)}</div><small>${context==='preview'?'将保存到已授权位置。':''}每个任务会创建独立子目录。</small></div><div data-browser-slot></div>`;}
function settingsBasePage(){const s=state.settings;return head('设置','下载位置、搜索并发与下载方式，由这台 NAS 管理。')+`<div class="settings-layout"><form id="settings-form"><section class="section"><h2>下载位置</h2><label class="field" for="download-root">NAS 共享目录路径<input id="download-root" name="download_root" value="${esc(state.downloadRoot)}" autocomplete="off" spellcheck="false" placeholder="例如 /vol1/video"><small>使用其他共享目录前，先在飞牛设置 → 应用权限中授权盘栈读写该文件夹。</small></label><div class="actions">${btn('浏览此位置','data-action="browse-root"')}</div><div data-root-browser-slot></div>${dirField(state.settingsDir,'settings')}<p class="sub">应用自带目录：<span class="path">${esc(state.root)}</span></p><div class="summary"><p>同名文件不会覆盖</p><p class="sub">新建任务使用独立目录。已有任务保留创建时的保存位置。</p></div></section><section class="section"><h2>搜索并发</h2><p class="sub">优先频道先启动；普通频道在其后并发补充。插件来源独立并发。</p><div class="form-grid"><label class="field">优先频道<select name="search_priority_concurrency">${[1,2,3,4,6,8].map(n=>`<option value="${n}" ${s.search_priority_concurrency===n?'selected':''}>${n} 路</option>`).join('')}</select></label><label class="field">普通频道<select name="search_channel_concurrency">${[2,4,6,8,10,12,16].map(n=>`<option value="${n}" ${s.search_channel_concurrency===n?'selected':''}>${n} 路</option>`).join('')}</select></label></div></section><section class="section"><h2>下载方式</h2><p class="sub">首版按顺序执行任务；每个文件可使用多路分片。</p><div class="form-grid"><label class="field">单文件并发<select name="concurrency">${[1,2,3,4,6,8].map(n=>`<option ${s.concurrency===n?'selected':''}>${n}</option>`).join('')}</select></label><label class="field">分片大小<select name="chunk_mib">${[1,5,10,20,32,64].map(n=>`<option value="${n}" ${s.chunk_mib===n?'selected':''}>${n} MiB</option>`).join('')}</select></label></div><p class="sub">参数调整应用于下一个执行的任务；已有分片沿用原记录。并发不保证提高速度。</p></section><button class="primary" type="submit">保存设置</button><p class="login-error" role="alert" tabindex="-1"></p></form><section class="section"><h2>本地数据</h2><p class="sub">任务、分片完成记录与凭据保存在应用目录。备份时需同时保留 state.json 和 master.key。</p><p class="sub">已写入的文件会进行大小和本地 SHA-256 检查；有上游 MD5 时额外进行 MD5 对比。</p></section></div>`;}
function settingsPage(){return settingsBasePage()+`<div data-agent-access>${agentAccessHTML(state.agents,state.newAgent,state.settings.download_root||state.root,state.settings.default_dir,location.origin)}</div>`;}
function renderAgentAccess(){const slot=$('[data-agent-access]');if(slot)slot.innerHTML=agentAccessHTML(state.agents,state.newAgent,state.settings.download_root||state.root,state.settings.default_dir,location.origin);}
document.addEventListener('submit',async event=>{const form=event.target;if(form?.id!=='agent-grant-form')return;event.preventDefault();event.stopImmediatePropagation();const button=form.querySelector('[type=submit]');button.disabled=true;try{const data=new FormData(form),scopes=['resources:read','tasks:read','events:read'];if(data.has('allow_download'))scopes.push('downloads:create');const created=await api('agents',{name:String(data.get('agent_name')).trim(),directory:String(data.get('agent_directory')).trim(),expires_days:Number(data.get('agent_days')),scopes});state.newAgent=created;state.agents=[...state.agents,created.agent];renderAgentAccess();toast('授权已创建，请保存只显示一次的访问令牌。');}catch(error){showFormError(form,error.message);}finally{button.disabled=false;}},true);
document.addEventListener('click',async event=>{const button=event.target.closest('button');if(!button)return;if(button.dataset.action==='hide-agent-token'){event.preventDefault();event.stopImmediatePropagation();state.newAgent=null;renderAgentAccess();return;}if(button.dataset.revokeAgent){event.preventDefault();event.stopImmediatePropagation();const id=button.dataset.revokeAgent;state.modal={type:'confirm',title:'撤销这个 Agent 的授权？',message:'此令牌将立即失效，已有下载任务继续保留。其他 Agent 不受影响。',label:'撤销授权',perform:async()=>{await api('agents/'+id,{action:'revoke'});state.agents=state.agents.map(agent=>agent.id===id?{...agent,revoked:true,active:false}:agent);if(state.newAgent?.agent?.id===id)state.newAgent=null;renderAgentAccess();toast('Agent 授权已撤销。');}};openModal(button);}},true);
function dialogHead(title,sub=''){return `<div class="dialog-head"><div><h2 id="dialog-title">${esc(title)}</h2>${sub?`<p class="sub">${esc(sub)}</p>`:''}</div>${btn('×','data-action="close" aria-label="关闭"')}</div>`;}
let modalTrigger;
function openModal(trigger=document.activeElement){if(!dialog.open){modalTrigger=trigger;dialog.replaceChildren();}renderModal();if(!dialog.open)dialog.showModal();}
function closeModal(){directoryController?.abort();state.modal=null;state.browser=null;if(dialog.open)dialog.close();else{const trigger=modalTrigger;modalTrigger=null;trigger?.isConnected&&trigger.focus({preventScroll:true});}}
function importModal(url='',title=''){state.modal={type:'import',url,title,code:'',error:'',dir:state.settings.default_dir||'',downloadRoot:state.downloadRoot};state.browser=null;openModal();}
function renderModal(){const m=state.modal;if(!m)return;let markup='';
 if(m.type==='import')markup=dialogHead('导入分享链接','分享内容仅发送给这台 NAS 与夸克')+`<form id="import-form"><label class="field">夸克分享内容<textarea name="url" required autocomplete="off" spellcheck="false" placeholder="例如 https://pan.quark.cn/s/…">${esc(m.url)}</textarea></label><label class="field">提取码（可选）<input name="code" maxlength="16" value="${esc(m.code)}" autocomplete="off" spellcheck="false" placeholder="已有提取码时可留空…"></label><p class="login-error" role="alert">${esc(m.error)}</p><div class="dialog-foot">${btn('返回','data-action="close"')}<button type="submit" class="primary">解析分享</button></div></form>`;
 if(m.type==='preview')markup=dialogHead(m.title||'分享内容','夸克分享 · 已读取当前文件清单')+`${m.warning?`<p class="alert">${esc(m.warning)}</p>`:''}<div class="meta"><span class="badge">夸克</span><span>${m.files.length} 个顶层项目</span><span>${m.share.parent==='0'?'整个分享':'链接指定子目录'}</span></div><ul class="file-list">${m.files.map(f=>`<li><span>${f.file?'':'目录 · '}${esc(f.file_name)}</span><small>${f.file?bytes(f.size):'目录'}</small></li>`).join('')}</ul><p class="sub">下载整个当前分享目录。清单用于预览。</p>${dirField(m.dir,'preview')}<div class="summary"><p>夸克转存位置 <span class="path">/盘栈转存/任务编号</span></p><p class="sub">云端文件默认保留；NAS 中会创建独立任务文件夹。</p></div><p class="login-error" role="alert">${esc(m.error)}</p><div class="dialog-foot">${btn('返回','data-action="close"')}${btn('转存并下载','data-action="create-task"','primary')}</div>`;
 if(m.type==='task'){const t=state.tasks.find(t=>t.id===m.id);if(!t){closeModal();return;}const p=taskProgress(t);markup=dialogHead(t.title,'任务详情')+`<span class="badge ${t.status==='attention'?'warning':['paused','cancelled'].includes(t.status)?'neutral':''}">${labels[p.phase==='verifying'&&t.status==='downloading'?'verifying':t.status]}</span><div class="steps">${['排队','转存','下载','校验','完成'].map((v,i)=>`<span class="${({queued:0,transferring:1,downloading:2,verifying:3,completed:4}[p.phase==='verifying'&&t.status==='downloading'?'verifying':t.status]===i)?'current':''}">${v}</span>${i<4?'<span>→</span>':''}`).join('')}</div><p class="path">${esc(fullPath(t.directory))}</p>${taskTelemetry(t)}${t.error?`<p class="alert" role="alert">${esc(t.error)}</p>`:''}<ul class="file-list">${(t.files||[]).map(f=>`<li><span>${esc(f.path)}</span><small>${f.done?'已校验':`${bytes(p.currentFile===f.path?p.fileDone:f.downloaded)} / ${bytes(f.size)}`}</small></li>`).join('')}</ul>${!t.files?.length?'<p class="sub">转存完成后建立下载文件清单。</p>':`<p class="sub">${bytes(p.done)} / ${bytes(p.total)} · ${t.files.filter(f=>f.done).length} 个文件已校验</p>`}${t.uncertain?`<div class="summary"><p>核对夸克中的目录：</p><p class="path">/盘栈转存/${esc(t.id)}/batch-编号</p><p class="sub">“核对已有结果”会比对文件名、类型和大小；“重新转存”只允许当前批次目录为空。</p><label class="check check-cloud"><input type="checkbox" data-confirm-cloud ${dialog.querySelector('[data-confirm-cloud]')?.checked?'checked':''}>我已核对上述云端目录</label></div>`:''}<details><summary>查看诊断与校验记录</summary><p>任务编号：${esc(t.id)}</p><p>云端批次：${(t.batches||[]).filter(b=>b.done).length} / ${(t.batches||[]).length} 已完成</p><p>校验方式：大小与本地 SHA-256；存在上游 MD5 时进行额外校验。凭据、分享令牌和直链不显示。</p></details><p class="login-error" role="alert" tabindex="-1"></p><div class="dialog-foot">${btn('关闭','data-action="close"')}${t.uncertain?btn('核对已有结果',`data-job-action="reconcile" data-id="${t.id}" data-cloud-action ${dialog.querySelector('[data-confirm-cloud]')?.checked?'':'disabled'}`,'primary')+btn('确认空目录，重新转存',`data-job-action="resubmit" data-id="${t.id}" data-cloud-action ${dialog.querySelector('[data-confirm-cloud]')?.checked?'':'disabled'}`):t.status==='attention'?btn('重试',`data-job-action="retry" data-id="${t.id}"`,'primary'):''}</div>`;}
 if(m.type==='confirm')markup=dialogHead(m.title)+`<p class="sub">${esc(m.message)}</p><div class="dialog-foot">${btn('返回','data-action="close"')}${btn(m.label,`data-action="confirm-action"`,'primary')}</div>`;
 if(m.type==='task')patchHTML(dialog,markup);else dialog.innerHTML=markup;
 dialog.setAttribute('aria-labelledby','dialog-title');
}
async function parseImport(form){const data=new FormData(form),m=state.modal;m.url=String(data.get('url')).trim();m.code=String(data.get('code')).trim();if(!state.account){closeModal();state.page='account';render();toast('请先保存夸克连接凭据，再解析分享。');return;}const submit=form.querySelector('[type=submit]');submit.disabled=true;submit.textContent='解析中…';try{const result=await api('preview',{url:m.url,code:m.code});if(state.modal!==m)return;for(const row of state.results){if(row.id===resultKey({url:m.url})){row.checked_at=result.checked_at;row.availability='accessible';}}renderSearch();state.modal={type:'preview',...result,url:m.url,code:m.code,title:m.title||result.files[0]?.file_name||'夸克分享',dir:m.dir||'',downloadRoot:m.downloadRoot||state.downloadRoot,error:''};openModal();}catch(e){if(state.modal===m){m.error=e.message;renderModal();}}}
async function openBrowser(context,path,root=undefined,rootBrowser=false){directoryController?.abort();directoryController=new AbortController();root=root||(context==='settings'?state.downloadRoot:state.modal?.downloadRoot)||state.downloadRoot;state.browser={context,path,root,rootBrowser};const slot=context==='settings'?$('#main '+(rootBrowser?'[data-root-browser-slot]':'[data-browser-slot]')):dialog.querySelector('[data-browser-slot]');if(!slot)return;slot.innerHTML='<div class="browser" role="status">正在读取目录…</div>';try{const data=await api('directories?root='+encodeURIComponent(root)+'&path='+encodeURIComponent(path),undefined,directoryController.signal);if(!state.browser||state.browser.path!==path||state.browser.root!==root)return;slot.innerHTML=`<div class="browser"><div class="browser-header"><span class="path">${esc(root+(path?'/'+path:''))}</span>${btn('使用此目录','data-use-dir','primary')}</div><div class="browser-folders">${path?btn('↑ 上一级',`data-open-dir="${esc(path.split('/').slice(0,-1).join('/'))}"`):''}${data.directories.map(name=>btn(`目录 · ${esc(name)}`,`data-open-dir="${esc(path?path+'/'+name:name)}"`)).join('')}${!data.directories.length?'<span class="sub">没有子目录</span>':''}</div></div>`;}catch(e){if(e.name!=='AbortError')slot.innerHTML=`<p class="alert">${esc(e.message)}</p>`;}}
async function actTask(id,action){try{await api('tasks/'+id,{action});state.tasks=await api('tasks');if(state.page==='tasks')renderPage();if(state.modal?.type==='task')renderModal();toast(action==='cancel'?'任务已取消，云端和本地内容会保留。':'任务状态已更新。');}catch(e){const err=dialog.open?dialog.querySelector('.login-error'):null;if(err)err.textContent=e.message;else toast(e.message);}}
function showFormError(form,message){const error=form.querySelector('.login-error');if(!error)return;error.textContent=message;error.tabIndex=-1;error.focus({preventScroll:true});}
document.addEventListener('click',async e=>{
 const button=e.target.closest('button');if(!button)return;
 if(button.dataset.action==='create-task'){
  e.preventDefault();e.stopImmediatePropagation();const modal=state.modal;button.disabled=true;button.textContent='正在创建…';
  try{await api('tasks',{url:modal.url,code:modal.code,title:modal.title,directory:modal.dir,download_root:modal.downloadRoot||state.downloadRoot});closeModal();state.page='tasks';state.filter='全部';state.tasks=await api('tasks');render();toast('任务已创建。你可以继续搜索其他资源。');}
  catch(error){if(state.modal===modal){modal.error=error.message;renderModal();}}return;
 }
 if(button.dataset.action==='browse-root'){
  e.preventDefault();e.stopImmediatePropagation();
  const root=$('#download-root')?.value.trim();if(!root){toast('请输入 NAS 共享目录路径。');return;}
  await openBrowser('settings','',root,true);return;
 }
 if(button.hasAttribute('data-open-dir')){
  e.preventDefault();e.stopImmediatePropagation();await openBrowser(state.browser.context,button.dataset.openDir,state.browser.root,state.browser.rootBrowser);return;
 }
 if(button.hasAttribute('data-use-dir')){
  e.preventDefault();e.stopImmediatePropagation();const {context,path,root}=state.browser,selectedRoot=root+(path?'/'+path:'');state.browser=null;
  if(context==='settings'){const values=new FormData($('#settings-form'));state.downloadRoot=selectedRoot;state.settingsDir='';renderPage();for(const name of ['concurrency','chunk_mib','search_priority_concurrency','search_channel_concurrency'])$(`#settings-form [name=${name}]`).value=values.get(name);}
  else{state.modal.downloadRoot=selectedRoot;state.modal.dir='';renderModal();}
  return;
 }
 if(button.dataset.action==='save-settings')return;
},true);
document.addEventListener('submit',async e=>{
 const form=e.target;if(form?.id!=='settings-form')return;
 e.preventDefault();e.stopImmediatePropagation();
 const button=form.querySelector('button[type="submit"]');if(button.disabled)return;button.disabled=true;
 try{
  const data=new FormData(form),settings={...state.settings,download_root:String(data.get('download_root')||'').trim(),default_dir:state.settingsDir,concurrency:Number(data.get('concurrency')),chunk_mib:Number(data.get('chunk_mib')),search_priority_concurrency:Number(data.get('search_priority_concurrency')),search_channel_concurrency:Number(data.get('search_channel_concurrency'))};
  const result=await api('settings',settings);state.settings=settings;state.downloadRoot=settings.download_root;state.root=result.root||state.root;toast('设置已保存。');
 }catch(error){showFormError(form,error.message);}finally{button.disabled=false;}
},true);
document.addEventListener('input',e=>{if(e.target.id==='download-root'&&e.target.value.trim()!==state.downloadRoot)state.settingsDir='';});
document.addEventListener('submit',async e=>{const f=e.target;if(!(f instanceof HTMLFormElement))return;e.preventDefault();
 if(f.id==='auth-form'){const data=new FormData(f),b=f.querySelector('button');b.disabled=true;try{await api(state.status.configured?'login':'setup',Object.fromEntries(data));await boot();}catch(er){showFormError(f,er.message);b.disabled=false;}return;}
 if(f.id==='search-form'){const q=String(new FormData(f).get('query')).trim();if(q.includes('pan.quark.cn/s/')){importModal(q);return;}beginSearch(q);return;}
 if(f.id==='import-form'){await parseImport(f);return;}
 if(f.id==='account-form'){const b=f.querySelector('button');b.disabled=true;try{await api('account',{cookie:String(new FormData(f).get('cookie'))});state.account=true;f.reset();render();toast('连接凭据已加密保存，可以验证连接。');}catch(er){showFormError(f,er.message);b.disabled=false;}return;}
 if(f.id==='settings-form'){const b=f.querySelector('button[type=submit]');b.disabled=true;try{const data=new FormData(f),settings={...state.settings,default_dir:state.settingsDir,concurrency:Number(data.get('concurrency')),chunk_mib:Number(data.get('chunk_mib')),search_priority_concurrency:Number(data.get('search_priority_concurrency')),search_channel_concurrency:Number(data.get('search_channel_concurrency'))};await api('settings',settings);state.settings=settings;toast('设置已保存。');}catch(er){showFormError(f,er.message);}finally{b.disabled=false;}return;}
});
document.addEventListener('click',async e=>{const b=e.target.closest('button,a[data-page]');if(!b||b.disabled)return;
 if(b.dataset.page){if(b.tagName==='A'&&(e.metaKey||e.ctrlKey||e.shiftKey||e.altKey||e.button!==0))return;e.preventDefault();requestPage(b.dataset.page);return;}
 if(b.dataset.filter){state.filter=b.dataset.filter;renderPage();return;}
 if(b.dataset.sourcePreset){
  if(state.sourceSaving)return;
  const preset=b.dataset.sourcePreset;
  if(preset==='reset')state.sourceDraft=null;
  else state.sourceDraft=state.sourceList.filter(s=>preset==='all'||preset==='aggregate'&&!s.id.startsWith('panplugin:')).map(s=>s.id);
  state.sourceError='';renderSources();return;
 }
 if(b.dataset.sourceGroup){selectSourceGroup(b.dataset.sourceGroup,b.dataset.enable==='true');return;}
 if(b.dataset.action==='save-sources'){await saveSources();return;}

 if(b.hasAttribute('data-result')){const r=state.results.find(r=>r.id===b.dataset.result);if(!r)return;importModal(r.url,r.title);const f=$('#import-form');await parseImport(f);return;}
 if(b.dataset.task){state.modal={type:'task',id:b.dataset.task};state.browser=null;openModal(b);return;}
 if(b.dataset.jobAction){const {id,jobAction:action}=b.dataset;if(action==='cancel'){state.modal={type:'confirm',title:'取消这项任务？',message:'取消不会删除已下载文件或云端转存内容。',label:'取消任务',perform:()=>actTask(id,action)};openModal();return;}b.disabled=true;await actTask(id,action);if(b.isConnected)b.disabled=false;return;}
 if(b.dataset.browse){const context=b.dataset.browse;await openBrowser(context,context==='settings'?state.settingsDir:state.modal.dir);return;}
 if(b.hasAttribute('data-open-dir')){await openBrowser(state.browser.context,b.dataset.openDir);return;}
 if(b.hasAttribute('data-use-dir')){const {context,path}=state.browser;state.browser=null;if(context==='settings'){state.settingsDir=path;const values=new FormData($('#settings-form'));renderPage();for(const name of ['concurrency','chunk_mib','search_priority_concurrency','search_channel_concurrency'])$(`#settings-form [name=${name}]`).value=values.get(name);}else{state.modal.dir=path;renderModal();}return;}
 const action=b.dataset.action;
 if(action==='close')closeModal();
 if(action==='reload')boot();
 if(action==='import')importModal();
 if(action==='pause-search')pauseSearch();
 if(action==='resume-search')resumeSearch();
 if(action==='cancel-search')cancelSearch();
 if(action==='refresh-order'){state.searchOrder=[];renderSearch();}
 if(action==='logout'){state.newAgent=null;state.agents=[];cancelSearch();try{await api('logout',{});state.status.authenticated=false;state.tasks=[];state.results=[];renderAuth();}catch(er){toast(er.message);}}
 if(action==='verify'){b.disabled=true;b.textContent='验证中…';try{const data=await api('account/verify',{});$('#verify-result').innerHTML=`<p class="summary">${esc(data.message)}</p>`;}catch(er){$('#verify-result').innerHTML=`<p class="alert error" role="alert">${esc(er.message)}</p>`;}finally{b.disabled=false;b.textContent='验证连接';}}
 if(action==='disconnect'){state.modal={type:'confirm',title:'断开夸克账号？',message:'新的转存下载将需要重新连接。正在执行的任务使用已有会话，若需停止，请先暂停任务。',label:'断开账号',perform:async()=>{await api('account',{cookie:''});state.account=false;render();toast('账号已断开。');}};openModal();}
 if(action==='confirm-action'){const perform=state.modal?.perform;b.disabled=true;try{await perform?.();closeModal();}catch(er){b.disabled=false;toast(er.message);}}
 if(action==='create-task'){const m=state.modal;b.disabled=true;b.textContent='正在创建…';try{await api('tasks',{url:m.url,code:m.code,title:m.title,directory:m.dir});closeModal();state.page='tasks';state.filter='全部';state.tasks=await api('tasks');render();toast('任务已创建。你可以继续搜索其他资源。');}catch(er){if(state.modal===m){m.error=er.message;renderModal();}}}
});
document.addEventListener('change',async e=>{const el=e.target;if(el.dataset.searchOption){const key={type:'searchType',source:'searchSource',days:'searchDays',sort:'searchSort'}[el.dataset.searchOption];if(key){state[key]=key==='searchDays'?Number(el.value):el.value;state.searchOrder=[];renderSearch();}return;}if(el.hasAttribute('data-confirm-cloud')){dialog.querySelectorAll('[data-cloud-action]').forEach(b=>b.disabled=!el.checked);}
 if(el.hasAttribute('data-source')){
  if(state.sourceSaving)return;
  const selected=new Set(sourceSelection());if(el.checked)selected.add(el.dataset.source);else selected.delete(el.dataset.source);
  state.sourceDraft=[...selected];state.sourceError='';renderSources();
 }

});
dialog.addEventListener('cancel',()=>{state.modal=null;state.browser=null;directoryController?.abort();});
dialog.addEventListener('click',e=>{if(e.target===dialog){const r=dialog.getBoundingClientRect();if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom)closeModal();}});
function refreshTaskDialog(){
 if(dialog.open&&state.modal?.type==='task')renderModal();
}
async function refreshTasks(){
 if(polling||!state.status?.authenticated||state.page!=='tasks')return;
 polling=true;
 try{
  const tasks=await api('tasks'),changed=JSON.stringify(tasks)!==JSON.stringify(state.tasks);state.tasks=tasks;
  if(state.page==='tasks'&&changed){renderPage();refreshTaskDialog();updateTaskCount();}
 }catch(e){toast(e.message);}finally{polling=false;}
}
function updateTaskCount(){
 const count=document.querySelector('.main-nav [data-page="tasks"] .count');if(!count)return;
 const active=state.tasks.filter(t=>!['completed','cancelled'].includes(t.status)).length;
 count.textContent=active;count.hidden=!active;
}
function hasUnsavedForm(){
	if($('#agent-grant-form [name=agent_name]')?.value.trim())return true;
 if(state.page==='sources')return sourcesDirty();
 const settings=$('#settings-form');
 if(settings){const values=new FormData(settings);return state.settingsDir!==state.settings.default_dir||String(values.get('download_root')||'')!==(state.settings.download_root||state.root)||['concurrency','chunk_mib','search_priority_concurrency','search_channel_concurrency'].some(name=>Number(values.get(name))!==state.settings[name]);}
 return !!$('#account-form [name="cookie"]')?.value.trim();
}
function navigatePage(page){
 if(!pageTitles[page])return;
 if(state.page==='settings'){state.settingsDir=state.settings.default_dir;state.downloadRoot=state.settings.download_root||state.root;}
 if(state.page==='sources'){state.sourceDraft=null;state.sourceError='';}
 state.page=page;state.browser=null;history.replaceState(null,'','#'+page);render();
 $('#main')?.focus({preventScroll:true});window.scrollTo({top:0,behavior:'instant'});
 if(page==='tasks')refreshTasks();
}
function requestPage(page){
 if(page===state.page)return;
 if(state.sourceSaving){toast('来源设置正在保存，请稍候。');return;}
 if(hasUnsavedForm()){
  state.modal={type:'confirm',title:'放弃未保存的修改？',message:'此页有尚未保存的修改。可以返回保存，或放弃修改后离开。',label:'放弃并离开',perform:()=>navigatePage(page)};openModal();
 }else navigatePage(page);
}
window.addEventListener('hashchange',()=>{const page=location.hash.slice(1);if(pageTitles[page]){history.replaceState(null,'','#'+state.page);requestPage(page);}});
window.addEventListener('beforeunload',e=>{if(hasUnsavedForm()){e.preventDefault();e.returnValue='';}});
setInterval(()=>{if(!document.hidden)refreshTasks();},1000);
boot();
