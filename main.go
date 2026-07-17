package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct { void* ptr; size_t len; } cliproxy_buffer;
typedef struct { uint32_t abi_version; void* host_ctx; void* call; void* free_buffer; } cliproxy_host_api;
typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);
typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
*/
import "C"

import (
	"encoding/json"
	"html"
	"net/http"
	"strings"
	"unsafe"

	"grok-quota/cpasdk/pluginabi"
	"grok-quota/cpasdk/pluginapi"
)

const managementPrefix = "/plugins/" + pluginName

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(_ *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	startBackgroundRefresh()
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required"))
		return 1
	}
	var rawRequest []byte
	if request != nil && requestLen > 0 {
		rawRequest = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, err := handleMethod(C.GoString(method), rawRequest)
	if err != nil {
		writeResponse(response, errorEnvelope("plugin_error", err.Error()))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, _ C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	stopBackgroundRefresh()
}

func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		startBackgroundRefresh()
		return okEnvelope(pluginRegistration())
	case pluginabi.MethodManagementRegister:
		return okEnvelope(managementRegistration())
	case pluginabi.MethodManagementHandle:
		return handleManagement(request)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

type registration struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Metadata      pluginapi.Metadata     `json:"metadata"`
	Capabilities  registrationCapability `json:"capabilities"`
}

type registrationCapability struct {
	ManagementAPI bool `json:"management_api"`
}

func pluginRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             pluginName,
			Version:          pluginVersion,
			Author:           "gzy3894-png",
			// Host validPlugin requires a non-empty repository URL.
			GitHubRepository: "https://github.com/gzy3894-png/grok-quota",
			ConfigFields:     []pluginapi.ConfigField{},
		},
		Capabilities: registrationCapability{ManagementAPI: true},
	}
}

func managementRegistration() pluginapi.ManagementRegistrationResponse {
	return pluginapi.ManagementRegistrationResponse{
		Routes: []pluginapi.ManagementRoute{
			{Method: http.MethodGet, Path: managementPrefix + "/summary", Description: "Grok quota summary (rolling 24h)."},
			{Method: http.MethodGet, Path: managementPrefix + "/accounts", Description: "Per-account rolling 24h quota."},
			{Method: http.MethodPost, Path: managementPrefix + "/refresh", Description: "Force recompute rolling quota snapshot."},
			{Method: http.MethodGet, Path: managementPrefix + "/settings", Description: "Read plugin settings."},
			{Method: http.MethodPost, Path: managementPrefix + "/settings", Description: "Update plugin settings (auto-disable switch)."},
			{Method: http.MethodPost, Path: managementPrefix + "/disable", Description: "Manually disable one auth file (quota log evidence)."},
		},
		Resources: []pluginapi.ResourceRoute{
			{Path: "/status", Menu: "Grok Quota", Description: "Rolling 24h Grok usage log observer console."},
			{Path: "/data", Description: "Public quota snapshot JSON."},
			{Path: "/accounts", Description: "Public per-account quota JSON."},
			{Path: "/summary", Description: "Public quota summary JSON."},
			{Path: "/settings", Description: "Plugin settings JSON."},
		},
	}
}

func handleManagement(raw []byte) ([]byte, error) {
	var req pluginapi.ManagementRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	return okEnvelope(dispatchManagement(req))
}

