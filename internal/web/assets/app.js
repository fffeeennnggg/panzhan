import {mergeSearchResults, selectResults, highlight, resultKey} from './search-results.mjs';
const $ = s => document.querySelector(s);
const app = $('#app'), dialog = $('#dialog');
const esc = v => String(v ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const bytes = n => { n = Number(n || 0); if(n<1024) return `${n} B`; const units=['KiB','MiB','GiB','TiB']; let i=-1; do{n/=1024;i++;}while(n>=1024&&i<3); return `${n.toFixed(1)} ${units[i]}`; };
const labels={queued:'排队中',transferring:'转存中',downloading:'下载中',verifying:'校验中',completed:'已完成',attention:'需要处理',paused:'已暂停',cancelled:'已取消'};
const state={searchSource:'',searchDays:0,searchSort:'relevance',localPending:false,localCount:0,firstResultMS:null,searchStarted:0,page:'search',status:null,settings:null,root:'',account:false,tasks:[],results:[],report:[],searched:false,query:'',filter:'全部',searching:false,searchPaused:false,searchRun:0,searchRemaining:[],searchQueue:[],searchActive:0,searchActivePriority:0,searchActiveChannels:0,searchActiveProviders:0,searchError:'',settingsDir:'',sourceList:[],modal:null,browser:null};
let toastTimer, searchControllers=new Map(), polling=false, directoryController, localController;
const btn=(text,attrs='',kind='')=>`<button type="button" class="${kind}" ${attrs}>${text}</button>`;
const head=(title,desc,action='')=>`<div class="page-head"><div><h1>${title}</h1><p class="sub">${desc}</p></div>${action}</div>`;
const empty=(title,desc,action='')=>`<div class="empty"><div class="empty-symbol" aria-hidden="true">↓</div><h3>${title}</h3><p>${desc}</p>${action}</div>`;
function toast(text){$('#toast').textContent=text;$('#toast').classList.add('show');clearTimeout(toastTimer);toastTimer=setTimeout(()=>$('#toast').classList.remove('show'),5000);}
async function api(path,body,signal){const res=await fetch('/api/'+path,{method:body===undefined?'GET':'POST',headers:body===undefined?{}:{'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body),signal,credentials:'same-origin'});let data;try{data=await res.json();}catch{throw new Error('服务响应无法解析，请检查程序是否运行。');}if(!res.ok){if(res.status===401&&state.status?.authenticated){cancelSearch();state.status.authenticated=false;renderAuth();}throw new Error(data.error||`请求失败 (${res.status})`);}return data;}
async function boot(){try{state.status=await api('status');if(!state.status.authenticated){renderAuth();return;}await load();render();}catch(e){app.innerHTML=empty('暂时无法连接盘栈',esc(e.message),btn('重新连接','data-action="reload"'));}}
async function load(){const [config,account,tasks,sources]=await Promise.all([api('settings'),api('account'),api('tasks'),api('sources')]);state.settings=config.settings;state.root=config.root;state.settingsDir=config.settings.default_dir;state.account=account.configured;state.tasks=tasks;state.sourceList=sources;}
function renderSearch(){
 if(state.page!=='search'||!state.status?.authenticated)return;
 const feedback=$('#search-feedback'),controls=$('#search-controls');
 if(!feedback||!controls){renderPage();return;}
 const active=document.activeElement,focusName=active?.dataset?.searchOption;
 const diagnosticsOpen=!!feedback.querySelector('.source-diagnostics')?.open;
 feedback.innerHTML=searchFeedback();controls.innerHTML=searchControls();
 const diagnostics=feedback.querySelector('.source-diagnostics');if(diagnostics)diagnostics.open=diagnosticsOpen;
 $('.search-intro')?.classList.toggle('search-compact',state.searching||state.searched);
 if(focusName)feedback.querySelector(`[data-search-option="${focusName}"]`)?.focus({preventScroll:true});
}
function finishSearchIfIdle(){
 if(state.searching&&!state.localPending&&!state.searchPaused&&state.searchActive===0&&state.searchQueue.length===0){state.searching=false;state.searched=true;}
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
 if(state.results.length&&state.firstResultMS===null)state.firstResultMS=Date.now()-state.searchStarted;
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
 localController?.abort();for(const controller of searchControllers.keys())controller.abort();searchControllers.clear();
 const run=++state.searchRun;
 Object.assign(state,{query,results:[],report:[],searchError:'',searched:false,searchPaused:false,searching:true,localPending:true,localCount:0,firstResultMS:null,searchStarted:Date.now(),searchActive:0,searchActivePriority:0,searchActiveChannels:0,searchActiveProviders:0,searchSource:'',searchDays:0});
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
function resumeSearch(){state.searchPaused=false;if(state.searchActive===0)scheduleSearch();}
function cancelSearch(){
 state.searchRun++;state.searchPaused=false;state.localPending=false;state.searchQueue=[];state.searchRemaining=[];
 localController?.abort();localController=null;for(const controller of searchControllers.keys())controller.abort();searchControllers.clear();
 state.searchActive=0;state.searchActivePriority=0;state.searchActiveChannels=0;state.searchActiveProviders=0;state.searching=false;state.searched=true;renderSearch();
}
function renderAuth(){state.modal=null;state.browser=null;if(dialog.open)dialog.close();const setup=!state.status.configured;app.innerHTML=`<main class="auth"><a class="brand" href="/"><img src="/mark.svg" alt=""><strong>盘栈</strong></a><p class="eyebrow">PERSONAL NAS WORKSPACE</p><h1>${setup?'让资源回到你的 NAS':'欢迎回来'}</h1><p>${setup?'设置管理密码，然后连接夸克并选择保存目录。':'登录后继续搜索资源和管理下载。'}</p><form id="auth-form">${setup?'<label class="field">安装令牌<input name="token" required autocomplete="off" placeholder="读取 NAS 中的 setup-token.txt"><small>令牌位于应用数据目录，仅用于首次配置。</small></label>':''}<label class="field">管理密码<input type="password" name="password" required minlength="12" maxlength="256" autocomplete="${setup?'new-password':'current-password'}" placeholder="${setup?'至少 12 个字符':'输入管理密码'}"></label><button class="primary" type="submit">${setup?'创建工作台':'登录'}</button><div class="login-error" role="alert"></div></form><p class="auth-note">你的凭据和任务记录由这台 NAS 保存。盘栈直接连接搜索来源和夸克。</p></main>`;}
function render(){if(!state.status?.authenticated)return;app.innerHTML=`<div class="shell"><header class="top"><a href="/" class="brand"><img src="/mark.svg" alt=""><strong>盘栈</strong><span>资源到家，一步入库</span></a><div class="top-actions"><span>本地工作台</span>${btn(state.account?'夸克已配置':'连接夸克','data-page="account"','ghost')}${btn('退出','data-action="logout"','ghost')}</div></header><nav class="main-nav" aria-label="主导航">${[['search','资源搜索'],['tasks','下载任务'],['account','网盘账号'],['sources','搜索来源'],['settings','设置']].map(([id,label])=>btn(label+(id==='tasks'&&state.tasks.some(t=>!['completed','cancelled'].includes(t.status))?` <span class="count">${state.tasks.filter(t=>!['completed','cancelled'].includes(t.status)).length}</span>`:''),`data-page="${id}" ${state.page===id?'aria-current="page"':''}`,state.page===id?'active':'')).join('')}</nav><main id="main"></main><footer class="footer"><span>盘栈 <span class="version">v0.1.0</span> · 飞牛 NAS 资源工具</span><span>云端文件默认保留 · 本地文件默认不覆盖</span></footer></div>`;renderPage();}
function renderPage(){const main=$('#main');if(!main)return;if(state.page==='search')main.innerHTML=searchPage();if(state.page==='tasks')main.innerHTML=tasksPage();if(state.page==='account')main.innerHTML=accountPage();if(state.page==='sources')main.innerHTML=sourcesPage();if(state.page==='settings')main.innerHTML=settingsPage();main.querySelectorAll('[data-percent]').forEach(e=>e.style.width=e.dataset.percent+'%');}
function searchControls(){
 return `${btn('导入分享链接','data-action="import"','ghost import-link')}${state.searching?(state.searchPaused?btn('继续搜索','data-action="resume-search"','ghost import-link'):btn('暂停搜索','data-action="pause-search"','ghost import-link'))+btn('停止搜索','data-action="cancel-search"','ghost import-link'):''}`;
}
function searchPage(){
 return `<section class="search-intro ${state.searching||state.searched?'search-compact':''}"><p class="eyebrow">SEARCH · SAVE · DOWNLOAD</p><h1>找到资源，交给 NAS</h1><p>已有资源先显示，新的搜索结果陆续补充。</p><form id="search-form" class="search-form"><label class="search-input"><span aria-hidden="true">⌕</span><input name="query" aria-label="资源关键词或夸克分享链接" value="${esc(state.query)}" placeholder="搜索资源名称，或粘贴夸克分享链接" required maxlength="600"></label><button type="submit" class="primary">搜索</button></form><div id="search-controls" class="actions">${searchControls()}</div></section>${!state.account?'<p class="alert">可以先搜索资源。查看分享内容和下载前，请在“网盘账号”连接夸克。</p>':''}<div id="search-feedback">${searchFeedback()}</div>`;
}
function discoveryLabel(r){
 if(!r.discovered_at)return '发现时间未知';
 const date=new Date(r.discovered_at);if(!Number.isFinite(date.getTime()))return '发现时间未知';
 return '首次发现 '+date.toLocaleDateString('zh-CN');
}
function searchFeedback(){
 const visible=selectResults(state.results,{query:state.query,source:state.searchSource,days:state.searchDays,sort:state.searchSort});
 const errors=state.report.filter(s=>s.error),hasRun=state.searching||state.searched;
 const status=state.localPending?'正在读取本地资源':state.searchPaused?'搜索已暂停':state.searching?`外部搜索中 · ${state.searchRemaining.length} 项待完成`:state.searched?'搜索已结束':'从关键词或分享链接开始';
 const first=state.firstResultMS===null?'':` · 首条 ${(state.firstResultMS/1000).toFixed(2)} 秒`;
 const filters=state.results.length?`<div class="search-filters"><span class="badge">夸克网盘</span><label>来源<select data-search-option="source" aria-label="筛选来源"><option value="">全部来源</option>${state.sourceList.filter(s=>state.results.some(r=>r.origins?.some(o=>o.id===s.id))).map(s=>`<option value="${esc(s.id)}" ${state.searchSource===s.id?'selected':''}>${esc(s.name)}</option>`).join('')}</select></label><label>发现时间<select data-search-option="days" aria-label="筛选发现时间">${[[0,'不限时间'],[3,'近 3 天'],[7,'近 7 天'],[30,'近 30 天']].map(([v,t])=>`<option value="${v}" ${state.searchDays===v?'selected':''}>${t}</option>`).join('')}</select></label><label>排序<select data-search-option="sort" aria-label="结果排序"><option value="relevance" ${state.searchSort==='relevance'?'selected':''}>相关度优先</option><option value="newest" ${state.searchSort==='newest'?'selected':''}>最近发现优先</option></select></label></div>`:'';
 const list=visible.length?`<ul class="rows">${visible.map(r=>`<li class="result"><span class="file-mark" aria-hidden="true">夸克</span><div class="row-body"><h3>${highlight(r.title,state.query)}</h3><div class="meta"><span class="badge">${r.local?'本地记录':r.cached?'近期缓存':'本次发现'}</span><span>${esc((r.origins||[]).map(o=>o.name).join(' · ')||r.source)}</span><span>${discoveryLabel(r)}</span></div><p class="result-check">${r.checked_at&&r.availability==='accessible'?`曾成功读取 · ${esc(new Date(r.checked_at).toLocaleString('zh-CN'))}`:'分享内容待检查'}${r.published_at?` · 来源发布 ${esc(new Date(r.published_at).toLocaleDateString('zh-CN'))}`:''}</p></div>${btn('查看资源',`data-result="${esc(r.id)}"`)}</li>`).join('')}</ul>`:state.results.length?empty('没有符合筛选条件的结果','调整来源或发现时间，查看其他结果。'):state.searching?'<div class="loading" role="status">正在查找资源。找到结果后会立即显示，无需等待全部来源完成。</div>':empty(state.searched?'暂未找到匹配资源':'把网盘资源，存进自己的空间',state.searched?(errors.length?'部分来源暂时不可用，可以换个关键词或导入分享链接。':'试试更短的关键词，或直接导入夸克分享。'):'搜索成功的结果会保存在本机，下次搜索可优先显示。',btn('导入分享链接','data-action="import"'));
 return `<div class="section-head"><span role="status">${status}</span><span>${hasRun?`${visible.length} / ${state.results.length} 条 · 本地命中 ${state.localCount}${first}`:'支持搜索中更换关键词'}</span></div>${state.searchError?`<p class="alert" role="alert">${esc(state.searchError)}</p>`:''}${filters}${list}${state.report.length?`<details class="source-diagnostics"><summary>搜索来源状态 · ${state.report.length-errors.length} 个正常返回${errors.length?`，${errors.length} 个来源受影响`:''}</summary>${state.report.map(s=>`<p><strong>${esc(s.name)}</strong> · ${s.cached?'缓存命中':`${s.count||0} 条`}${s.duration_ms?` · ${(s.duration_ms/1000).toFixed(1)} 秒`:''}${s.error?` · ${esc(s.error)}`:''}${s.retry_at?` · ${esc(new Date(s.retry_at).toLocaleTimeString('zh-CN'))} 后重试`:''}</p>`).join('')}</details>`:''}<p class="tip"><span aria-hidden="true">ⓘ</span><span>发现时间是盘栈首次收录时间。分享是否可访问将在“查看资源”时检查，确认文件和目录后再转存下载。</span></p>`;
}
function taskProgress(t){const files=t.files||[],total=files.reduce((s,f)=>s+f.size,0),done=files.reduce((s,f)=>s+(f.done?f.size:f.downloaded),0);return {total,done,percent:total?Math.min(100,Math.floor(done/total*100)):0};}
function tasksPage(){const filtered=state.tasks.filter(t=>state.filter==='全部'||state.filter==='进行中'&&['queued','transferring','downloading','verifying','paused'].includes(t.status)||state.filter==='需要处理'&&t.status==='attention'||state.filter==='已完成'&&t.status==='completed');return head('下载任务','从转存到本地校验，每一步都有记录。',btn('＋ 新建任务','data-action="import"'))+`<div class="tabs" aria-label="任务状态">${['全部','进行中','需要处理','已完成'].map(f=>btn(f,`data-filter="${f}" aria-pressed="${state.filter===f}"`,state.filter===f?'active':'')).join('')}</div><div aria-live="polite">${filtered.length?filtered.map(t=>{const p=taskProgress(t);return `<article class="task"><div class="between"><div class="row-body"><h3>${esc(t.title)}</h3><p class="path">${esc(fullPath(t.directory))}</p></div><span class="badge ${t.status==='attention'?'warning':''}">${labels[t.status]||esc(t.status)}</span></div>${t.enumerated?`<div class="progress" role="progressbar" aria-label="已下载字节进度" aria-valuenow="${p.percent}" aria-valuemin="0" aria-valuemax="100"><span data-percent="${p.percent}"></span></div><div class="meta"><span>${bytes(p.done)} / ${bytes(p.total)}</span><span>${t.files.filter(f=>f.done).length} / ${t.files.length} 个文件已校验</span></div>`:`<p class="sub">${t.status==='queued'?'等待执行':t.status==='transferring'?'正在核对云端目录与转存任务':'文件清单将在转存完成后建立'}</p>`}${t.error?`<p class="sub">${esc(t.error)}</p>`:''}<div class="actions">${btn('查看详情',`data-task="${t.id}"`)}${['queued','transferring','downloading','verifying'].includes(t.status)?btn('暂停',`data-job-action="pause" data-id="${t.id}"`):t.status==='paused'?btn('继续',`data-job-action="resume" data-id="${t.id}"`):t.status==='attention'&&!t.uncertain?btn('重试',`data-job-action="retry" data-id="${t.id}"`):''}${!['completed','cancelled'].includes(t.status)?btn('取消',`data-job-action="cancel" data-id="${t.id}"`,'ghost'):''}</div></article>`;}).join(''):empty('这里暂时没有任务','选择资源、确认目录后，任务会出现在这里。')}</div>`;}
function accountPage(){return head('网盘账号','使用自己的夸克账号，直接转存和下载。')+`<div class="settings-layout"><section class="section"><div class="between"><div><h3>夸克网盘</h3><p class="sub">网页端 Cookie 连接</p></div><span class="badge ${state.account?'':'warning'}">${state.account?'已配置 · 可重新验证':'未连接'}</span></div><div class="summary"><p>云端转存目录 <span class="path">/盘栈转存/任务编号</span></p><p class="sub">每个任务使用独立目录。下载完成后保留云端文件。</p></div><div class="actions">${btn('验证连接','data-action="verify"','primary')}${state.account?btn('断开账号','data-action="disconnect"','ghost danger'):''}</div><div id="verify-result" aria-live="polite"></div></section><section class="section"><h3>${state.account?'更新连接凭据':'连接夸克'}</h3><p class="sub">在自己登录的夸克网页中获取 Cookie。保存后页面不会回显。</p><form id="account-form"><label class="field">夸克 Cookie<textarea name="cookie" placeholder="粘贴网页端请求中的 Cookie" required autocomplete="off" spellcheck="false"></textarea><small>仅由 NAS 服务加密保存；请勿在截图或公开日志中分享。</small></label><button class="primary" type="submit">保存连接凭据</button><p class="login-error" role="alert"></p></form></section></div>`;}
function sourcesPage(){return head('搜索来源','可以单独启停。来源故障不会影响已有下载。')+`<div class="section-head"><span>已接入来源</span><span>状态来自本次搜索记录</span></div><div class="sources-list">${state.sourceList.map(s=>{const last=state.report.find(r=>r.id===s.id),detail=last?last.channel_count?`${last.channel_count} 个频道 · ${last.channel_errors||0} 个失败 · 返回 ${last.count} 条结果`:last.error||`返回 ${last.count} 条结果`:'当前会话尚未检测';const setup=s.needs_config?' · 需要来源账号或专属配置':'';return `<article class="result"><div class="row-body"><h3>${esc(s.name)}</h3><p class="path">${esc(s.base||(s.needs_config?'需账号或专属配置':'集成搜索插件'))}</p><p class="sub">${s.priority?`优先级 ${s.priority} · `:''}${setup}${last?`${esc(last.checked)} · ${esc(detail)}`:detail}</p></div><span class="badge ${last?.error?'warning':''}">${last?last.error?'部分失败':'已返回':'未检测'}</span><label class="check"><input type="checkbox" data-source="${s.id}" ${s.enabled?'checked':''}>启用</label></article>`;}).join('')}</div><p class="tip"><span>ⓘ</span><span>来源状态按实际搜索请求更新。需要登录或站点专属配置的来源，在完成配置前可能返回空结果。</span></p>`;}
function fullPath(rel){return state.root+(rel?'/'+rel:'');}
function dirField(dir,context){return `<div class="field"><span>NAS 保存目录</span><div class="directory-value"><span class="path">${esc(fullPath(dir))}</span>${btn('选择目录',`data-browse="${context}"`)}</div><small>仅显示应用已授权范围；每个任务会创建独立子目录。</small></div><div data-browser-slot></div>`;}
function settingsPage(){const s=state.settings;return head('设置','下载位置、搜索并发与下载方式，由这台 NAS 管理。')+`<div class="settings-layout"><form id="settings-form"><section class="section"><h3>下载位置</h3>${dirField(state.settingsDir,'settings')}<p class="sub">授权根目录：<span class="path">${esc(state.root)}</span></p><div class="summary"><p>同名文件不会覆盖</p><p class="sub">新建任务使用独立目录，取消任务不会删除已有内容。</p></div></section><section class="section"><h3>搜索并发</h3><p class="sub">优先频道先启动；普通频道在其后并发补充。插件来源独立并发。</p><div class="form-grid"><label class="field">优先频道<select name="search_priority_concurrency">${[1,2,3,4,6,8].map(n=>`<option value="${n}" ${s.search_priority_concurrency===n?'selected':''}>${n} 路</option>`).join('')}</select></label><label class="field">普通频道<select name="search_channel_concurrency">${[2,4,6,8,10,12,16].map(n=>`<option value="${n}" ${s.search_channel_concurrency===n?'selected':''}>${n} 路</option>`).join('')}</select></label></div></section><section class="section"><h3>下载方式</h3><p class="sub">首版按顺序执行任务；每个文件可使用多路分片。</p><div class="form-grid"><label class="field">单文件并发<select name="concurrency">${[1,2,3,4,6,8].map(n=>`<option ${s.concurrency===n?'selected':''}>${n}</option>`).join('')}</select></label><label class="field">分片大小<select name="chunk_mib">${[1,5,10,20,32,64].map(n=>`<option value="${n}" ${s.chunk_mib===n?'selected':''}>${n} MiB</option>`).join('')}</select></label></div><p class="sub">参数调整应用于下一个执行的任务；已有分片沿用原记录。并发不保证提高速度。</p></section><button class="primary" type="submit">保存设置</button><p class="login-error" role="alert"></p></form><section class="section"><h3>本地数据</h3><p class="sub">任务、分片完成记录与凭据保存在应用目录。备份时需同时保留 state.json 和 master.key。</p><p class="sub">已写入的文件会进行大小和本地 SHA-256 检查；有上游 MD5 时额外进行 MD5 对比。</p></section></div>`;}
function dialogHead(title,sub=''){return `<div class="dialog-head"><div><h2>${esc(title)}</h2>${sub?`<p class="sub">${esc(sub)}</p>`:''}</div>${btn('×','data-action="close" aria-label="关闭"')}</div>`;}
function openModal(){renderModal();if(!dialog.open)dialog.showModal();}
function closeModal(){directoryController?.abort();state.modal=null;state.browser=null;dialog.close();}
function importModal(url='',title=''){state.modal={type:'import',url,title,code:'',error:''};state.browser=null;openModal();}
function renderModal(){const m=state.modal;if(!m)return;let markup='';
 if(m.type==='import')markup=dialogHead('导入分享链接','分享内容仅发送给这台 NAS 与夸克')+`<form id="import-form"><label class="field">夸克分享内容<textarea name="url" required placeholder="粘贴分享链接，可包含提取码">${esc(m.url)}</textarea></label><label class="field">提取码（可选）<input name="code" maxlength="16" value="${esc(m.code)}" autocomplete="off" placeholder="链接中已有提取码时可留空"></label><p class="login-error" role="alert">${esc(m.error)}</p><div class="dialog-foot">${btn('返回','data-action="close"')}<button type="submit" class="primary">解析分享</button></div></form>`;
 if(m.type==='preview')markup=dialogHead(m.title||'分享内容','夸克分享 · 已读取当前文件清单')+`${m.warning?`<p class="alert">${esc(m.warning)}</p>`:''}<div class="meta"><span class="badge">夸克</span><span>${m.files.length} 个顶层项目</span><span>${m.share.parent==='0'?'整个分享':'链接指定子目录'}</span></div><ul class="file-list">${m.files.map(f=>`<li><span>${f.file?'':'目录 · '}${esc(f.file_name)}</span><small>${f.file?bytes(f.size):'目录'}</small></li>`).join('')}</ul><p class="sub">下载整个当前分享目录。清单用于预览。</p>${dirField(m.dir,'preview')}<div class="summary"><p>夸克转存位置 <span class="path">/盘栈转存/任务编号</span></p><p class="sub">云端文件默认保留；NAS 中会创建独立任务文件夹。</p></div><p class="login-error" role="alert">${esc(m.error)}</p><div class="dialog-foot">${btn('返回','data-action="close"')}${btn('转存并下载','data-action="create-task"','primary')}</div>`;
 if(m.type==='task'){const t=state.tasks.find(t=>t.id===m.id);if(!t){closeModal();return;}const p=taskProgress(t);markup=dialogHead(t.title,'任务详情')+`<span class="badge ${t.status==='attention'?'warning':''}">${labels[t.status]}</span><div class="steps">${['排队','转存','下载','校验','完成'].map((v,i)=>`<span class="${({queued:0,transferring:1,downloading:2,verifying:3,completed:4}[t.status]===i)?'current':''}">${v}</span>${i<4?'<span>→</span>':''}`).join('')}</div><p class="path">${esc(fullPath(t.directory))}</p>${t.error?`<p class="alert" role="alert">${esc(t.error)}</p>`:''}<ul class="file-list">${(t.files||[]).map(f=>`<li><span>${esc(f.path)}</span><small>${f.done?'已校验':`${bytes(f.downloaded)} / ${bytes(f.size)}`}</small></li>`).join('')}</ul>${!t.files?.length?'<p class="sub">转存完成后建立下载文件清单。</p>':`<p class="sub">${bytes(p.done)} / ${bytes(p.total)} · ${t.files.filter(f=>f.done).length} 个文件已校验</p>`}${t.uncertain?`<div class="summary"><p>核对夸克中的目录：</p><p class="path">/盘栈转存/${esc(t.id)}/batch-编号</p><p class="sub">“核对已有结果”会比对文件名、类型和大小；“重新转存”只允许当前批次目录为空。</p><label class="check check-cloud"><input type="checkbox" data-confirm-cloud>我已核对上述云端目录</label></div>`:''}<details><summary>查看诊断与校验记录</summary><p>任务编号：${esc(t.id)}</p><p>云端批次：${(t.batches||[]).filter(b=>b.done).length} / ${(t.batches||[]).length} 已完成</p><p>校验方式：大小与本地 SHA-256；存在上游 MD5 时进行额外校验。凭据、分享令牌和直链不显示。</p></details><p class="login-error" role="alert"></p><div class="dialog-foot">${btn('关闭','data-action="close"')}${t.uncertain?btn('核对已有结果',`data-job-action="reconcile" data-id="${t.id}" data-cloud-action disabled`,'primary')+btn('确认空目录，重新转存',`data-job-action="resubmit" data-id="${t.id}" data-cloud-action disabled`):t.status==='attention'?btn('重试',`data-job-action="retry" data-id="${t.id}"`,'primary'):''}</div>`;}
 if(m.type==='confirm')markup=dialogHead(m.title)+`<p class="sub">${esc(m.message)}</p><div class="dialog-foot">${btn('返回','data-action="close"')}${btn(m.label,`data-action="confirm-action"`,'primary')}</div>`;
 dialog.innerHTML=markup;
}
async function parseImport(form){const data=new FormData(form),m=state.modal;m.url=String(data.get('url')).trim();m.code=String(data.get('code')).trim();if(!state.account){closeModal();state.page='account';render();toast('请先保存夸克连接凭据，再解析分享。');return;}const submit=form.querySelector('[type=submit]');submit.disabled=true;submit.textContent='解析中…';try{const result=await api('preview',{url:m.url,code:m.code});if(state.modal!==m)return;for(const row of state.results){if(row.id===resultKey({url:m.url})){row.checked_at=result.checked_at;row.availability='accessible';}}renderSearch();state.modal={type:'preview',...result,url:m.url,code:m.code,title:m.title||result.files[0]?.file_name||'夸克分享',dir:state.settings.default_dir,error:''};openModal();}catch(e){if(state.modal===m){m.error=e.message;renderModal();}}}
async function openBrowser(context,path){directoryController?.abort();directoryController=new AbortController();state.browser={context,path};const slot=context==='settings'?$('#main [data-browser-slot]'):dialog.querySelector('[data-browser-slot]');if(!slot)return;slot.innerHTML='<div class="browser" role="status">正在读取目录…</div>';try{const data=await api('directories?path='+encodeURIComponent(path),undefined,directoryController.signal);if(!state.browser||state.browser.context!==context||state.browser.path!==path)return;slot.innerHTML=`<div class="browser"><div class="browser-header"><span class="path">${esc(path||'授权根目录')}</span>${btn('使用此目录','data-use-dir','primary')}</div><div class="browser-folders">${path?btn('↑ 上一级',`data-open-dir="${esc(path.split('/').slice(0,-1).join('/'))}"`):''}${data.directories.map(name=>btn(`目录 · ${esc(name)}`,`data-open-dir="${esc(path?path+'/'+name:name)}"`)).join('')}${!data.directories.length?'<span class="sub">没有子目录</span>':''}</div></div>`;}catch(e){if(e.name!=='AbortError')slot.innerHTML=`<p class="alert">${esc(e.message)}</p>`;}}
async function actTask(id,action){try{await api('tasks/'+id,{action});state.tasks=await api('tasks');if(state.page==='tasks')renderPage();if(state.modal?.type==='task')renderModal();toast(action==='cancel'?'任务已取消，云端和本地内容会保留。':'任务状态已更新。');}catch(e){const err=dialog.open?dialog.querySelector('.login-error'):null;if(err)err.textContent=e.message;else toast(e.message);}}
document.addEventListener('submit',async e=>{const f=e.target;if(!(f instanceof HTMLFormElement))return;e.preventDefault();
 if(f.id==='auth-form'){const data=new FormData(f),b=f.querySelector('button');b.disabled=true;try{await api(state.status.configured?'login':'setup',Object.fromEntries(data));await boot();}catch(er){f.querySelector('.login-error').textContent=er.message;b.disabled=false;}return;}
 if(f.id==='search-form'){const q=String(new FormData(f).get('query')).trim();if(q.includes('pan.quark.cn/s/')){importModal(q);return;}beginSearch(q);return;}
 if(f.id==='import-form'){await parseImport(f);return;}
 if(f.id==='account-form'){const b=f.querySelector('button');b.disabled=true;try{await api('account',{cookie:String(new FormData(f).get('cookie'))});state.account=true;f.reset();render();toast('连接凭据已加密保存，可以验证连接。');}catch(er){f.querySelector('.login-error').textContent=er.message;b.disabled=false;}return;}
 if(f.id==='settings-form'){const b=f.querySelector('button[type=submit]');b.disabled=true;try{const data=new FormData(f),settings={...state.settings,default_dir:state.settingsDir,concurrency:Number(data.get('concurrency')),chunk_mib:Number(data.get('chunk_mib')),search_priority_concurrency:Number(data.get('search_priority_concurrency')),search_channel_concurrency:Number(data.get('search_channel_concurrency'))};await api('settings',settings);state.settings=settings;toast('设置已保存。');}catch(er){f.querySelector('.login-error').textContent=er.message;}finally{b.disabled=false;}return;}
});
document.addEventListener('click',async e=>{const b=e.target.closest('button');if(!b||b.disabled)return;
 if(b.dataset.page){state.page=b.dataset.page;state.browser=null;render();if(state.page==='tasks')refreshTasks();return;}
 if(b.dataset.filter){state.filter=b.dataset.filter;renderPage();return;}
 if(b.hasAttribute('data-result')){const r=state.results.find(r=>r.id===b.dataset.result);if(!r)return;importModal(r.url,r.title);const f=$('#import-form');await parseImport(f);return;}
 if(b.dataset.task){state.modal={type:'task',id:b.dataset.task};state.browser=null;openModal();return;}
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
 if(action==='logout'){cancelSearch();try{await api('logout',{});state.status.authenticated=false;state.tasks=[];state.results=[];renderAuth();}catch(er){toast(er.message);}}
 if(action==='verify'){b.disabled=true;b.textContent='验证中…';try{const data=await api('account/verify',{});$('#verify-result').innerHTML=`<p class="summary">${esc(data.message)}</p>`;}catch(er){$('#verify-result').innerHTML=`<p class="alert error" role="alert">${esc(er.message)}</p>`;}finally{b.disabled=false;b.textContent='验证连接';}}
 if(action==='disconnect'){state.modal={type:'confirm',title:'断开夸克账号？',message:'新的转存下载将需要重新连接。正在执行的任务使用已有会话，若需停止，请先暂停任务。',label:'断开账号',perform:async()=>{await api('account',{cookie:''});state.account=false;render();toast('账号已断开。');}};openModal();}
 if(action==='confirm-action'){const perform=state.modal?.perform;b.disabled=true;try{await perform?.();closeModal();}catch(er){b.disabled=false;toast(er.message);}}
 if(action==='create-task'){const m=state.modal;b.disabled=true;b.textContent='正在创建…';try{await api('tasks',{url:m.url,code:m.code,title:m.title,directory:m.dir});closeModal();state.page='tasks';state.filter='全部';state.tasks=await api('tasks');render();toast('任务已创建。你可以继续搜索其他资源。');}catch(er){if(state.modal===m){m.error=er.message;renderModal();}}}
});
document.addEventListener('change',async e=>{const el=e.target;if(el.dataset.searchOption){const key={source:'searchSource',days:'searchDays',sort:'searchSort'}[el.dataset.searchOption];if(key){state[key]=key==='searchDays'?Number(el.value):el.value;renderSearch();}return;}if(el.hasAttribute('data-confirm-cloud')){dialog.querySelectorAll('[data-cloud-action]').forEach(b=>b.disabled=!el.checked);}
 if(el.hasAttribute('data-source')){const source=state.sourceList.find(s=>s.id===el.dataset.source),old=source.enabled;source.enabled=el.checked;el.disabled=true;try{const settings={...state.settings,sources:state.sourceList.filter(s=>s.enabled).map(s=>s.id)};await api('settings',settings);state.settings=settings;toast('来源设置已保存。');}catch(er){source.enabled=old;el.checked=old;toast(er.message);}finally{el.disabled=false;}}
});
dialog.addEventListener('cancel',()=>{state.modal=null;state.browser=null;directoryController?.abort();});
dialog.addEventListener('click',e=>{if(e.target===dialog){const r=dialog.getBoundingClientRect();if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom)closeModal();}});
function refreshTaskDialog(){
 if(!dialog.open||state.modal?.type!=='task')return;
 const current=state.tasks.find(t=>t.id===state.modal.id),signature=JSON.stringify(current);
 if(signature===state.modal.signature)return;
 state.modal.signature=signature;
 const active=document.activeElement,wasInside=dialog.contains(active),action=active?.dataset?.action,jobAction=active?.dataset?.jobAction,isSummary=active?.tagName==='SUMMARY',isCheckbox=active?.hasAttribute('data-confirm-cloud');
 const expanded=dialog.querySelector('details')?.open,confirmed=dialog.querySelector('[data-confirm-cloud]')?.checked;
 renderModal();
 const details=dialog.querySelector('details');if(details)details.open=!!expanded;
 const checkbox=dialog.querySelector('[data-confirm-cloud]');if(checkbox&&confirmed){checkbox.checked=true;dialog.querySelectorAll('[data-cloud-action]').forEach(b=>b.disabled=false);}
 if(wasInside){const next=action?dialog.querySelector(`[data-action="${action}"]`):jobAction?dialog.querySelector(`[data-job-action="${jobAction}"]`):isSummary?dialog.querySelector('summary'):isCheckbox?checkbox:null;next?.focus({preventScroll:true});}
}
async function refreshTasks(){if(polling||!state.status?.authenticated||state.page!=='tasks')return;polling=true;try{state.tasks=await api('tasks');if(state.page==='tasks'){if(dialog.open)refreshTaskDialog();else if(!$('#main').contains(document.activeElement))renderPage();else{const main=$('#main'),draft=document.createElement('div');draft.innerHTML=tasksPage();const existing=main.querySelector('[aria-live="polite"]'),updated=draft.querySelector('[aria-live="polite"]');if(existing&&updated){const active=document.activeElement,task=active?.dataset?.task,action=active?.dataset?.jobAction,job=active?.dataset?.id;existing.replaceWith(updated);main.querySelectorAll('[data-percent]').forEach(e=>e.style.width=e.dataset.percent+'%');const next=task?[...updated.querySelectorAll('[data-task]')].find(e=>e.dataset.task===task):action?[...updated.querySelectorAll('[data-job-action]')].find(e=>e.dataset.jobAction===action&&e.dataset.id===job):null;next?.focus({preventScroll:true});}}}}catch(e){toast(e.message);}finally{polling=false;}}
setInterval(()=>{if(!document.hidden)refreshTasks();},2500);
boot();
