package main

import "html"

func statusPage() string {
	name := html.EscapeString(pluginName)
	ver := html.EscapeString(pluginVersion)
	return `<!doctype html>
<html lang="zh-CN" class="h-full">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>` + name + `</title>
  <script src="https://cdn.tailwindcss.com"></script>
  <script>
    tailwind.config = {
      theme: {
        extend: {
          colors: {
            ink: { DEFAULT: '#0f172a', muted: '#64748b', soft: '#94a3b8' },
            brand: { DEFAULT: '#0f766e', dark: '#134e4a', soft: '#ccfbf1' },
            line: { DEFAULT: '#e2e8f0', strong: '#cbd5e1' }
          },
          boxShadow: {
            card: '0 0.0625rem 0.125rem rgba(15,23,42,.04), 0 0.5rem 1.5rem rgba(15,23,42,.05)'
          }
        }
      }
    }
  </script>
  <style type="text/tailwindcss">
    @layer base {
      /* mobile root slightly smaller; md+ = 16px so 1rem = 16px on PC */
      html { font-size: 14px; }
      @media (min-width: 768px) {
        html { font-size: 16px; }
      }
      body {
        @apply m-0 min-h-full bg-slate-100 text-ink antialiased;
        font-family: "Segoe UI", "PingFang SC", "Microsoft YaHei", system-ui, sans-serif;
      }
    }
  </style>
</head>
<body class="h-full text-sm md:text-base">
  <div class="flex min-h-full w-full flex-col gap-3 p-3 sm:gap-4 sm:p-4 md:gap-4 md:p-6">
    <!-- hero: full width of host -->
    <header class="flex w-full flex-col gap-3 rounded-xl bg-gradient-to-br from-slate-900 via-brand-dark to-brand px-4 py-4 text-teal-50 shadow-card sm:flex-row sm:items-end sm:justify-between sm:px-5 sm:py-5">
      <div class="min-w-0">
        <h1 class="m-0 text-xl font-bold tracking-tight md:text-2xl">Grok 额度观测</h1>
        <p class="mt-1.5 text-xs text-teal-200/95 md:text-sm">近 24 小时滚动用量 · v` + ver + ` · 北京时间 · 2M 参考基线（非官方余额）</p>
      </div>
      <span id="updPill" class="inline-flex shrink-0 items-center rounded-full border border-white/15 bg-white/10 px-3 py-1.5 text-xs text-teal-50 md:text-sm">更新中…</span>
    </header>

    <!-- stats -->
    <section class="grid w-full grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6" aria-label="汇总">
      <div class="rounded-xl border border-line bg-white p-3 shadow-card sm:p-4">
        <span class="block text-xs font-semibold text-ink-muted">账号数</span>
        <span id="nAcc" class="mt-1 block text-xl font-extrabold tabular-nums tracking-tight md:text-2xl">-</span>
      </div>
      <div class="rounded-xl border border-line bg-white p-3 shadow-card sm:p-4">
        <span class="block text-xs font-semibold text-ink-muted">额度问题</span>
        <span id="nIssue" class="mt-1 block text-xl font-extrabold tabular-nums tracking-tight text-red-700 md:text-2xl">-</span>
      </div>
      <div class="rounded-xl border border-line bg-white p-3 shadow-card sm:p-4">
        <span class="block text-xs font-semibold text-ink-muted">建议停用</span>
        <span id="nSuggest" class="mt-1 block text-xl font-extrabold tabular-nums tracking-tight md:text-2xl">-</span>
      </div>
      <div class="rounded-xl border border-line bg-white p-3 shadow-card sm:p-4">
        <span class="block text-xs font-semibold text-ink-muted">高用量</span>
        <span id="nHigh" class="mt-1 block text-xl font-extrabold tabular-nums tracking-tight text-violet-700 md:text-2xl">-</span>
      </div>
      <div class="rounded-xl border border-line bg-white p-3 shadow-card sm:p-4">
        <span class="block text-xs font-semibold text-ink-muted">24h 合计</span>
        <span id="used" class="mt-1 block text-xl font-extrabold tabular-nums tracking-tight md:text-2xl">-</span>
      </div>
      <div class="rounded-xl border border-line bg-white p-3 shadow-card sm:p-4">
        <span class="block text-xs font-semibold text-ink-muted">已停用</span>
        <span id="nDis" class="mt-1 block text-xl font-extrabold tabular-nums tracking-tight md:text-2xl">-</span>
      </div>
    </section>

    <!-- main panel -->
    <section class="flex w-full min-h-0 flex-1 flex-col overflow-hidden rounded-xl border border-line bg-white shadow-card">
      <div class="flex w-full flex-col gap-2 border-b border-line p-3 sm:flex-row sm:flex-wrap sm:items-center sm:gap-3 sm:p-4">
        <input id="q" type="search" placeholder="搜索邮箱或备注" class="h-11 w-full min-w-0 flex-1 rounded-lg border border-line-strong bg-white px-3 text-sm outline-none ring-brand/30 focus:ring-2 md:h-10 md:text-base sm:min-w-[12rem]">
        <select id="st" title="账号状态" class="h-11 w-full rounded-lg border border-line-strong bg-white px-3 text-sm md:h-10 md:w-auto md:text-base">
          <option value="all">全部状态</option>
          <option value="active">正常</option>
          <option value="high_usage">高用量</option>
          <option value="quota_issue">额度问题</option>
          <option value="disabled">已停用</option>
        </select>
        <select id="xf" title="附加条件：在状态结果上再收窄" class="h-11 w-full rounded-lg border border-line-strong bg-white px-3 text-sm md:h-10 md:w-auto md:text-base">
          <option value="all">条件不限</option>
          <option value="suggest">只要建议停用</option>
          <option value="zero">只要零用量</option>
        </select>
        <select id="sort" title="排序" class="h-11 w-full rounded-lg border border-line-strong bg-white px-3 text-sm md:h-10 md:w-auto md:text-base">
          <option value="tokens_desc">用量从高到低</option>
          <option value="tokens_asc">用量从低到高</option>
          <option value="status">异常优先</option>
          <option value="email">按邮箱</option>
        </select>
        <select id="ps" title="每页条数" class="h-11 w-full rounded-lg border border-line-strong bg-white px-3 text-sm md:h-10 md:w-auto md:text-base">
          <option value="20">20 条/页</option>
          <option value="50" selected>50 条/页</option>
          <option value="100">100 条/页</option>
          <option value="200">200 条/页</option>
        </select>
        <button id="btnRefresh" type="button" class="h-11 shrink-0 rounded-lg bg-brand px-4 text-sm font-semibold text-white hover:brightness-105 md:h-10 md:text-base">刷新</button>
        <label class="inline-flex h-11 items-center gap-2 whitespace-nowrap text-xs text-ink-muted md:h-10 md:text-sm"><input id="auto" type="checkbox" checked class="h-4 w-4">自动刷新</label>
        <label class="inline-flex h-11 items-center gap-2 whitespace-nowrap text-xs text-ink-muted md:h-10 md:text-sm" title="仅日志出现额度错误码时停用"><input id="autoDis" type="checkbox" class="h-4 w-4">自动停用额度问题</label>
      </div>

      <p id="msg" class="min-h-[1.25rem] px-3 pb-2 text-xs text-ink-muted sm:px-4 md:text-sm"></p>

      <div class="w-full flex-1 overflow-x-auto">
        <table class="w-full min-w-[48rem] border-separate border-spacing-0 text-left">
          <colgroup>
            <col class="w-[16%]"><col class="w-[12%]"><col class="w-[14%]">
            <col class="w-[10%]"><col class="w-[12%]"><col class="w-[28%]"><col class="w-[8%]">
          </colgroup>
          <thead>
            <tr class="bg-slate-50 text-xs font-bold text-ink-muted md:text-sm">
              <th class="sticky top-0 z-10 border-b border-line bg-slate-50 px-3 py-3 sm:px-4">邮箱</th>
              <th class="sticky top-0 z-10 border-b border-line bg-slate-50 px-3 py-3 sm:px-4">用量</th>
              <th class="sticky top-0 z-10 border-b border-line bg-slate-50 px-3 py-3 sm:px-4">进度</th>
              <th class="sticky top-0 z-10 border-b border-line bg-slate-50 px-3 py-3 sm:px-4">状态</th>
              <th class="sticky top-0 z-10 border-b border-line bg-slate-50 px-3 py-3 sm:px-4">预计恢复</th>
              <th class="sticky top-0 z-10 border-b border-line bg-slate-50 px-3 py-3 sm:px-4">备注</th>
              <th class="sticky top-0 z-10 border-b border-line bg-slate-50 px-3 py-3 sm:px-4">操作</th>
            </tr>
          </thead>
          <tbody id="rows" class="text-sm"></tbody>
        </table>
      </div>

      <div class="flex w-full flex-col gap-2 border-t border-line bg-slate-50/80 px-3 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-4">
        <div class="flex items-center gap-2">
          <button id="prev" type="button" class="h-10 rounded-lg border border-line-strong bg-white px-3 text-sm font-semibold disabled:opacity-40">上一页</button>
          <span id="pageInfo" class="min-w-[6rem] text-center text-xs text-ink-muted md:text-sm">-</span>
          <button id="next" type="button" class="h-10 rounded-lg border border-line-strong bg-white px-3 text-sm font-semibold disabled:opacity-40">下一页</button>
        </div>
        <span class="text-xs text-ink-muted md:text-sm">点击邮箱可复制完整地址</span>
      </div>
    </section>
  </div>
<script>
const base='/v0/resource/plugins/grok-quota';
let rows=[], timer=null, page=1, settingsBusy=false;
const $=id=>document.getElementById(id);
const esc=s=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const fmtInt=n=>Number(n||0).toLocaleString('zh-CN');
function toM(n){
  const v=Number(n||0)/1e6;
  if(!Number.isFinite(v)) return '0.00M';
  return (Math.round(v*100)/100).toFixed(2)+'M';
}
function pickM(obj, mKey, rawKey){
  if(obj && obj[mKey]) return String(obj[mKey]);
  return toM(obj && obj[rawKey]);
}
function cnTime(iso){
  if(!iso) return '—';
  const d=new Date(iso);
  if(Number.isNaN(d.getTime())) return String(iso);
  return new Intl.DateTimeFormat('zh-CN',{
    timeZone:'Asia/Shanghai', year:'numeric', month:'2-digit', day:'2-digit',
    hour:'2-digit', minute:'2-digit', second:'2-digit', hour12:false
  }).format(d).replace(/\//g,'-');
}
function reasonZH(r){
  const s=String(r||'');
  const low=s.toLowerCase();
  const map=[
    ['subscription:free-usage-exhausted','免费额度用尽'],
    ['personal-team-blocked:spending-limit','个人团队消费限额'],
    ['free-usage-exhausted','免费额度用尽'],
    ['spending-limit','消费限额'],
    ['out of credits','积分不足'],
    ['insufficient credits','积分不足'],
    ['resource_exhausted','资源耗尽'],
    ['quota exceeded','额度超限'],
    ['quota_exceeded','额度超限'],
    ['quota_exhausted','额度耗尽']
  ];
  for(const [k,zh] of map){ if(low===k || low.includes(k)) return zh; }
  return s;
}
function statusOf(a){
  const k=String(a.status_kind||'');
  if(k) return k;
  if(a.auth_disabled || a.pool_status==='disabled') return 'disabled';
  if(a.health==='cooldown') return 'quota_issue';
  if(a.over_reference) return 'high_usage';
  return 'active';
}
function statusLabel(k,a){
  if(a && a.status_label) return a.status_label;
  return ({active:'正常',high_usage:'高用量',quota_issue:'额度问题',disabled:'已停用'}[k]||k||'未知');
}
function tagClass(kind){
  return ({
    active:'bg-emerald-100 text-emerald-800',
    high_usage:'bg-violet-100 text-violet-800',
    quota_issue:'bg-red-100 text-red-800',
    disabled:'bg-slate-100 text-slate-600'
  }[kind]||'bg-slate-100 text-slate-600');
}
function barClass(kind){
  if(kind==='quota_issue') return 'bg-red-600';
  if(kind==='high_usage') return 'bg-violet-600';
  return 'bg-blue-600';
}
function copyText(text){
  const t=String(text||'');
  if(!t) return;
  const done=()=>{ $('msg').textContent='已复制邮箱'; $('msg').className='min-h-[1.25rem] px-3 pb-2 text-xs text-ink-muted sm:px-4 md:text-sm'; };
  if(navigator.clipboard && navigator.clipboard.writeText){
    navigator.clipboard.writeText(t).then(done).catch(()=>fallbackCopy(t,done));
  } else fallbackCopy(t,done);
}
function fallbackCopy(t,done){
  const ta=document.createElement('textarea');
  ta.value=t; document.body.appendChild(ta); ta.select();
  try{ document.execCommand('copy'); done(); }
  catch(e){ $('msg').textContent='复制失败'; $('msg').className='min-h-[1.25rem] px-3 pb-2 text-xs text-red-600 sm:px-4 md:text-sm'; }
  document.body.removeChild(ta);
}
function usageCell(a){
  const tokens=Number(a.tokens_24h||0);
  const ref=Number(a.reference_tokens||2000000);
  const lim=Number(a.limit_tokens||Math.max(ref,tokens)||ref||1);
  const used=a.tokens_24h_m || toM(tokens);
  const limM=a.limit_tokens_m || toM(lim);
  return '<span class="whitespace-nowrap font-bold tabular-nums"><b>'+esc(used)+'</b><span class="mx-1 font-medium text-ink-muted">/</span>'+esc(limM)+'</span>';
}
function pctBar(a){
  const tokens=Number(a.tokens_24h||0);
  const ref=Number(a.reference_tokens||2000000);
  const lim=Number(a.limit_tokens||Math.max(ref,tokens)||ref||1);
  const st=statusOf(a);
  const denom=lim>0?lim:1;
  const pct=tokens/denom*100;
  const w=Math.max(0,Math.min(100,pct));
  return '<div class="text-xs tabular-nums text-ink-muted">'+Number(pct).toFixed(1)+'%</div>'
    +'<div class="mt-1.5 h-2 max-w-[10rem] overflow-hidden rounded-full bg-slate-100"><i class="block h-full rounded-full '+barClass(st)+'" style="width:'+w.toFixed(1)+'%"></i></div>';
}
function filtered(){
  const q=($('q').value||'').toLowerCase();
  const st=$('st').value||'all';
  const xf=$('xf').value||'all';
  let list=rows.filter(a=>{
    const kind=statusOf(a);
    if(st!=='all' && kind!==st) return false;
    if(xf==='suggest' && !a.suggest_disable) return false;
    if(xf==='zero' && Number(a.tokens_24h||0)!==0) return false;
    if(!q) return true;
    return String(a.email||'').toLowerCase().includes(q)
      || String(a.auth_index||'').toLowerCase().includes(q)
      || String(a.auth_file||'').toLowerCase().includes(q)
      || String(a.reason||a.reason_label||'').toLowerCase().includes(q)
      || String(a.remark||a.action_hint||'').toLowerCase().includes(q)
      || String(a.status_label||'').includes(q);
  });
  const sort=$('sort').value||'tokens_desc';
  const rank=k=>({quota_issue:0,disabled:1,high_usage:2,active:3}[k]??9);
  list=list.slice().sort((a,b)=>{
    if(sort==='tokens_asc') return Number(a.tokens_24h||0)-Number(b.tokens_24h||0);
    if(sort==='email') return String(a.email||'').localeCompare(String(b.email||''));
    if(sort==='status'){
      const d=rank(statusOf(a))-rank(statusOf(b));
      if(d!==0) return d;
      return Number(b.tokens_24h||0)-Number(a.tokens_24h||0);
    }
    return Number(b.tokens_24h||0)-Number(a.tokens_24h||0);
  });
  return list;
}
function render(){
  const list=filtered();
  const pageSize=Math.max(1, Number($('ps').value||50));
  const pages=Math.max(1, Math.ceil(list.length/pageSize));
  if(page>pages) page=pages;
  if(page<1) page=1;
  const start=(page-1)*pageSize;
  const slice=list.slice(start, start+pageSize);
  $('pageInfo').textContent=page+' / '+pages+' · '+fmtInt(list.length)+' 条';
  $('prev').disabled=page<=1;
  $('next').disabled=page>=pages;
  $('rows').innerHTML=slice.map(a=>{
    const kind=statusOf(a);
    const label=statusLabel(kind,a);
    const emailShow=a.email_masked || a.email || '—';
    const recover=a.recover_at_cn || cnTime(a.recover_at);
    const reason=a.reason_label || reasonZH(a.reason) || '';
    const remark=a.remark || a.action_hint || '';
    const note=remark || reason || '—';
    const act=(a.suggest_disable && a.auth_file && kind!=='disabled')
      ? '<button type="button" data-disable="'+esc(a.auth_file)+'" class="h-9 rounded-lg border border-red-200 bg-white px-3 text-xs font-semibold text-red-700 hover:bg-red-50 md:text-sm">停用</button>'
      : '<span class="text-ink-soft">—</span>';
    return '<tr class="hover:bg-slate-50">'
      +'<td class="border-b border-slate-100 px-3 py-3 sm:px-4"><span class="cursor-copy border-b border-dashed border-transparent hover:border-brand hover:text-brand" data-copy="'+esc(a.email||'')+'" title="点击复制完整邮箱">'+esc(emailShow)+'</span></td>'
      +'<td class="border-b border-slate-100 px-3 py-3 sm:px-4">'+usageCell(a)+'</td>'
      +'<td class="border-b border-slate-100 px-3 py-3 sm:px-4">'+pctBar(a)+'</td>'
      +'<td class="border-b border-slate-100 px-3 py-3 sm:px-4"><span class="inline-flex rounded-full px-2.5 py-1 text-xs font-bold '+tagClass(kind)+'">'+esc(label)+'</span></td>'
      +'<td class="border-b border-slate-100 px-3 py-3 tabular-nums sm:px-4">'+esc(recover)+'</td>'
      +'<td class="border-b border-slate-100 px-3 py-3 sm:px-4"><div class="line-clamp-2 text-xs leading-snug text-ink-muted md:text-sm" title="'+esc(note)+'">'+esc(note)+'</div></td>'
      +'<td class="border-b border-slate-100 px-3 py-3 sm:px-4">'+act+'</td>'
      +'</tr>';
  }).join('')||'<tr><td colspan="7" class="px-4 py-10 text-center text-ink-muted">没有匹配的账号</td></tr>';

  document.querySelectorAll('[data-copy]').forEach(el=>{
    el.onclick=()=>copyText(el.getAttribute('data-copy'));
  });
  document.querySelectorAll('button[data-disable]').forEach(btn=>{
    btn.onclick=async()=>{
      if(!confirm('确认停用该凭证？')) return;
      try{
        const res=await fetch(base+'/disable',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({auth_file:btn.getAttribute('data-disable'),reason:'manual_from_console'})});
        const data=await res.json();
        if(!res.ok||data.ok===false) throw new Error(data.error||('HTTP '+res.status));
        await load(true);
      }catch(e){ $('msg').textContent=String(e.message||e); $('msg').className='min-h-[1.25rem] px-3 pb-2 text-xs text-red-600 sm:px-4 md:text-sm'; }
    };
  });
}
async function loadSettingsUI(){
  try{
    const res=await fetch(base+'/settings',{cache:'no-store'});
    const data=await res.json();
    if(res.ok && data.settings){
      settingsBusy=true;
      $('autoDis').checked=!!data.settings.auto_disable_quota_exhausted;
      settingsBusy=false;
    }
  }catch(_){}
}
async function saveAutoDisable(){
  if(settingsBusy) return;
  try{
    settingsBusy=true;
    const res=await fetch(base+'/settings',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({auto_disable_quota_exhausted:!!$('autoDis').checked,updated_by:'console'})});
    const data=await res.json();
    if(!res.ok||data.ok===false) throw new Error(data.error||('HTTP '+res.status));
    $('msg').textContent='设置已保存';
    $('msg').className='min-h-[1.25rem] px-3 pb-2 text-xs text-ink-muted sm:px-4 md:text-sm';
    await load(true);
  }catch(e){
    $('msg').textContent=String(e.message||e);
    $('msg').className='min-h-[1.25rem] px-3 pb-2 text-xs text-red-600 sm:px-4 md:text-sm';
  }finally{ settingsBusy=false; }
}
async function load(force){
  try{
    $('msg').textContent=force?'刷新中…':'同步中…';
    $('msg').className='min-h-[1.25rem] px-3 pb-2 text-xs text-ink-muted sm:px-4 md:text-sm';
    const url=force?(base+'/data?force=1&_='+Date.now()):(base+'/data');
    const res=await fetch(url,{cache:'no-store'});
    const data=await res.json();
    if(!res.ok) throw new Error(data.error||('HTTP '+res.status));
    rows=data.accounts||[];
    const s=data.summary||{};
    $('nAcc').textContent=fmtInt(s.account_count);
    $('nIssue').textContent=fmtInt(s.quota_issue_accounts!=null?s.quota_issue_accounts:s.cooldown_accounts);
    $('nSuggest').textContent=fmtInt(s.suggest_disable_accounts);
    $('nHigh').textContent=fmtInt(s.high_usage_accounts);
    $('used').textContent=pickM(s,'used_tokens_m','used_tokens');
    $('nDis').textContent=fmtInt(s.disabled_accounts);
    if(typeof s.auto_disable_enabled==='boolean'){
      settingsBusy=true; $('autoDis').checked=!!s.auto_disable_enabled; settingsBusy=false;
    }
    const when=data.computed_at_cn || cnTime(data.computed_at);
    $('updPill').textContent='更新于 '+when;
    $('msg').textContent=data.error?('错误：'+data.error):'';
    render();
  }catch(e){
    $('msg').textContent=String(e.message||e);
    $('msg').className='min-h-[1.25rem] px-3 pb-2 text-xs text-red-600 sm:px-4 md:text-sm';
  }
}
['q','st','xf','sort','ps'].forEach(id=>{
  $(id).addEventListener('input',()=>{page=1;render();});
  $(id).addEventListener('change',()=>{page=1;render();});
});
$('prev').onclick=()=>{page--;render();};
$('next').onclick=()=>{page++;render();};
$('btnRefresh').onclick=()=>load(true);
$('autoDis').addEventListener('change',saveAutoDisable);
function arm(){if(timer)clearInterval(timer);timer=$('auto').checked?setInterval(()=>load(false),10000):null}
$('auto').addEventListener('change',arm);arm();
loadSettingsUI();load(false);
</script>
</body>
</html>`
}
