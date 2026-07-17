package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteStateFileLive(t *testing.T) {
	if os.Getenv("GROK_QUOTA_WRITE_STATE") != "1" {
		t.Skip()
	}
	snap := buildSnapshot(detectUsageDBPath(), time.Now().UTC())
	if err := writeStateFile(snap); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"accounts": len(snap.Accounts), "cooldown": snap.Summary.CooldownAccounts, "used": snap.Summary.UsedTokens, "error": snap.Error})
	t.Log(string(raw))
}

func TestVersionLess(t *testing.T) {
	if !versionLess("0.1.4", "0.1.7") {
		t.Fatal("0.1.4 should be < 0.1.7")
	}
	if versionLess("0.1.7", "0.1.4") {
		t.Fatal("0.1.7 should not be < 0.1.4")
	}
	if versionLess("0.1.7", "0.1.7") {
		t.Fatal("equal versions should not be less")
	}
}

func TestWriteStateFileRefusesOlderVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "grok-quota-state.json")
	stateFilePath = path

	newer := quotaSnapshot{Plugin: pluginName, Version: "0.1.7", Accounts: []accountQuota{{AuthIndex: "n"}}}
	if err := writeStateFile(newer); err != nil {
		t.Fatal(err)
	}
	older := quotaSnapshot{Plugin: pluginName, Version: "0.1.4", Accounts: []accountQuota{{AuthIndex: "old"}}}
	if err := writeStateFile(older); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got quotaSnapshot
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != "0.1.7" {
		t.Fatalf("older writer stomped state: got %s", got.Version)
	}
	if len(got.Accounts) != 1 || got.Accounts[0].AuthIndex != "n" {
		t.Fatalf("unexpected accounts: %+v", got.Accounts)
	}
}
