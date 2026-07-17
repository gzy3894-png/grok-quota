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