func dispatchManagement(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	path := strings.TrimRight(strings.TrimSpace(req.Path), "/")
	// Normalize: host may pass full /v0/resource/... or short /plugins/... paths.
	leaf := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		leaf = path[i+1:]
	}

	switch {
	case method == http.MethodGet && (leaf == "status" || leaf == "ui"):
		// HTML console (resource /status). Do not treat this as JSON summary.
		return pluginapi.ManagementResponse{
			StatusCode: http.StatusOK,
			Headers:    http.Header{"Content-Type": {"text/html; charset=utf-8"}},
			Body:       []byte(statusPage()),
		}
	case method == http.MethodGet && leaf == "summary":
		snap := getSnapshot(false)
		return jsonResponse(http.StatusOK, map[string]any{
			"plugin":   snap.Plugin,
			"version":  snap.Version,
			"summary":  snap.Summary,
			"as_of_ms": snap.AsOfMS,
			"source":   snap.Source,
			"note":     snap.Note,
			"error":    snap.Error,
			"db_path":  snap.DBPath,
		})
	case method == http.MethodGet && leaf == "accounts":
		snap := getSnapshot(false)
		return jsonResponse(http.StatusOK, map[string]any{
			"plugin":      snap.Plugin,
			"version":     snap.Version,
			"computed_at": snap.ComputedAt,
			"as_of_ms":    snap.AsOfMS,
			"summary":     snap.Summary,
			"accounts":    snap.Accounts,
			"error":       snap.Error,
		})
	case method == http.MethodPost && leaf == "refresh":
		snap := getSnapshot(true)
		return jsonResponse(http.StatusOK, map[string]any{"ok": true, "summary": snap.Summary, "as_of_ms": snap.AsOfMS, "error": snap.Error})
	case method == http.MethodGet && leaf == "data":
		force := false
		if req.Query != nil {
			v := strings.TrimSpace(req.Query.Get("force"))
			force = v == "1" || strings.EqualFold(v, "true")
		}
		return jsonResponse(http.StatusOK, getSnapshot(force))
	case method == http.MethodGet && leaf == "settings":
		return jsonResponse(http.StatusOK, map[string]any{
			"plugin":   pluginName,
			"version":  pluginVersion,
			"settings": loadSettings(),
			"path":     settingsPath(),
		})
	case method == http.MethodPost && leaf == "settings":
		var body struct {
			AutoDisableQuotaExhausted *bool  `json:"auto_disable_quota_exhausted"`
			UpdatedBy                 string `json:"updated_by"`
		}
		if len(req.Body) > 0 {
			_ = json.Unmarshal(req.Body, &body)
		}
		s := loadSettings()
		if body.AutoDisableQuotaExhausted != nil {
			s.AutoDisableQuotaExhausted = *body.AutoDisableQuotaExhausted
		}
		if strings.TrimSpace(body.UpdatedBy) != "" {
			s.UpdatedBy = body.UpdatedBy
		} else {
			s.UpdatedBy = "api"
		}
		if err := saveSettings(s); err != nil {
			return jsonResponse(http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		}
		// Recompute so auto-disable can apply immediately when turned on.
		snap := getSnapshot(true)
		return jsonResponse(http.StatusOK, map[string]any{
			"ok":       true,
			"settings": s,
			"summary":  snap.Summary,
		})
	case method == http.MethodPost && leaf == "disable":
		var body struct {
			AuthFile string `json:"auth_file"`
			Reason   string `json:"reason"`
		}
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return jsonResponse(http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_json"})
		}
		authDir := detectAuthDir()
		reason := strings.TrimSpace(body.Reason)
		if reason == "" {
			reason = "manual_disable_from_grok_quota"
		}
		if err := setAuthFileDisabled(authDir, body.AuthFile, true, reason); err != nil {
			return jsonResponse(http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		}
		snap := getSnapshot(true)
		return jsonResponse(http.StatusOK, map[string]any{"ok": true, "auth_file": body.AuthFile, "summary": snap.Summary})
	default:
		return jsonResponse(http.StatusNotFound, map[string]any{"error": "not_found", "path": req.Path})
	}
}

func statusPage() string {
	name := html.EscapeString(pluginName)
	ver := html.EscapeString(pluginVersion)
	return `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>` + name + `</title>
  <style>
    :root{color-scheme:light;--bg:#f4f7fb;--card:#fff;--text:#152033;--muted:#66758a;--line:#d9e1ec;--blue:#175cd3;--green:#067647;--amber:#b54708;--red:#b42318;--over:#7a5af8}
    *{box-sizing:border-box}body{margin:0;font-family:"Segoe UI","PingFang SC","Microsoft YaHei",ui-sans-serif,system-ui,sans-serif;background:var(--bg);color:var(--text)}
    header{background:#101828;color:#fff;padding:18px 22px}header h1{margin:0;font-size:20px}header p{margin:6px 0 0;color:#b6c2d2;font-size:13px}
    main{max-width:1480px;margin:0 auto;padding:18px 20px 40px}
    .stats{display:grid;grid-template-columns:repeat(6,minmax(110px,1fr));gap:12px;margin-bottom:14px}
    .stat{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:14px}
    .stat b{display:block;font-size:12px;color:var(--muted);margin-bottom:6px}.stat span{font-size:22px;font-weight:750}
    .toolbar,.pager{display:flex;gap:10px;flex-wrap:wrap;margin-bottom:12px;align-items:center}
    input,button,select{height:36px;border:1px solid #c3cfdd;border-radius:8px;padding:0 12px;font:inherit}button{background:#fff;cursor:pointer;font-weight:600}button.primary{background:var(--blue);border-color:var(--blue);color:#fff}button.danger{background:#fff;border-color:#fda29b;color:var(--red)}button:disabled{opacity:.5;cursor:not-allowed}
    .meta{display:grid;grid-template-columns:repeat(auto-fit,minmax(240px,1fr));gap:8px;margin:0 0 12px}
    .meta .chip{background:var(--card);border:1px solid var(--line);border-radius:8px;padding:8px 10px;color:var(--muted);font-size:12px;line-height:1.45}
    table{width:100%;border-collapse:collapse;background:var(--card);border:1px solid var(--line);border-radius:10px;overflow:hidden}
    th,td{padding:10px 12px;border-bottom:1px solid #edf2f7;text-align:left;font-size:13px;vertical-align:top}
    th{background:#f8fafc;color:#475467;font-size:12px;white-space:nowrap}code{font-family:ui-monospace,Consolas,monospace;font-size:12px}
    .bar{height:8px;background:#eef2f6;border-radius:999px;overflow:hidden;min-width:110px;margin-top:4px;position:relative}
    .bar>i{display:block;height:100%;background:var(--blue);max-width:100%}
    .bar.over>i{background:var(--over)}
    .bar.bad>i{background:var(--red)}
    .tag{display:inline-block;padding:2px 8px;border-radius:999px;font-size:12px;font-weight:700}
    .healthy{background:#ecfdf3;color:var(--green)}.cooldown{background:#fef3f2;color:var(--red)}.over{background:#f4f3ff;color:var(--over)}
    .msg{min-height:18px;color:var(--muted);font-size:13px}.err{color:var(--red)}
    .sub{color:var(--muted);font-size:12px;margin-top:2px}
    .switch{display:flex;gap:8px;align-items:center;background:var(--card);border:1px solid var(--line);border-radius:8px;padding:6px 12px;font-size:13px}
    @media(max-width:1100px){.stats{grid-template-columns:1fr 1fr 1fr}}
  </style>
</head>
<body>
<header>
  <h1>Grok 额度 · 近 24 小时日志观测</h1>
  <p>插件 v` + ver + ` · 北京时间 · 2M 仅为参考基线 · 非 xAI 官方余额</p>
</header>
<main>
  <div class="stats">
    <div class="stat"><b>现网账号</b><span id="nAcc">-</span></div>
    <div class="stat"><b>额度问题（日志）</b><span id="nIssue">-</span></div>
    <div class="stat"><b>建议停用</b><span id="nSuggest">-</span></div>
    <div class="stat"><b>高用量(>参考)</b><span id="nHigh">-</span></div>
    <div class="stat"><b>近24h合计</b><span id="used">-</span></div>
    <div class="stat"><b>已停用</b><span id="nDis">-</span></div>
  </div>
  <div class="toolbar">
    <input id="q" type="search" placeholder="搜索邮箱 / auth_index / 文件名 / 原因" style="min-width:240px;flex:1">
    <select id="hf">
      <option value="all">状态：全部</option>
      <option value="healthy">状态：正常</option>
      <option value="cooldown">状态：额度问题</option>
    </select>
    <select id="pf">
      <option value="all">池：全部</option>
      <option value="active">池：启用中</option>
      <option value="disabled">池：已停用</option>
    </select>
    <select id="sf">
      <option value="all">建议：全部</option>
      <option value="suggest">仅建议停用</option>
      <option value="high">仅高用量</option>
      <option value="zero">仅零用量</option>
    </select>
    <select id="minu">
      <option value="0">用量：不限</option>
      <option value="500000">用量 ≥ 0.5M</option>
      <option value="1000000">用量 ≥ 1M</option>
      <option value="2000000">用量 ≥ 2M</option>
      <option value="3000000">用量 ≥ 3M</option>
    </select>
    <select id="sort">
      <option value="tokens_desc">排序：用量↓</option>
      <option value="tokens_asc">排序：用量↑</option>
      <option value="email">排序：邮箱</option>
      <option value="issue">排序：额度问题优先</option>
    </select>
    <select id="ps">
      <option value="20">每页 20</option>
      <option value="50" selected>每页 50</option>
      <option value="100">每页 100</option>
      <option value="200">每页 200</option>
    </select>
    <button class="primary" id="btnRefresh">刷新</button>
    <label class="switch"><input id="auto" type="checkbox" checked>10 秒自动刷新</label>
    <label class="switch" title="仅当 usage_events 出现额度错误码时才停用"><input id="autoDis" type="checkbox">自动停用额度问题账号</label>
  </div>
  <div class="meta" id="meta"><div class="chip">加载中…</div></div>
  <div class="msg" id="msg"></div>
  <div class="pager">
    <button id="prev">上一页</button>
    <span id="pageInfo" class="sub">-</span>
    <button id="next">下一页</button>
  </div>
  <div style="overflow:auto">
    <table>
      <thead>
        <tr>
          <th>邮箱</th>
          <th>Auth / 文件</th>
          <th>近24h 真实用量</th>
          <th>进度（相对动态上限）</th>
          <th>状态</th>
          <th>恢复时间</th>
          <th>原因 / 建议</th>
          <th>操作</th>
        </tr>
      </thead>
      <tbody id="rows"></tbody>
    </table>
  </div>
</main>
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
function healthZH(h){
  return ({healthy:'正常',cooldown:'额度问题',quota_issue:'额度问题',soft_exhausted:'高用量(旧)',unknown:'未知'}[String(h||'').toLowerCase()]||h||'未知');
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
    ['resource exhausted','资源耗尽'],
    ['quota exceeded','额度超限'],
    ['quota_exceeded','额度超限'],
    ['quota_exhausted','额度耗尽']
  ];
  for(const [k,zh] of map){ if(low===k || low.includes(k)) return zh; }
  return s;
}
function cnTime(iso){
  if(!iso) return '-';
  const d=new Date(iso);
  if(Number.isNaN(d.getTime())) return String(iso);
  return new Intl.DateTimeFormat('zh-CN',{
    timeZone:'Asia/Shanghai', year:'numeric', month:'2-digit', day:'2-digit',
    hour:'2-digit', minute:'2-digit', second:'2-digit', hour12:false
  }).format(d).replace(/\//g,'-');
}
function pctBar(a){
  const tokens=Number(a.tokens_24h||0);
  const ref=Number(a.reference_tokens||2000000);
  const lim=Number(a.limit_tokens||Math.max(ref,tokens)||1);
  // Bar fills by real usage / dynamic limit (limit grows with usage).
  const pct=lim>0?(tokens/lim*100):0;
  const w=Math.max(0,Math.min(100,pct));
  const pref=ref>0?(tokens/ref*100):0;
  let cls='bar';
  if(a.health==='cooldown') cls+=' bad';
  else if(pref>100) cls+=' over';
  return Number(pref).toFixed(1)+'% 相对参考 · 动态 '+Number(pct).toFixed(1)+'%'
    +'<div class="'+cls+'"><i style="width:'+w.toFixed(1)+'%"></i></div>'
    +'<div class="sub">真实 '+fmtInt(tokens)+' token · 动态上限 '+toM(lim)+' · 参考 '+toM(ref)+'</div>';
}
function filtered(){
  const q=($('q').value||'').toLowerCase();
  const hf=$('hf').value||'all';
  const pf=$('pf').value||'all';
  const sf=$('sf').value||'all';
  const minU=Number($('minu').value||0);
  let list=rows.filter(a=>{
    const health=String(a.health||'healthy');
    if(hf!=='all' && health!==hf) return false;
    const pool=String(a.pool_status|| (a.auth_disabled?'disabled':(a.in_pool?'active':'')));
    if(pf!=='all' && pool!==pf) return false;
    if(sf==='suggest' && !a.suggest_disable) return false;
    if(sf==='high' && !a.over_reference) return false;
    if(sf==='zero' && Number(a.tokens_24h||0)!==0) return false;
    if(Number(a.tokens_24h||0)<minU) return false;
    if(!q) return true;
    return String(a.auth_index||'').toLowerCase().includes(q)
      || String(a.email||'').toLowerCase().includes(q)
      || String(a.auth_file||'').toLowerCase().includes(q)
      || String(a.reason_label||a.reason||'').toLowerCase().includes(q)
      || String(a.health_label||a.action_hint||'').toLowerCase().includes(q);
  });
  const sort=$('sort').value||'tokens_desc';
  list=list.slice().sort((a,b)=>{
    if(sort==='tokens_asc') return Number(a.tokens_24h||0)-Number(b.tokens_24h||0);
    if(sort==='email') return String(a.email||'').localeCompare(String(b.email||''));
    if(sort==='issue'){
      const ai=a.health==='cooldown'?0:1, bi=b.health==='cooldown'?0:1;
      if(ai!==bi) return ai-bi;
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
  $('pageInfo').textContent='第 '+page+' / '+pages+' 页 · 筛选 '+fmtInt(list.length)+' / 全部 '+fmtInt(rows.length);
  $('prev').disabled=page<=1;
  $('next').disabled=page>=pages;
  $('rows').innerHTML=slice.map(a=>{
    const health=a.health||'healthy';
    const label=a.health_label || healthZH(health);
    const used=a.tokens_24h_m || toM(a.tokens_24h);
    const lim=a.limit_tokens_m || toM(a.limit_tokens||a.tokens_24h||2000000);
    const recover=a.recover_at_cn || cnTime(a.recover_at);
    const reason=a.reason_label || reasonZH(a.reason) || '-';
    const hint=a.action_hint || (a.suggest_disable?'日志含额度错误，建议停用':'');
    const tags=(a.over_reference&&health!=='cooldown'?' <span class="tag over">高于参考</span>':'')
      +(a.suggest_disable?' <span class="tag cooldown">建议停用</span>':'')
      +(a.auth_disabled?' <span class="tag">已停用</span>':'');
    const act=a.suggest_disable && a.auth_file
      ? '<button class="danger" data-disable="'+esc(a.auth_file)+'">停用</button>'
      : '-';
    return '<tr>'
      +'<td>'+esc(a.email||'-')+tags+'</td>'
      +'<td><code>'+esc(a.auth_index)+'</code><div class="sub">'+esc(a.auth_file||'')+'</div></td>'
      +'<td><b>'+esc(used)+'</b><div class="sub">动态上限 '+esc(lim)+(a.limit_mode?(' · '+esc(a.limit_mode)):'')+'</div></td>'
      +'<td>'+pctBar(a)+'</td>'
      +'<td><span class="tag '+esc(health==='cooldown'?'cooldown':'healthy')+'">'+esc(label)+'</span></td>'
      +'<td>'+esc(recover)+'</td>'
      +'<td>'+esc(reason)+(hint?('<div class="sub">'+esc(hint)+'</div>'):'')+'</td>'
      +'<td>'+act+'</td>'
      +'</tr>';
  }).join('')||'<tr><td colspan="8" style="text-align:center;color:#66758a;padding:28px">没有匹配的账号</td></tr>';
  document.querySelectorAll('button[data-disable]').forEach(btn=>{
    btn.onclick=async()=>{
      if(!confirm('确认停用 '+btn.getAttribute('data-disable')+' ?\\n仅应在日志已证明额度问题时操作。')) return;
      try{
        const res=await fetch(base+'/disable',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({auth_file:btn.getAttribute('data-disable'),reason:'manual_from_console'})});
        const data=await res.json();
        if(!res.ok||data.ok===false) throw new Error(data.error||('HTTP '+res.status));
        await load(true);
      }catch(e){ $('msg').textContent=String(e.message||e); $('msg').className='msg err'; }
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
    $('msg').textContent='设置已保存：自动停用='+($('autoDis').checked?'开':'关');
    $('msg').className='msg';
    await load(true);
  }catch(e){
    $('msg').textContent=String(e.message||e);
    $('msg').className='msg err';
  }finally{ settingsBusy=false; }
}
async function load(force){
  try{
    $('msg').textContent=force?'正在强制刷新…':'同步中…';
    $('msg').className='msg';
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
    const lines=s.meta_lines && s.meta_lines.length? s.meta_lines : [
      '成员：现网 auth-dir（'+(s.membership_source||'cpa_auth_dir')+'）',
      '用量：CPAMP usage_events · 近 24h',
      '时区：北京时间（UTC+8）',
      '统计：'+(data.computed_at_cn || cnTime(data.computed_at)),
      '参考基线：2.00M/账号（非硬上限）'
    ];
    $('meta').innerHTML=lines.map(t=>'<div class="chip">'+esc(t)+'</div>').join('');
    if(typeof s.auto_disable_enabled==='boolean'){
      settingsBusy=true; $('autoDis').checked=!!s.auto_disable_enabled; settingsBusy=false;
    }
    $('msg').textContent='已更新 '+cnTime(new Date().toISOString())+(data.error?(' · '+data.error):'');
    render();
  }catch(e){
    $('msg').textContent=String(e.message||e);
    $('msg').className='msg err';
  }
}
['q','hf','pf','sf','minu','sort','ps'].forEach(id=>{
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

func jsonResponse(status int, value any) pluginapi.ManagementResponse {
	raw, _ := json.MarshalIndent(value, "", "  ")
	return pluginapi.ManagementResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": {"application/json; charset=utf-8"}},
		Body:       raw,
	}
}

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func okEnvelope(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{OK: true, Result: raw})
}

func errorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return raw
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil {
		return
	}
	if len(raw) == 0 {
		response.ptr = nil
		response.len = 0
		return
	}
	ptr := C.CBytes(raw)
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}
