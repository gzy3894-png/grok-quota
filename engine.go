package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	defaultLimitTokens int64 = 2_000_000
	defaultWindow            = 24 * time.Hour
	xaiProvider              = "xai"
	sourceRolling            = "cpamp_usage_events_rolling_24h"
)

var (
	quotaCodeRE = regexp.MustCompile(`(?i)(free-usage-exhausted|spending-limit|out of credits|personal-team-blocked:spending-limit|resource[_ ]?exhausted|insufficient.?credits|quota exceeded|quota_exceeded)`)
	notQuotaRE  = regexp.MustCompile(`(?i)(permission-denied|service.?capacity|overloaded|deactivated_workspace|invalid.?api.?key|unauthorized)`)
	// Plain rate-limit without free-usage markers is not quota exhaustion.
	plainRateLimitRE = regexp.MustCompile(`(?i)rate[\s_-]?limit`)
)

type accountQuota struct {
	AuthIndex     string  `json:"auth_index"`
	Email         string  `json:"email,omitempty"`
	AuthFile      string  `json:"auth_file,omitempty"`
	Tokens24h     int64   `json:"tokens_24h"`
	Tokens24hM    string  `json:"tokens_24h_m"`
	Success24h    int64   `json:"success_24h"`
	Failed24h     int64   `json:"failed_24h"`
	LimitTokens   int64   `json:"limit_tokens"`
	LimitTokensM  string  `json:"limit_tokens_m"`
	Remaining     int64   `json:"remaining_tokens"`
	RemainingM    string  `json:"remaining_tokens_m"`
	Pct           float64 `json:"pct"`
	Window        string  `json:"window"`
	Health        string  `json:"health"`
	HealthLabel   string  `json:"health_label"`
	Reason        string  `json:"reason,omitempty"`
	ReasonLabel   string  `json:"reason_label,omitempty"`
	FailureAt     string  `json:"failure_at,omitempty"`
	FailureAtCN   string  `json:"failure_at_cn,omitempty"`
	RecoverAt     string  `json:"recover_at,omitempty"`
	RecoverAtCN   string  `json:"recover_at_cn,omitempty"`
	StatusCode    int     `json:"status_code,omitempty"`
	Source        string  `json:"source"`
	LastUsageAt   string  `json:"last_usage_at,omitempty"`
	LastUsageAtCN string  `json:"last_usage_at_cn,omitempty"`
	SoftExhausted bool    `json:"soft_exhausted"`
	SchedulerOwner   string `json:"scheduler_owner,omitempty"`
	GlobalStatusKind string `json:"global_status_kind,omitempty"`

	// Live pool membership (from CPA auth-dir files, not historical usage_events).
	InPool        bool   `json:"in_pool"`
	AuthDisabled  bool   `json:"auth_disabled"`
	PoolStatus    string `json:"pool_status,omitempty"` // active|disabled|missing
	MembershipKey string `json:"membership_key,omitempty"`

	// Panel-facing aliases (Codex contract). Display consumers should prefer these.
	QuotaLimit       int64  `json:"quota_limit"`
	QuotaUsed        int64  `json:"quota_used"`
	QuotaRemaining   int64  `json:"quota_remaining"`
	QuotaHealth      string `json:"quota_health"`
	QuotaWindowStart string `json:"quota_window_start,omitempty"`
	QuotaFailureAt   string `json:"quota_failure_at,omitempty"`
	CooldownUntil     string `json:"cooldown_until,omitempty"`
	QuotaReason      string `json:"quota_reason,omitempty"`
}

type quotaSnapshot struct {
	Plugin         string         `json:"plugin"`
	Version        string         `json:"version"`
	ComputedAt     string         `json:"computed_at"`
	ComputedAtCN   string         `json:"computed_at_cn"`
	Timezone       string         `json:"timezone"`
	AsOfMS         int64          `json:"as_of_ms"`
	WindowHours    int            `json:"window_hours"`
	LimitTokens    int64          `json:"limit_tokens"`
	DBPath         string         `json:"db_path"`
	Source         string         `json:"source"`
	Note           string         `json:"note"`
	Summary        quotaSummary   `json:"summary"`
	Accounts       []accountQuota `json:"accounts"`
	ByAuthIndex    map[string]accountQuota `json:"by_auth_index,omitempty"`
	SchedulerSync  map[string]any `json:"scheduler_sync,omitempty"`
	Error          string         `json:"error,omitempty"`
}

