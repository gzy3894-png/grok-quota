package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
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

func TestDynamicLimit(t *testing.T) {
	if got := dynamicLimit(500_000, 2_000_000); got != 2_000_000 {
		t.Fatalf("under reference: %d", got)
	}
	if got := dynamicLimit(3_000_000, 2_000_000); got != 3_000_000 {
		t.Fatalf("over reference: %d", got)
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
	if a1.QuotaUsed != 500000 || a1.LimitTokens != 2_000_000 {
		t.Fatalf("a1 used/limit = %d/%d", a1.QuotaUsed, a1.LimitTokens)
	}

	b2 := by["b2"]
	if b2.Health != "cooldown" {
		t.Fatalf("b2 health=%s want cooldown", b2.Health)
	}
	if b2.Remaining != 0 {
		t.Fatalf("b2 remaining=%d", b2.Remaining)
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
	if c3.LimitTokens != 3_100_000 {
		t.Fatalf("c3 dynamic limit should be 3.1M, got %d", c3.LimitTokens)
	}
	if !c3.OverReference {
		t.Fatalf("c3 should be over reference")
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

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec: %v", err)
	}
}
