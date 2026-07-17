package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type disableResult struct {
	AuthFile string `json:"auth_file"`
	Email    string `json:"email,omitempty"`
	OK       bool   `json:"ok"`
	Skipped  bool   `json:"skipped,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Error    string `json:"error,omitempty"`
}

// setAuthFileDisabled toggles disabled on a CPA auth JSON file.
// Only mutates the disabled flag and lightweight audit fields — never tokens.
func setAuthFileDisabled(authDir, authFile string, disabled bool, reason string) error {
	authDir = strings.TrimSpace(authDir)
	authFile = filepath.Base(strings.TrimSpace(authFile))
	if authDir == "" || authFile == "" || authFile == "." || authFile == string(filepath.Separator) {
		return fmt.Errorf("auth path incomplete")
	}
	path := filepath.Join(authDir, authFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	if doc == nil {
		doc = map[string]any{}
	}
	doc["disabled"] = disabled
	if disabled {
		doc["quota_disabled_by"] = pluginName
		doc["quota_disable_reason"] = strings.TrimSpace(reason)
		doc["quota_disabled_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	} else {
		// Leave audit fields for history; only clear active disable.
		delete(doc, "quota_disable_reason")
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// applyAutoDisableQuotaIssues disables live pool accounts that have
// log-proven quota exhaustion and are not already disabled.
// Returns per-file results; safe no-op when setting is off.
func applyAutoDisableQuotaIssues(snap *quotaSnapshot) []disableResult {
	if snap == nil {
		return nil
	}
	cfg := loadSettings()
	snap.Summary.AutoDisableEnabled = cfg.AutoDisableQuotaExhausted
	if !cfg.AutoDisableQuotaExhausted {
		return nil
	}
	authDir := strings.TrimSpace(snap.Summary.AuthDir)
	if authDir == "" {
		authDir = detectAuthDir()
	}
	results := make([]disableResult, 0)
	for i := range snap.Accounts {
		a := &snap.Accounts[i]
		if !a.SuggestDisable || a.AuthDisabled || !a.InPool {
			continue
		}
		if strings.TrimSpace(a.AuthFile) == "" {
			results = append(results, disableResult{
				Email:  a.Email,
				OK:     false,
				Reason: "missing auth_file",
				Error:  "no auth file name",
			})
			continue
		}
		reason := a.Reason
		if reason == "" {
			reason = "quota_exhausted_from_usage_events"
		}
		err := setAuthFileDisabled(authDir, a.AuthFile, true, reason)
		if err != nil {
			results = append(results, disableResult{
				AuthFile: a.AuthFile,
				Email:    a.Email,
				OK:       false,
				Error:    err.Error(),
			})
			continue
		}
		a.AuthDisabled = true
		a.PoolStatus = "disabled"
		a.SuggestDisable = false
		a.ActionHint = "已自动停用（日志额度证据）"
		results = append(results, disableResult{
			AuthFile: a.AuthFile,
			Email:    a.Email,
			OK:       true,
			Reason:   reason,
		})
		snap.Summary.AutoDisabledNow++
	}
	// Recount disabled/active after mutations.
	snap.Summary.DisabledAccounts = 0
	snap.Summary.ActiveAccounts = 0
	snap.Summary.SuggestDisableAccounts = 0
	for _, a := range snap.Accounts {
		if a.AuthDisabled {
			snap.Summary.DisabledAccounts++
		} else if a.InPool {
			snap.Summary.ActiveAccounts++
		}
		if a.SuggestDisable {
			snap.Summary.SuggestDisableAccounts++
		}
	}
	return results
}
