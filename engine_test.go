package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestIsQuotaExhaustionFailure(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		summary string
		want    bool
	}{
		{"free 429", 429, `{"code":"subscription:free-usage-exhausted"}`, true},
		{"spending 402", 402, "personal-team-blocked:spending-limit", true},
		{"plain rate limit 429", 429, "rate limit exceeded", false},
		{"permission 403", 403, "permission-denied", false},
		{"quota exceeded text", 429, "quota exceeded for account", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isQuotaExhaustionFailure(tc.status, tc.summary, "", "", "")
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestParseOfficialActualLimit(t *testing.T) {
	blob := `{"code":"subscription:free-usage-exhausted","error":"You've used all the included free usage for model grok-4.5-build-free for now. Usage resets over a rolling 24-hour window — tokens (actual/limit): 2117440/1000000. Upgrade"}`
	actual, limit, ok := parseOfficialActualLimit(blob)
	if !ok || actual != 2_117_440 || limit != 1_000_000 {
		t.Fatalf("got ok=%v actual=%d limit=%d", ok, actual, limit)
	}
	if _, _, ok := parseOfficialActualLimit("no limit here"); ok {
		t.Fatal("expected parse fail")
	}
}

func TestAccountCeiling(t *testing.T) {
	// No exhaust observation means the ceiling is genuinely unknown.
	lim, mode, src, over := accountCeiling(3_100_000, observedOfficialLimit{}, false)
	if lim != 0 || mode != "unknown" || src != "none_no_free_usage_exhaust_log" || !over {
		t.Fatalf("no-obs: lim=%d mode=%s src=%s over=%v", lim, mode, src, over)
	}
	// The dynamic ceiling is the actual used amount when upstream reported exhaustion.
	lim, mode, src, over = accountCeiling(400_000, observedOfficialLimit{Limit: 1_000_000, Actual: 1_200_000}, true)
	if lim != 1_200_000 || mode != "observed" || src != "cpamp_free_usage_exhausted_actual_at_exhaust" || over {
		t.Fatalf("obs-actual: lim=%d mode=%s src=%s over=%v", lim, mode, src, over)
	}
}

func TestUpsertObservedLimitKeepsNewestExhaustionActual(t *testing.T) {
	observed := map[string]observedOfficialLimit{}
	older := observedOfficialLimit{
		Limit:  2_000_000,
		Actual: 2_100_000,
		At:     time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
	}
	newer := observedOfficialLimit{
		Limit:  1_000_000,
		Actual: 1_200_000,
		At:     time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC),
	}
	upsertObservedLimit(observed, "a1", older)
	upsertObservedLimit(observed, "a1", newer)
	if got := observed["a1"]; got.Actual != newer.Actual || got.Limit != newer.Limit {
		t.Fatalf("latest exhaustion must replace prior ceiling, got actual/limit=%d/%d", got.Actual, got.Limit)
	}
}

func TestBuildSnapshotRollingWindow(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "usage.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
CREATE TABLE usage_events (
  id INTEGER PRIMARY KEY,
  timestamp_ms INTEGER,
  auth_index TEXT,
  auth_provider_snapshot TEXT,
  auth_file_snapshot TEXT,
  account_snapshot TEXT,
  total_tokens INTEGER,
  failed INTEGER,
  fail_status_code INTEGER,
  fail_summary TEXT,
  fail_body TEXT,
  header_quota_recover_at_ms INTEGER,
  header_error_kind TEXT,
  header_error_code TEXT
);`)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	// Inside 24h
	mustExec(t, db, `INSERT INTO usage_events(id,timestamp_ms,auth_index,auth_provider_snapshot,auth_file_snapshot,account_snapshot,total_tokens,failed,fail_status_code,fail_summary,fail_body,header_quota_recover_at_ms,header_error_kind,header_error_code)
VALUES (1,?, 'a1','xai','xai-a@x.com.json','a@x.com', 500000, 0, 0, '', '', 0, '', '')`, now.Add(-2*time.Hour).UnixMilli())
	// Outside 24h — must not count
	mustExec(t, db, `INSERT INTO usage_events(id,timestamp_ms,auth_index,auth_provider_snapshot,auth_file_snapshot,account_snapshot,total_tokens,failed,fail_status_code,fail_summary,fail_body,header_quota_recover_at_ms,header_error_kind,header_error_code)
VALUES (2,?, 'a1','xai','xai-a@x.com.json','a@x.com', 9000000, 0, 0, '', '', 0, '', '')`, now.Add(-30*time.Hour).UnixMilli())
	// Failed success tokens should not count
	mustExec(t, db, `INSERT INTO usage_events(id,timestamp_ms,auth_index,auth_provider_snapshot,auth_file_snapshot,account_snapshot,total_tokens,failed,fail_status_code,fail_summary,fail_body,header_quota_recover_at_ms,header_error_kind,header_error_code)
VALUES (3,?, 'a1','xai','xai-a@x.com.json','a@x.com', 100000, 1, 500, 'boom', '', 0, '', '')`, now.Add(-1*time.Hour).UnixMilli())
	// Active cooldown account
	failMS := now.Add(-1 * time.Hour).UnixMilli()
	mustExec(t, db, `INSERT INTO usage_events(id,timestamp_ms,auth_index,auth_provider_snapshot,auth_file_snapshot,account_snapshot,total_tokens,failed,fail_status_code,fail_summary,fail_body,header_quota_recover_at_ms,header_error_kind,header_error_code)
VALUES (4,?, 'b2','xai','xai-b@x.com.json','b@x.com', 0, 1, 429, 'subscription:free-usage-exhausted', '', 0, '', '')`, failMS)
	// High usage WITHOUT hard failure — must stay healthy, record full 3.1M
	mustExec(t, db, `INSERT INTO usage_events(id,timestamp_ms,auth_index,auth_provider_snapshot,auth_file_snapshot,account_snapshot,total_tokens,failed,fail_status_code,fail_summary,fail_body,header_quota_recover_at_ms,header_error_kind,header_error_code)
VALUES (5,?, 'c3','xai','xai-c@x.com.json','c@x.com', 3100000, 0, 0, '', '', 0, '', '')`, now.Add(-30*time.Minute).UnixMilli())

	t.Setenv("GROK_QUOTA_GLOBAL_STATUS", "none")
	t.Setenv("GROK_QUOTA_AUTH_DIR", "none")
	t.Setenv("GROK_QUOTA_AUTO_DISABLE", "false")
	// Isolate settings file
	t.Setenv("GROK_QUOTA_SETTINGS_PATH", filepath.Join(dir, "settings.json"))
	settingsLoaded = false

	snap := buildSnapshot(dbPath, now)
	if snap.Error != "" {
		t.Fatalf("snapshot error: %s", snap.Error)
	}
	by := accountsByAuthIndex(snap)
	a1 := by["a1"]
	if a1.Tokens24h != 500000 {
		t.Fatalf("a1 tokens_24h=%d want 500000", a1.Tokens24h)
	}
	if a1.Health != "healthy" {
		t.Fatalf("a1 health=%s", a1.Health)
	}
	if a1.QuotaUsed != 500000 || a1.LimitTokens != nil {
		t.Fatalf("a1 used/limit = %d/%v", a1.QuotaUsed, a1.LimitTokens)
	}
	if a1.LimitMode != "unknown" || a1.LimitSource != "none_no_free_usage_exhaust_log" {
		t.Fatalf("a1 should have unknown ceiling, mode=%s src=%s", a1.LimitMode, a1.LimitSource)
	}
	if a1.Remaining != nil {
		t.Fatalf("a1 remaining must be unknown, got %v", a1.Remaining)
	}

	b2 := by["b2"]
	if b2.Health != "cooldown" {
		t.Fatalf("b2 health=%s want cooldown", b2.Health)
	}
	if b2.Remaining != nil || b2.LimitTokens != nil {
		t.Fatalf("b2 must keep an unknown ceiling/remaining, got limit=%v remaining=%v", b2.LimitTokens, b2.Remaining)
	}
	// Real usage stays 0; do NOT invent 2M used.
	if b2.QuotaUsed != 0 || b2.Tokens24h != 0 {
		t.Fatalf("b2 must keep real usage 0, got used=%d tokens=%d", b2.QuotaUsed, b2.Tokens24h)
	}
	if !b2.SuggestDisable {
		t.Fatalf("b2 should suggest disable (log-proven quota issue)")
	}

	c3 := by["c3"]
	if c3.Health != "healthy" {
		t.Fatalf("c3 must stay healthy without log evidence, got %s", c3.Health)
	}
	if c3.SoftExhausted {
		t.Fatalf("c3 must not soft-exhaust on local 2M")
	}
	if c3.Tokens24h != 3_100_000 || c3.QuotaUsed != 3_100_000 {
		t.Fatalf("c3 must record full 3.1M, got tokens=%d used=%d", c3.Tokens24h, c3.QuotaUsed)
	}
	// CRITICAL: without free-usage actual/limit, the ceiling is unknown.
	if c3.LimitTokens != nil || c3.LimitMode != "unknown" {
		t.Fatalf("c3 must have unknown ceiling, got limit=%v mode=%s", c3.LimitTokens, c3.LimitMode)
	}
	if c3.Remaining != nil {
		t.Fatalf("c3 remaining must be unknown, got %v", c3.Remaining)
	}
	if !c3.OverReference {
		t.Fatalf("c3 should be over reference")
	}
	if c3.StatusKind != "high_usage" {
		t.Fatalf("c3 status_kind=%s want high_usage", c3.StatusKind)
	}
	if c3.Remark == "" {
		t.Fatalf("c3 needs remark explaining high usage")
	}
	if c3.EmailMasked == "" || !containsAtMask(c3.EmailMasked) {
		t.Fatalf("c3 email should be masked, got %q", c3.EmailMasked)
	}
	if snap.Summary.SoftExhaustedAccounts != 0 {
		t.Fatalf("soft_exhausted_accounts should be 0, got %d", snap.Summary.SoftExhaustedAccounts)
	}
	if snap.Summary.CooldownAccounts != 1 || snap.Summary.QuotaIssueAccounts != 1 {
		t.Fatalf("quota issues=%d cooldown=%d", snap.Summary.QuotaIssueAccounts, snap.Summary.CooldownAccounts)
	}
	if snap.Summary.HighUsageAccounts != 1 {
		t.Fatalf("high_usage_accounts=%d", snap.Summary.HighUsageAccounts)
	}
}

func TestBuildSnapshotUsesLiveAuthPoolNotHistorical(t *testing.T) {
	dir := t.TempDir()
	authDir := filepath.Join(dir, "auths")
	if err := os.MkdirAll(authDir, 0o755); err != nil {
		t.Fatal(err)
	}
	live := map[string]any{
		"type":     "xai",
		"email":    "keep@x.com",
		"disabled": false,
	}
	raw, _ := json.Marshal(live)
	if err := os.WriteFile(filepath.Join(authDir, "xai-keep@x.com.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(dir, "usage.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
CREATE TABLE usage_events (
  id INTEGER PRIMARY KEY,
  timestamp_ms INTEGER,
  auth_index TEXT,
  auth_provider_snapshot TEXT,
  auth_file_snapshot TEXT,
  account_snapshot TEXT,
  total_tokens INTEGER,
  failed INTEGER,
  fail_status_code INTEGER,
  fail_summary TEXT,
  fail_body TEXT,
  header_quota_recover_at_ms INTEGER,
  header_error_kind TEXT,
  header_error_code TEXT
);`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	mustExec(t, db, `INSERT INTO usage_events(id,timestamp_ms,auth_index,auth_provider_snapshot,auth_file_snapshot,account_snapshot,total_tokens,failed,fail_status_code,fail_summary,fail_body,header_quota_recover_at_ms,header_error_kind,header_error_code)
VALUES (1,?, 'keep1','xai','xai-keep@x.com.json','keep@x.com', 100, 0, 0, '', '', 0, '', '')`, now.Add(-1*time.Hour).UnixMilli())
	mustExec(t, db, `INSERT INTO usage_events(id,timestamp_ms,auth_index,auth_provider_snapshot,auth_file_snapshot,account_snapshot,total_tokens,failed,fail_status_code,fail_summary,fail_body,header_quota_recover_at_ms,header_error_kind,header_error_code)
VALUES (2,?, 'gone1','xai','xai-gone@x.com.json','gone@x.com', 999999, 0, 0, '', '', 0, '', '')`, now.Add(-1*time.Hour).UnixMilli())

	t.Setenv("GROK_QUOTA_AUTH_DIR", authDir)
	t.Setenv("GROK_QUOTA_GLOBAL_STATUS", "none")
	t.Setenv("GROK_QUOTA_SETTINGS_PATH", filepath.Join(dir, "settings.json"))
	t.Setenv("GROK_QUOTA_AUTO_DISABLE", "false")
	settingsLoaded = false

	snap := buildSnapshot(dbPath, now)
	if snap.Summary.AccountCount != 1 {
		t.Fatalf("account_count=%d want 1", snap.Summary.AccountCount)
	}
	if snap.Summary.DroppedHistorical < 1 {
		t.Fatalf("expected dropped historical >=1, got %d", snap.Summary.DroppedHistorical)
	}
}

func TestAutoDisableQuotaIssue(t *testing.T) {
	dir := t.TempDir()
	authDir := filepath.Join(dir, "auths")
	if err := os.MkdirAll(authDir, 0o755); err != nil {
		t.Fatal(err)
	}
	live := map[string]any{"type": "xai", "email": "bad@x.com", "disabled": false}
	raw, _ := json.Marshal(live)
	authFile := "xai-bad@x.com.json"
	if err := os.WriteFile(filepath.Join(authDir, authFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "usage.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
CREATE TABLE usage_events (
  id INTEGER PRIMARY KEY,
  timestamp_ms INTEGER,
  auth_index TEXT,
  auth_provider_snapshot TEXT,
  auth_file_snapshot TEXT,
  account_snapshot TEXT,
  total_tokens INTEGER,
  failed INTEGER,
  fail_status_code INTEGER,
  fail_summary TEXT,
  fail_body TEXT,
  header_quota_recover_at_ms INTEGER,
  header_error_kind TEXT,
  header_error_code TEXT
);`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	mustExec(t, db, `INSERT INTO usage_events(id,timestamp_ms,auth_index,auth_provider_snapshot,auth_file_snapshot,account_snapshot,total_tokens,failed,fail_status_code,fail_summary,fail_body,header_quota_recover_at_ms,header_error_kind,header_error_code)
VALUES (1,?, 'bad1','xai',?, 'bad@x.com', 100, 1, 429, 'subscription:free-usage-exhausted', '', 0, '', '')`, now.Add(-1*time.Hour).UnixMilli(), authFile)

	t.Setenv("GROK_QUOTA_AUTH_DIR", authDir)
	t.Setenv("GROK_QUOTA_GLOBAL_STATUS", "none")
	t.Setenv("GROK_QUOTA_SETTINGS_PATH", filepath.Join(dir, "settings.json"))
	t.Setenv("GROK_QUOTA_AUTO_DISABLE", "true")
	settingsLoaded = false

	snap := buildSnapshot(dbPath, now)
	if snap.Summary.AutoDisabledNow != 1 {
		t.Fatalf("auto_disabled_now=%d want 1", snap.Summary.AutoDisabledNow)
	}
	raw2, err := os.ReadFile(filepath.Join(authDir, authFile))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw2, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["disabled"] != true {
		t.Fatalf("auth file not disabled: %#v", doc["disabled"])
	}
}

func TestObservedOfficialLimitFromFreeUsageLog(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "usage.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
CREATE TABLE usage_events (
  id INTEGER PRIMARY KEY,
  timestamp_ms INTEGER,
  auth_index TEXT,
  auth_provider_snapshot TEXT,
  auth_file_snapshot TEXT,
  account_snapshot TEXT,
  total_tokens INTEGER,
  failed INTEGER,
  fail_status_code INTEGER,
  fail_summary TEXT,
  fail_body TEXT,
  header_quota_recover_at_ms INTEGER,
  header_error_kind TEXT,
  header_error_code TEXT
);`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	// Past free-usage exhaust establishes the next cycle's dynamic ceiling.
	body := `{"code":"subscription:free-usage-exhausted","error":"tokens (actual/limit): 1000500/1000000"}`
	mustExec(t, db, `INSERT INTO usage_events(id,timestamp_ms,auth_index,auth_provider_snapshot,auth_file_snapshot,account_snapshot,total_tokens,failed,fail_status_code,fail_summary,fail_body,header_quota_recover_at_ms,header_error_kind,header_error_code)
VALUES (1,?, 'd4','xai','xai-d@x.com.json','d@x.com', 0, 1, 429, ?, ?, 0, '', '')`,
		now.Add(-30*time.Hour).UnixMilli(), body, body)
	// Current rolling usage under the observed 1M ceiling.
	mustExec(t, db, `INSERT INTO usage_events(id,timestamp_ms,auth_index,auth_provider_snapshot,auth_file_snapshot,account_snapshot,total_tokens,failed,fail_status_code,fail_summary,fail_body,header_quota_recover_at_ms,header_error_kind,header_error_code)
VALUES (2,?, 'd4','xai','xai-d@x.com.json','d@x.com', 400000, 0, 0, '', '', 0, '', '')`,
		now.Add(-1*time.Hour).UnixMilli())
	// Another account never exhausted — stays reference 2M.
	mustExec(t, db, `INSERT INTO usage_events(id,timestamp_ms,auth_index,auth_provider_snapshot,auth_file_snapshot,account_snapshot,total_tokens,failed,fail_status_code,fail_summary,fail_body,header_quota_recover_at_ms,header_error_kind,header_error_code)
VALUES (3,?, 'e5','xai','xai-e@x.com.json','e@x.com', 100000, 0, 0, '', '', 0, '', '')`,
		now.Add(-1*time.Hour).UnixMilli())
	// Historical usage stays separate from the rolling 24h numerator.
	mustExec(t, db, `INSERT INTO usage_events(id,timestamp_ms,auth_index,auth_provider_snapshot,auth_file_snapshot,account_snapshot,total_tokens,failed,fail_status_code,fail_summary,fail_body,header_quota_recover_at_ms,header_error_kind,header_error_code)
VALUES (4,?, 'd4','xai','xai-d@x.com.json','d@x.com', 800000, 0, 0, '', '', 0, '', '')`,
		now.Add(-30*time.Hour).UnixMilli())
	// A cooling account must keep its real rolling ratio, rather than a frozen 100%.
	coolBody := `{"code":"subscription:free-usage-exhausted","error":"tokens (actual/limit): 1000000/1000000"}`
	mustExec(t, db, `INSERT INTO usage_events(id,timestamp_ms,auth_index,auth_provider_snapshot,auth_file_snapshot,account_snapshot,total_tokens,failed,fail_status_code,fail_summary,fail_body,header_quota_recover_at_ms,header_error_kind,header_error_code)
VALUES (5,?, 'f6','xai','xai-f@x.com.json','f@x.com', 1500000, 0, 0, '', '', 0, '', '')`,
		now.Add(-45*time.Minute).UnixMilli())
	mustExec(t, db, `INSERT INTO usage_events(id,timestamp_ms,auth_index,auth_provider_snapshot,auth_file_snapshot,account_snapshot,total_tokens,failed,fail_status_code,fail_summary,fail_body,header_quota_recover_at_ms,header_error_kind,header_error_code)
VALUES (6,?, 'f6','xai','xai-f@x.com.json','f@x.com', 0, 1, 429, ?, ?, ?, '', '')`,
		now.Add(-15*time.Minute).UnixMilli(), coolBody, coolBody, now.Add(time.Hour).UnixMilli())

	t.Setenv("GROK_QUOTA_GLOBAL_STATUS", "none")
	t.Setenv("GROK_QUOTA_AUTH_DIR", "none")
	t.Setenv("GROK_QUOTA_AUTO_DISABLE", "false")
	t.Setenv("GROK_QUOTA_SETTINGS_PATH", filepath.Join(dir, "settings.json"))
	settingsLoaded = false

	snap := buildSnapshot(dbPath, now)
	if snap.Error != "" {
		t.Fatalf("snapshot error: %s", snap.Error)
	}
	by := accountsByAuthIndex(snap)
	d4 := by["d4"]
	if d4.LimitTokens == nil || *d4.LimitTokens != 1_000_500 || d4.LimitMode != "observed" {
		t.Fatalf("d4 limit/mode = %v/%s want 1.0005M/observed", d4.LimitTokens, d4.LimitMode)
	}
	if d4.Remaining == nil || *d4.Remaining != 600_500 {
		t.Fatalf("d4 remaining=%v want 600500", d4.Remaining)
	}
	if d4.LimitSource != "cpamp_free_usage_exhausted_actual_at_exhaust" {
		t.Fatalf("d4 limit_source=%s", d4.LimitSource)
	}
	if d4.LimitActualAtExhaust != 1_000_500 {
		t.Fatalf("d4 actual_at_exhaust=%d", d4.LimitActualAtExhaust)
	}
	if d4.HistoricalTokens != 1_200_000 {
		t.Fatalf("d4 historical_tokens=%d want 1200000", d4.HistoricalTokens)
	}
	if d4.ShowReference {
		t.Fatalf("d4 should not show an unconfirmed reference once observed")
	}
	e5 := by["e5"]
	if e5.LimitTokens != nil || e5.LimitMode != "unknown" {
		t.Fatalf("e5 should have unknown ceiling, got %v/%s", e5.LimitTokens, e5.LimitMode)
	}
	if e5.HistoricalTokens != 100_000 {
		t.Fatalf("e5 historical_tokens=%d", e5.HistoricalTokens)
	}
	f6 := by["f6"]
	if f6.Health != "cooldown" || f6.LimitTokens == nil || *f6.LimitTokens != 1_000_000 {
		t.Fatalf("f6 cooldown/limit=%s/%v", f6.Health, f6.LimitTokens)
	}
	if f6.Pct == nil || *f6.Pct != 150 {
		t.Fatalf("f6 pct=%v want real rolling ratio 150", f6.Pct)
	}
	if f6.Remaining == nil || *f6.Remaining != 0 {
		t.Fatalf("f6 remaining=%v want 0 while cooldown is active", f6.Remaining)
	}
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec: %v", err)
	}
}

func containsAtMask(s string) bool {
	return strings.Contains(s, "@***")
}
