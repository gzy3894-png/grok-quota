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
	// Soft exhausted without hard failure
	mustExec(t, db, `INSERT INTO usage_events(id,timestamp_ms,auth_index,auth_provider_snapshot,auth_file_snapshot,account_snapshot,total_tokens,failed,fail_status_code,fail_summary,fail_body,header_quota_recover_at_ms,header_error_kind,header_error_code)
VALUES (5,?, 'c3','xai','xai-c@x.com.json','c@x.com', 2100000, 0, 0, '', '', 0, '', '')`, now.Add(-30*time.Minute).UnixMilli())

	t.Setenv("GROK_QUOTA_GLOBAL_STATUS", "none")
	// Force usage_events membership for unit isolation (no live CPA auth-dir).
	t.Setenv("GROK_QUOTA_AUTH_DIR", "none")
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
	b2 := by["b2"]
	if b2.Health != "cooldown" {
		t.Fatalf("b2 health=%s want cooldown", b2.Health)
	}
	if b2.Remaining != 0 {
		t.Fatalf("b2 remaining=%d", b2.Remaining)
	}
	if b2.QuotaHealth != "cooldown" || b2.QuotaLimit != 2_000_000 || b2.QuotaUsed != 2_000_000 {
		t.Fatalf("b2 panel aliases unexpected: health=%s limit=%d used=%d tokens24h=%d", b2.QuotaHealth, b2.QuotaLimit, b2.QuotaUsed, b2.Tokens24h)
	}
	// Audit field keeps real rolling usage (no success tokens for b2).
	if b2.Tokens24h != 0 {
		t.Fatalf("b2 tokens_24h should stay real rolling value 0, got %d", b2.Tokens24h)
	}
	c3 := by["c3"]
	if !c3.SoftExhausted || c3.Health != "soft_exhausted" {
		t.Fatalf("c3 soft exhausted not set: %+v", c3)
	}
	if snap.Summary.CooldownAccounts != 1 {
		t.Fatalf("cooldown_accounts=%d", snap.Summary.CooldownAccounts)
	}
}

func TestBuildSnapshotUsesLiveAuthPoolNotHistorical(t *testing.T) {
	dir := t.TempDir()
	authDir := filepath.Join(dir, "auths")
	if err := os.MkdirAll(authDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Only one live account remains after cleanup.
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
	// Historical deleted account still has usage — must NOT appear.
	mustExec(t, db, `INSERT INTO usage_events(id,timestamp_ms,auth_index,auth_provider_snapshot,auth_file_snapshot,account_snapshot,total_tokens,failed,fail_status_code,fail_summary,fail_body,header_quota_recover_at_ms,header_error_kind,header_error_code)
VALUES (1,?, 'gone1','xai','xai-gone@x.com.json','gone@x.com', 900000, 0, 0, '', '', 0, '', '')`, now.Add(-1*time.Hour).UnixMilli())
	// Live account usage joins by email/file.
	mustExec(t, db, `INSERT INTO usage_events(id,timestamp_ms,auth_index,auth_provider_snapshot,auth_file_snapshot,account_snapshot,total_tokens,failed,fail_status_code,fail_summary,fail_body,header_quota_recover_at_ms,header_error_kind,header_error_code)
VALUES (2,?, 'liveidx','xai','xai-keep@x.com.json','keep@x.com', 123456, 0, 0, '', '', 0, '', '')`, now.Add(-30*time.Minute).UnixMilli())

	t.Setenv("GROK_QUOTA_GLOBAL_STATUS", "none")
	t.Setenv("GROK_QUOTA_AUTH_DIR", authDir)
	snap := buildSnapshot(dbPath, now)
	if snap.Error != "" {
		t.Fatalf("error: %s", snap.Error)
	}
	if snap.Summary.AccountCount != 1 {
		t.Fatalf("account_count=%d want 1 (deleted must drop); accounts=%v", snap.Summary.AccountCount, snap.Accounts)
	}
	if snap.Summary.DroppedHistorical < 1 {
		t.Fatalf("expected dropped_historical >=1, got %d", snap.Summary.DroppedHistorical)
	}
	a := snap.Accounts[0]
	if a.Email != "keep@x.com" {
		t.Fatalf("email=%s", a.Email)
	}
	if a.Tokens24h != 123456 {
		t.Fatalf("tokens_24h=%d", a.Tokens24h)
	}
	if !a.InPool || a.PoolStatus != "active" {
		t.Fatalf("pool flags: in_pool=%v status=%s", a.InPool, a.PoolStatus)
	}
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec: %v", err)
	}
}
