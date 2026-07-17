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
		},
		Resources: []pluginapi.ResourceRoute{
			{Path: "/status", Menu: "Grok Quota", Description: "Local 2M/24h quota observation console."},
			{Path: "/data", Description: "Public quota snapshot JSON."},
			{Path: "/accounts", Description: "Public per-account quota JSON."},
			{Path: "/summary", Description: "Public quota summary JSON."},
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
		return jsonResponse(http.StatusOK, getSnapshot(false))
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
    :root{color-scheme:light;--bg:#f4f7fb;--card:#fff;--text:#152033;--muted:#66758a;--line:#d9e1ec;--blue:#175cd3;--green:#067647;--amber:#b54708;--red:#b42318}
    *{box-sizing:border-box}body{margin:0;font-family:"Segoe UI","PingFang SC","Microsoft YaHei",ui-sans-serif,system-ui,sans-serif;background:var(--bg);color:var(--text)}
    header{background:#101828;color:#fff;padding:18px 22px}header h1{margin:0;font-size:20px}header p{margin:6px 0 0;color:#b6c2d2;font-size:13px}
    main{max-width:1320px;margin:0 auto;padding:18px 20px 40px}
    .stats{display:grid;grid-template-columns:repeat(5,minmax(120px,1fr));gap:12px;margin-bottom:14px}
    .stat{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:14px}
    .stat b{display:block;font-size:12px;color:var(--muted);margin-bottom:6px}.stat span{font-size:22px;font-weight:750}
    .toolbar{display:flex;gap:10px;flex-wrap:wrap;margin-bottom:12px;align-items:center}
    input,button,select{height:36px;border:1px solid #c3cfdd;border-radius:8px;padding:0 12px;font:inherit}button{background:#fff;cursor:pointer;font-weight:600}button.primary{background:var(--blue);border-color:var(--blue);color:#fff}
    .note{color:var(--muted);font-size:12px;margin:0 0 12px;line-height:1.5}
    table{width:100%;border-collapse:collapse;background:var(--card);border:1px solid var(--line);border-radius:10px;overflow:hidden}
    th,td{padding:10px 12px;border-bottom:1px solid #edf2f7;text-align:left;font-size:13px;vertical-align:top}
    th{background:#f8fafc;color:#475467;font-size:12px;white-space:nowrap}code{font-family:ui-monospace,Consolas,monospace;font-size:12px}
    .bar{height:8px;background:#eef2f6;border-radius:999px;overflow:hidden;min-width:90px;margin-top:4px}.bar>i{display:block;height:100%;background:var(--blue)}
    .tag{display:inline-block;padding:2px 8px;border-radius:999px;font-size:12px;font-weight:700}
    .healthy{background:#ecfdf3;color:var(--green)}.cooldown{background:#fef3f2;color:var(--red)}.soft_exhausted{background:#fffaeb;color:var(--amber)}
    .msg{min-height:18px;color:var(--muted);font-size:13px}.err{color:var(--red)}
    .sub{color:var(--muted);font-size:12px;margin-top:2px}
    @media(max-width:1000px){.stats{grid-template-columns:1fr 1fr}}
  </style>
</head>
<body>
<header>
  <h1>Grok 额度 · 本地 2M / 24 小时观测</h1>
  <p>独立额度插件 v` + ver + ` · 中国时区（UTC+8）· 非 xAI 官方余额</p>
</header>
<main>
  <div class="stats">
    <div class="stat"><b>现网账号</b><span id="nAcc">-</span></div>
    <div class="stat"><b>冷却中</b><span id="nCool">-</span></div>
    <div class="stat"><b>本地已满</b><span id="nSoft">-</span></div>
    <div class="stat"><b>近24h已用</b><span id="used">-</span></div>
    <div class="stat"><b>池剩余</b><span id="avail">-</span></div>
  </div>
  <div class="toolbar">
    <input id="q" type="search" placeholder="搜索邮箱 / auth_index" style="min-width:260px;flex:1">
    <select id="hf">
      <option value="all">全部状态</option>
      <option value="healthy">正常</option>
      <option value="cooldown">冷却中</option>
      <option value="soft_exhausted">本地已满</option>
    </select>
    <button class="primary" onclick="load(true)">刷新</button>
    <label style="display:flex;gap:6px;align-items:center;color:#66758a"><input id="auto" type="checkbox" checked>30 秒自动刷新</label>
  </div>
  <p class="note" id="meta">加载中…</p>
  <div class="msg" id="msg"></div>
  <div style="overflow:auto">
    <table>
      <thead>
        <tr>
          <th>邮箱</th>
          <th>Auth</th>
          <th>近24h用量</th>
          <th>进度</th>
          <th>状态</th>
          <th>恢复时间（北京时间）</th>
          <th>原因</th>
        </tr>
      </thead>
      <tbody id="rows"></tbody>
    </table>
  </div>
</main>
<script>
const base='/v0/resource/plugins/grok-quota';
let rows=[], timer=null;
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
  return ({healthy:'正常',cooldown:'冷却中',soft_exhausted:'本地已满',unknown:'未知'}[String(h||'').toLowerCase()]||h||'未知');
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
  // Asia/Shanghai
  return new Intl.DateTimeFormat('zh-CN',{
    timeZone:'Asia/Shanghai', year:'numeric', month:'2-digit', day:'2-digit',
    hour:'2-digit', minute:'2-digit', second:'2-digit', hour12:false
  }).format(d).replace(/\//g,'-');
}
function pctBar(p){const w=Math.max(0,Math.min(100,Number(p||0)));return '<div class="bar"><i style="width:'+w.toFixed(1)+'%"></i></div>';}
function render(){
  const q=($('q').value||'').toLowerCase();
  const hf=$('hf').value||'all';
  const list=rows.filter(a=>{
    const health=String(a.health||'healthy');
    if(hf!=='all' && health!==hf) return false;
    if(!q) return true;
    return String(a.auth_index).toLowerCase().includes(q)
      || String(a.email||'').toLowerCase().includes(q)
      || String(a.auth_file||'').toLowerCase().includes(q)
      || String(a.reason_label||a.reason||'').toLowerCase().includes(q)
      || String(a.health_label||'').includes(q);
  });
  $('rows').innerHTML=list.map(a=>{
    const health=a.health||'healthy';
    const label=a.health_label || healthZH(health);
    const used=a.tokens_24h_m || toM(a.tokens_24h);
    const lim=a.limit_tokens_m || toM(a.limit_tokens||2000000);
    const recover=a.recover_at_cn || cnTime(a.recover_at);
    const reason=a.reason_label || reasonZH(a.reason) || '-';
    return '<tr>'
      +'<td>'+esc(a.email||'-')+'</td>'
      +'<td><code>'+esc(a.auth_index)+'</code></td>'
      +'<td><b>'+esc(used)+'</b> / '+esc(lim)+'<div class="sub">原始 '+fmtInt(a.tokens_24h)+' token</div></td>'
      +'<td>'+Number(a.pct||0).toFixed(1)+'%'+pctBar(a.pct)+'</td>'
      +'<td><span class="tag '+esc(health)+'">'+esc(label)+'</span></td>'
      +'<td>'+esc(recover)+'</td>'
      +'<td>'+esc(reason)+'</td>'
      +'</tr>';
  }).join('')||'<tr><td colspan="7" style="text-align:center;color:#66758a;padding:28px">没有匹配的账号</td></tr>';
}
async function load(force){
  try{
    $('msg').textContent=force?'正在强制刷新…':'同步中…';
    $('msg').className='msg';
    const res=await fetch(base+'/data',{cache:'no-store'});
    const data=await res.json();
    if(!res.ok) throw new Error(data.error||('HTTP '+res.status));
    rows=data.accounts||[];
    const s=data.summary||{};
    $('nAcc').textContent=fmtInt(s.account_count);
    $('nCool').textContent=fmtInt(s.cooldown_accounts);
    $('nSoft').textContent=fmtInt(s.soft_exhausted_accounts);
    $('used').textContent=pickM(s,'used_tokens_m','used_tokens');
    $('avail').textContent=pickM(s,'current_available_tokens_m','current_available_tokens');
    const when=data.computed_at_cn || cnTime(data.computed_at);
    const note=data.note || '本地观测额度，非 xAI 官方余额。';
    const mem=s.membership_source||'cpa_auth_dir';
    const dropped=s.dropped_historical_accounts!=null?s.dropped_historical_accounts:s.dropped_historical;
    $('meta').textContent='成员集：现网 auth-dir（'+mem+'）· 用量：CPAMP 近24h · 北京时间 · 统计：'+when
      +' · 上限：2.00M/账号'
      +(dropped?(' · 已丢弃历史账号 '+fmtInt(dropped)):'')
      +(data.error?(' · 错误：'+data.error):'')
      +' · '+note;
    $('msg').textContent='已更新 '+cnTime(new Date().toISOString());
    render();
  }catch(e){
    $('msg').textContent=String(e.message||e);
    $('msg').className='msg err';
  }
}
$('q').addEventListener('input',render);
$('hf').addEventListener('change',render);
function arm(){if(timer)clearInterval(timer);timer=$('auto').checked?setInterval(()=>load(false),30000):null}
$('auto').addEventListener('change',arm);arm();load(false);
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
