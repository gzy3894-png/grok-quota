package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// pluginSettings controls operator-facing optional behavior.
// Default is observe-only: never write auth disabled unless the user turns
// auto-disable on (or calls the manual disable API).
type pluginSettings struct {
	AutoDisableQuotaExhausted bool   `json:"auto_disable_quota_exhausted"`
	UpdatedAt                 string `json:"updated_at,omitempty"`
	UpdatedBy                 string `json:"updated_by,omitempty"`
}

var (
	settingsMu   sync.RWMutex
	cachedSettings pluginSettings
	settingsLoaded bool
)

func defaultSettings() pluginSettings {
	return pluginSettings{
		AutoDisableQuotaExhausted: false,
	}
}

func settingsPath() string {
	if v := strings.TrimSpace(os.Getenv("GROK_QUOTA_SETTINGS_PATH")); v != "" {
		return v
	}
	// Prefer next to state file when possible.
	state := defaultStatePath()
	if dir := filepath.Dir(state); dir != "" && dir != "." {
		return filepath.Join(dir, "grok-quota-settings.json")
	}
	return filepath.Join("plugins", "grok-quota-settings.json")
}

func loadSettings() pluginSettings {
	settingsMu.RLock()
	if settingsLoaded {
		s := cachedSettings
		settingsMu.RUnlock()
		return s
	}
	settingsMu.RUnlock()

	settingsMu.Lock()
	defer settingsMu.Unlock()
	if settingsLoaded {
		return cachedSettings
	}
	s := defaultSettings()
	path := settingsPath()
	raw, err := os.ReadFile(path)
	if err == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &s)
	}
	// Env override (highest priority for operators / containers).
	if v := strings.TrimSpace(os.Getenv("GROK_QUOTA_AUTO_DISABLE")); v != "" {
		s.AutoDisableQuotaExhausted = parseBoolish(v)
	}
	cachedSettings = s
	settingsLoaded = true
	return cachedSettings
}

func saveSettings(s pluginSettings) error {
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if strings.TrimSpace(s.UpdatedBy) == "" {
		s.UpdatedBy = "console"
	}
	path := settingsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return err
	}
	settingsMu.Lock()
	cachedSettings = s
	settingsLoaded = true
	settingsMu.Unlock()
	return nil
}

func parseBoolish(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "enable", "enabled":
		return true
	default:
		return false
	}
}