type quotaSummary struct {
	AccountCount            int    `json:"account_count"`
	ActiveAccounts          int    `json:"active_accounts"`
	DisabledAccounts        int    `json:"disabled_accounts"`
	CooldownAccounts        int    `json:"cooldown_accounts"`
	SoftExhaustedAccounts   int    `json:"soft_exhausted_accounts"`
	UsedTokens              int64  `json:"used_tokens"`
	UsedTokensM             string `json:"used_tokens_m"`
	MaximumTokens           int64  `json:"maximum_tokens"`
	MaximumTokensM          string `json:"maximum_tokens_m"`
	CurrentAvailableTokens  int64  `json:"current_available_tokens"`
	CurrentAvailableTokensM string `json:"current_available_tokens_m"`
	Window                  string `json:"window"`
	WindowLabel             string `json:"window_label"`
	Source                  string `json:"source"`
	AuthDir                 string `json:"auth_dir,omitempty"`
	MembershipSource        string `json:"membership_source,omitempty"`
	DroppedHistorical       int    `json:"dropped_historical_accounts,omitempty"`
}

type usageAgg struct {
	authIndex  string // original usage_events.auth_index when known
	email      string
	authFile   string
	tokens24h  int64
	success24h int64
	failed24h  int64
	lastUsage  time.Time
}

type coolingAgg struct {
	authIndex  string
	email      string
	authFile   string
	reason     string
	failureAt  time.Time
	recoverAt  time.Time
	statusCode int
}

func detectGlobalStatusPath() string {
	if v := strings.TrimSpace(os.Getenv("GROK_QUOTA_GLOBAL_STATUS")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("CPA_ACCOUNT_STATUS")); v != "" {
		return v
	}
	candidates := []string{
		filepath.Join("plugins", "account-status.json"),
		`E:\CPA\plugins\account-status.json`,
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append([]string{
			filepath.Join(cwd, "plugins", "account-status.json"),
			filepath.Clean(filepath.Join(cwd, "..", "plugins", "account-status.json")),
		}, candidates...)
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return filepath.Join("plugins", "account-status.json")
}

// loadGlobalQuotaCooldown joins the single-writer global bus published by
// quota-enforcer-v1. Observation only — never PATCHes auth disabled.
func loadGlobalQuotaCooldown(path string, now time.Time) map[string]coolingAgg {
	out := map[string]coolingAgg{}
	path = strings.TrimSpace(path)
	if path == "" {
		return out
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return out
	}
	var doc struct {
		Accounts map[string]struct {
			AuthFile  string `json:"auth_file"`
			AuthIndex string `json:"auth_index"`
			Email     string `json:"email"`
			Effective struct {
				PrimaryKind  string `json:"primary_kind"`
				PrimaryOwner string `json:"primary_owner"`
				RecoverAt    string `json:"recover_at"`
				Reason       string `json:"reason"`
				StatusCode   int    `json:"status_code"`
			} `json:"effective"`
			States []struct {
				Kind       string `json:"kind"`
				Owner      string `json:"owner"`
				Reason     string `json:"reason"`
				FailureAt  string `json:"failure_at"`
				RecoverAt  string `json:"recover_at"`
				StatusCode int    `json:"status_code"`
				AuthIndex  string `json:"auth_index"`
			} `json:"states"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return out
	}
	for name, acc := range doc.Accounts {
		if acc.Effective.PrimaryKind != "quota_cooldown" {
			continue
		}
		var failAt, recoverAt time.Time
		var status int
		reason := acc.Effective.Reason
		authIndex := strings.TrimSpace(acc.AuthIndex)
		for _, st := range acc.States {
			if st.Kind != "quota_cooldown" {
				continue
			}
			if t, err := time.Parse(time.RFC3339Nano, st.FailureAt); err == nil {
				failAt = t.UTC()
			} else if t, err := time.Parse(time.RFC3339, st.FailureAt); err == nil {
				failAt = t.UTC()
			}
			if t, err := time.Parse(time.RFC3339Nano, st.RecoverAt); err == nil {
				recoverAt = t.UTC()
			} else if t, err := time.Parse(time.RFC3339, st.RecoverAt); err == nil {
				recoverAt = t.UTC()
			}
			if st.StatusCode != 0 {
				status = st.StatusCode
			}
			if strings.TrimSpace(st.Reason) != "" {
				reason = st.Reason
			}
			if strings.TrimSpace(st.AuthIndex) != "" {
				authIndex = strings.TrimSpace(st.AuthIndex)
			}
		}
		if recoverAt.IsZero() {
			if t, err := time.Parse(time.RFC3339Nano, acc.Effective.RecoverAt); err == nil {
				recoverAt = t.UTC()
			} else if t, err := time.Parse(time.RFC3339, acc.Effective.RecoverAt); err == nil {
				recoverAt = t.UTC()
			}
		}
		if !recoverAt.After(now) {
			continue
		}
		if failAt.IsZero() {
			failAt = recoverAt.Add(-defaultWindow)
		}
		if authIndex == "" {
			// Prefer joining later by file/email; still store under file key.
			authIndex = "file:" + filepath.Base(name)
		}
		email := strings.TrimSpace(acc.Email)
		if email == "" {
			email = deriveEmail("", name)
		}
		c := coolingAgg{
			authIndex:  authIndex,
			email:      email,
			authFile:   filepath.Base(name),
			reason:     reason,
			failureAt:  failAt,
			recoverAt:  recoverAt,
			statusCode: status,
		}
		// Index by auth_index when real; also keep file-based for union later.
		out[authIndex] = c
		if !strings.HasPrefix(authIndex, "file:") {
			out["file:"+filepath.Base(name)] = c
		}
	}
	return out
}

func detectUsageDBPath() string {
	candidates := []string{}
	if v := strings.TrimSpace(os.Getenv("GROK_QUOTA_CPAMP_DB")); v != "" {
		candidates = append(candidates, v)
	}
	if v := strings.TrimSpace(os.Getenv("CPAMP_USAGE_DB")); v != "" {
		candidates = append(candidates, v)
	}
	if v := strings.TrimSpace(os.Getenv("GROK_PANEL_CPAMP_DB")); v != "" {
		candidates = append(candidates, v)
	}
	candidates = append(candidates,
		`E:\CPAMP\data\usage.sqlite`,
		`C:\CPAMP\data\usage.sqlite`,
	)
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(cwd, "CPAMP", "data", "usage.sqlite"),
			filepath.Clean(filepath.Join(cwd, "..", "CPAMP", "data", "usage.sqlite")),
		)
	}
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return ""
}

func openUsageDB(path string) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("usage db path empty")
	}
	// Read-only URI keeps the live CPAMP writer unblocked.
	dsn := "file:" + filepath.ToSlash(path) + "?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func isQuotaExhaustionFailure(status int, summary, body, errKind, errCode string) bool {
	blob := strings.ToLower(strings.Join([]string{summary, body, errKind, errCode}, "\n"))
	if quotaCodeRE.MatchString(blob) {
		return status == 402 || status == 429 || status == 403 || status == 0
	}
	if notQuotaRE.MatchString(blob) {
		return false
	}
	// "rate limit" alone is capacity/throttle, not free quota exhaustion.
	if plainRateLimitRE.MatchString(blob) && !strings.Contains(blob, "usage") {
		return false
	}
	if status == 402 && (strings.Contains(blob, "credit") || strings.Contains(blob, "spending") || strings.Contains(blob, "quota")) {
		return true
	}
	return false
}

func extractQuotaReason(summary string) string {
	re := regexp.MustCompile(`"code"\s*:\s*"([^"]+)"`)
	if m := re.FindStringSubmatch(summary); len(m) == 2 {
		return m[1]
	}
	low := strings.ToLower(summary)
	switch {
	case strings.Contains(low, "free-usage-exhausted"):
		return "subscription:free-usage-exhausted"
	case strings.Contains(low, "spending-limit"):
		return "personal-team-blocked:spending-limit"
	default:
		return "quota_exhausted"
	}
}

func loadRollingUsage(db *sql.DB, now time.Time, window time.Duration) (map[string]usageAgg, error) {
	sinceMS := now.Add(-window).UnixMilli()
	rows, err := db.Query(`
		SELECT auth_index,
			COALESCE(MAX(auth_file_snapshot), ''),
			COALESCE(MAX(account_snapshot), ''),
			COALESCE(SUM(CASE WHEN failed = 0 THEN total_tokens ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN failed = 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN failed = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(MAX(CASE WHEN failed = 0 THEN timestamp_ms ELSE NULL END), 0)
		FROM usage_events
		WHERE auth_provider_snapshot = ? AND timestamp_ms >= ?
		GROUP BY auth_index
	`, xaiProvider, sinceMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]usageAgg{}
	for rows.Next() {
		var authIndex, authFile, account string
		var tokens, success, failed, lastMS int64
		if err := rows.Scan(&authIndex, &authFile, &account, &tokens, &success, &failed, &lastMS); err != nil {
			return nil, err
		}
		authIndex = strings.TrimSpace(authIndex)
		if authIndex == "" {
			continue
		}
		email := deriveEmail(account, authFile)
		agg := usageAgg{
			authIndex:  authIndex, // keep original event auth_index for display join
			email:      email,
			authFile:   filepath.Base(strings.TrimSpace(authFile)),
			tokens24h:  tokens,
			success24h: success,
			failed24h:  failed,
		}
		if lastMS > 0 {
			agg.lastUsage = time.UnixMilli(lastMS).UTC()
		}
		out[authIndex] = agg
	}
	return out, rows.Err()
}

func loadActiveCoolings(db *sql.DB, now time.Time, lookback time.Duration) (map[string]coolingAgg, error) {
	sinceMS := now.Add(-lookback).UnixMilli()
	rows, err := db.Query(`
		SELECT id, timestamp_ms, fail_status_code,
			COALESCE(fail_summary, ''), COALESCE(fail_body, ''),
			COALESCE(auth_index, ''), COALESCE(auth_file_snapshot, ''),
			COALESCE(account_snapshot, ''),
			COALESCE(header_quota_recover_at_ms, 0),
			COALESCE(header_error_kind, ''), COALESCE(header_error_code, '')
		FROM usage_events
		WHERE failed = 1
		  AND auth_provider_snapshot = ?
		  AND timestamp_ms >= ?
		  AND fail_status_code IN (402, 403, 429)
		ORDER BY timestamp_ms ASC
	`, xaiProvider, sinceMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	latest := map[string]coolingAgg{}
	for rows.Next() {
		var id, tsMS, status, recoverMS int64
		var summary, body, authIndex, authFile, account, errKind, errCode string
		if err := rows.Scan(&id, &tsMS, &status, &summary, &body, &authIndex, &authFile, &account, &recoverMS, &errKind, &errCode); err != nil {
			return nil, err
		}
		if !isQuotaExhaustionFailure(int(status), summary, body, errKind, errCode) {
			continue
		}
		authIndex = strings.TrimSpace(authIndex)
		if authIndex == "" {
			continue
		}
		failAt := time.UnixMilli(tsMS).UTC()
		var recoverAt time.Time
		if recoverMS > tsMS {
			recoverAt = time.UnixMilli(recoverMS).UTC()
		} else {
			recoverAt = failAt.Add(defaultWindow)
		}
		if !recoverAt.After(now) {
			continue
		}
		latest[authIndex] = coolingAgg{
			authIndex:  authIndex,
			email:      deriveEmail(account, authFile),
			authFile:   filepath.Base(strings.TrimSpace(authFile)),
			reason:     extractQuotaReason(summary),
			failureAt:  failAt,
			recoverAt:  recoverAt,
			statusCode: int(status),
		}
	}
	return latest, rows.Err()
}

func deriveEmail(accountSnapshot, authFile string) string {
	accountSnapshot = strings.TrimSpace(accountSnapshot)
	if strings.Contains(accountSnapshot, "@") {
		return accountSnapshot
	}
	base := filepath.Base(strings.TrimSpace(authFile))
	base = strings.TrimSuffix(base, ".json")
	if strings.HasPrefix(base, "xai-") {
		return strings.TrimPrefix(base, "xai-")
	}
	return base
}

func buildSnapshot(dbPath string, now time.Time) quotaSnapshot {
	now = now.UTC()
	authDir := detectAuthDir()
	snap := quotaSnapshot{
		Plugin:       pluginName,
		Version:      pluginVersion,
		ComputedAt:   now.Format(time.RFC3339Nano),
		ComputedAtCN: formatTimeCNFromTime(now),
		Timezone:     "Asia/Shanghai (UTC+8)",
		AsOfMS:       now.UnixMilli(),
		WindowHours:  int(defaultWindow / time.Hour),
		LimitTokens:  defaultLimitTokens,
		DBPath:       dbPath,
		Source:       sourceRolling,
		Note:         "本地观测额度，非 xAI 官方余额。账号列表以 CPA auth-dir 现网文件为准（已删除账号不会出现）；用量来自 usage_events 近24h；冷却 join account-status + 额度失败事件。",
		Summary: quotaSummary{
			Window:           "rolling_24h",
			WindowLabel:      "滚动 24 小时",
			Source:           sourceRolling,
			AuthDir:          authDir,
			MembershipSource: "cpa_auth_dir",
		},
		Accounts: []accountQuota{},
	}

	// Membership source of truth: current auth files on disk.
	// Historical usage_events alone must NOT resurrect deleted accounts.
	pool, poolErr := loadLiveAuthPool(authDir)
	if poolErr != nil {
		// Fallback only when auth-dir unreadable (tests / misconfig).
		snap.Summary.MembershipSource = "usage_events_fallback"
		snap.Note += " auth-dir 不可读，临时回退 usage_events 成员集：" + poolErr.Error()
	}
	byEmail, byFile := indexLiveAuths(pool)
	usePool := poolErr == nil && len(pool) > 0

	var usage map[string]usageAgg
	var cooling map[string]coolingAgg
	if dbPath != "" {
		db, err := openUsageDB(dbPath)
		if err != nil {
			snap.Error = err.Error()
			// still return pool-only rows if we have membership
		} else {
			defer db.Close()
			usage, err = loadRollingUsage(db, now, defaultWindow)
			if err != nil {
				snap.Error = "rolling usage: " + err.Error()
			}
			cooling, err = loadActiveCoolings(db, now, 48*time.Hour)
			if err != nil {
				if snap.Error != "" {
					snap.Error += "; "
				}
				snap.Error += "cooling: " + err.Error()
			}
		}
	} else if !usePool {
		snap.Error = "usage db not found"
		return snap
	}
	if usage == nil {
		usage = map[string]usageAgg{}
	}
	if cooling == nil {
		cooling = map[string]coolingAgg{}
	}

	// Prefer global bus from quota-enforcer for cooldown display.
	globalPath := detectGlobalStatusPath()
	var globalCooling map[string]coolingAgg
	if strings.EqualFold(strings.TrimSpace(globalPath), "none") {
		globalCooling = map[string]coolingAgg{}
	} else {
		globalCooling = loadGlobalQuotaCooldown(globalPath, now)
	}
	for k, g := range globalCooling {
		if strings.HasPrefix(k, "file:") {
			continue
		}
		cooling[k] = g
	}

	// Remap usage/cooling onto live membership keys (email/file join).
	usageByLive := map[string]usageAgg{}
	coolingByLive := map[string]coolingAgg{}
	dropped := 0
	for authIndex, u := range usage {
		if usePool {
			if key, ok := matchLiveKey(authIndex, u.email, u.authFile, pool, byEmail, byFile); ok {
				// Prefer higher token aggregate if multiple historical indices map to one live auth.
				prev := usageByLive[key]
				if u.tokens24h >= prev.tokens24h {
					usageByLive[key] = u
				}
				continue
			}
			dropped++
			continue
		}
		usageByLive[authIndex] = u
	}
	for authIndex, c := range cooling {
		if strings.HasPrefix(authIndex, "file:") {
			// try file key join
			if usePool {
				if key, ok := matchLiveKey("", c.email, c.authFile, pool, byEmail, byFile); ok {
					coolingByLive[key] = c
				}
			}
			continue
		}
		if usePool {
			if key, ok := matchLiveKey(authIndex, c.email, c.authFile, pool, byEmail, byFile); ok {
				coolingByLive[key] = c
				continue
			}
			// drop historical-only cooling
			continue
		}
		coolingByLive[authIndex] = c
	}
	// Global coolings with only file keys.
	for k, g := range globalCooling {
		if !strings.HasPrefix(k, "file:") {
			continue
		}
		if usePool {
			if key, ok := matchLiveKey(g.authIndex, g.email, g.authFile, pool, byEmail, byFile); ok {
				coolingByLive[key] = g
			}
		}
	}

	// Build account list from live pool (or fallback union).
	type seed struct {
		key      string
		email    string
		authFile string
		authIdx  string
		disabled bool
		inPool   bool
	}
	seeds := make([]seed, 0, 64)
	if usePool {
		for _, key := range sortedLiveKeys(pool) {
			a := pool[key]
			seeds = append(seeds, seed{
				key:      key,
				email:    a.Email,
				authFile: a.AuthFile,
				authIdx:  a.AuthIndex,
				disabled: a.Disabled,
				inPool:   true,
			})
		}
	} else {
		keys := map[string]struct{}{}
		for k := range usageByLive {
			keys[k] = struct{}{}
		}
		for k := range coolingByLive {
			keys[k] = struct{}{}
		}
		for k := range keys {
			u := usageByLive[k]
			c := coolingByLive[k]
			email := u.email
			authFile := u.authFile
			if email == "" {
				email = c.email
			}
			if authFile == "" {
				authFile = c.authFile
			}
			seeds = append(seeds, seed{key: k, email: email, authFile: authFile, authIdx: k, inPool: false})
		}
	}

	accounts := make([]accountQuota, 0, len(seeds))
	for _, s := range seeds {
		u := usageByLive[s.key]
		c, cool := coolingByLive[s.key]
		email := s.email
		authFile := s.authFile
		if email == "" {
			email = u.email
		}
		if email == "" {
			email = c.email
		}
		if authFile == "" {
			authFile = u.authFile
		}
		if authFile == "" {
			authFile = c.authFile
		}
		displayIndex := s.authIdx
		if displayIndex == "" {
			displayIndex = s.key
		}
		// Prefer real usage auth_index when available (better for panel join).
		if strings.TrimSpace(u.authIndex) != "" && !strings.HasPrefix(u.authIndex, "file:") {
			displayIndex = u.authIndex
		} else if cool && strings.TrimSpace(c.authIndex) != "" && !strings.HasPrefix(c.authIndex, "file:") {
			displayIndex = c.authIndex
		}

		acc := accountQuota{
			AuthIndex:     displayIndex,
			Email:         email,
			AuthFile:      authFile,
			Tokens24h:     u.tokens24h,
			Success24h:    u.success24h,
			Failed24h:     u.failed24h,
			LimitTokens:   defaultLimitTokens,
			Window:        "rolling_24h",
			Source:        sourceRolling,
			Health:        "healthy",
			InPool:        s.inPool,
			AuthDisabled:  s.disabled,
			MembershipKey: s.key,
		}
		if s.disabled {
			acc.PoolStatus = "disabled"
		} else if s.inPool {
			acc.PoolStatus = "active"
		} else {
			acc.PoolStatus = "historical"
		}
		if !u.lastUsage.IsZero() {
			acc.LastUsageAt = u.lastUsage.Format(time.RFC3339Nano)
		}
		if cool {
			acc.Health = "cooldown"
			acc.Reason = c.reason
			acc.FailureAt = c.failureAt.Format(time.RFC3339Nano)
			acc.RecoverAt = c.recoverAt.Format(time.RFC3339Nano)
			acc.StatusCode = c.statusCode
			acc.SchedulerOwner = "quota-enforcer-v1"
			acc.GlobalStatusKind = "quota_cooldown"
		} else if acc.Tokens24h >= defaultLimitTokens {
			acc.SoftExhausted = true
			acc.Health = "soft_exhausted"
		}
		if acc.Tokens24h < 0 {
			acc.Tokens24h = 0
		}
		usedForBar := acc.Tokens24h
		if usedForBar > defaultLimitTokens {
			usedForBar = defaultLimitTokens
		}
		if acc.Health == "cooldown" {
			acc.Remaining = 0
			acc.Pct = 100
			acc.QuotaUsed = defaultLimitTokens
		} else {
			acc.Remaining = defaultLimitTokens - usedForBar
			if acc.Remaining < 0 {
				acc.Remaining = 0
			}
			acc.Pct = float64(usedForBar) / float64(defaultLimitTokens) * 100
			acc.QuotaUsed = acc.Tokens24h
		}
		acc.QuotaLimit = defaultLimitTokens
		acc.QuotaRemaining = acc.Remaining
		acc.QuotaHealth = acc.Health
		acc.QuotaFailureAt = acc.FailureAt
		acc.CooldownUntil = acc.RecoverAt
		acc.QuotaReason = acc.Reason
		acc.QuotaWindowStart = now.Add(-defaultWindow).Format(time.RFC3339Nano)
		accounts = append(accounts, acc)
	}
	sort.Slice(accounts, func(i, j int) bool {
		if accounts[i].Tokens24h == accounts[j].Tokens24h {
			return accounts[i].AuthIndex < accounts[j].AuthIndex
		}
		return accounts[i].Tokens24h > accounts[j].Tokens24h
	})

	var usedSum int64
	for _, a := range accounts {
		used := a.QuotaUsed
		if a.Health != "cooldown" {
			used = a.Tokens24h
			if used > defaultLimitTokens {
				used = defaultLimitTokens
			}
		}
		if used < 0 {
			used = 0
		}
		// Capacity only counts live pool accounts (deleted ones are gone).
		if a.InPool {
			usedSum += used
		}
		if a.Health == "cooldown" {
			snap.Summary.CooldownAccounts++
		}
		if a.SoftExhausted {
			snap.Summary.SoftExhaustedAccounts++
		}
		if a.AuthDisabled {
			snap.Summary.DisabledAccounts++
		} else if a.InPool {
			snap.Summary.ActiveAccounts++
		}
	}
	n := len(accounts)
	byAuth := make(map[string]accountQuota, n)
	for i := range accounts {
		enrichAccountDisplay(&accounts[i])
		byAuth[accounts[i].AuthIndex] = accounts[i]
		if accounts[i].MembershipKey != "" && accounts[i].MembershipKey != accounts[i].AuthIndex {
			byAuth[accounts[i].MembershipKey] = accounts[i]
		}
	}
	snap.Accounts = accounts
	snap.ByAuthIndex = byAuth
	snap.Summary.AccountCount = n
	snap.Summary.DroppedHistorical = dropped
	// Pool capacity = live accounts only.
	liveN := snap.Summary.ActiveAccounts + snap.Summary.DisabledAccounts
	if liveN == 0 {
		liveN = n
	}
	snap.Summary.MaximumTokens = int64(liveN) * defaultLimitTokens
	snap.Summary.UsedTokens = usedSum
	snap.Summary.CurrentAvailableTokens = snap.Summary.MaximumTokens - usedSum
	if snap.Summary.CurrentAvailableTokens < 0 {
		snap.Summary.CurrentAvailableTokens = 0
	}
	snap.Summary.UsedTokensM = formatTokensM(snap.Summary.UsedTokens)
	snap.Summary.MaximumTokensM = formatTokensM(snap.Summary.MaximumTokens)
	snap.Summary.CurrentAvailableTokensM = formatTokensM(snap.Summary.CurrentAvailableTokens)
	if snap.Summary.WindowLabel == "" {
		snap.Summary.WindowLabel = "滚动 24 小时"
	}
	snap.SchedulerSync = loadSchedulerSync()
	return snap
}

func loadSchedulerSync() map[string]any {
	out := map[string]any{
		"quota_writer":   "quota-enforcer-v1",
		"quota_observer": "grok-quota",
		"panel_display":  "grok-panel",
	}
	path := detectGlobalStatusPath()
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		out["global_status_path"] = path
		out["global_status_ok"] = false
		return out
	}
	var doc struct {
		Source    string `json:"source"`
		UpdatedAt string `json:"updated_at"`
		Summary   any    `json:"summary"`
		Roles     any    `json:"roles"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		out["global_status_path"] = path
		out["global_status_ok"] = false
		out["global_status_error"] = err.Error()
		return out
	}
	out["global_status_path"] = path
	out["global_status_ok"] = true
	out["source"] = doc.Source
	out["updated_at"] = doc.UpdatedAt
	out["summary"] = doc.Summary
	out["roles"] = doc.Roles
	return out
}

func computeLiveSnapshot() quotaSnapshot {
	return buildSnapshot(detectUsageDBPath(), time.Now().UTC())
}
