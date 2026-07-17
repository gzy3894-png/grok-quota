package main

import "testing"

func TestFormatTokensM(t *testing.T) {
	if got := formatTokensM(2_000_000); got != "2.00M" {
		t.Fatalf("got %s", got)
	}
	if got := formatTokensM(1_234_567); got != "1.23M" {
		t.Fatalf("got %s", got)
	}
	if got := formatTokensM(0); got != "0.00M" {
		t.Fatalf("got %s", got)
	}
}

func TestHealthAndReasonZH(t *testing.T) {
	if healthLabelZH("cooldown") != "额度问题" {
		t.Fatalf("cooldown label=%s", healthLabelZH("cooldown"))
	}
	if healthLabelZH("healthy") != "正常" {
		t.Fatal("healthy label")
	}
	if reasonLabelZH("subscription:free-usage-exhausted") != "免费额度用尽" {
		t.Fatal("reason free")
	}
	if reasonLabelZH("personal-team-blocked:spending-limit") != "个人团队消费限额" {
		t.Fatal("reason spend")
	}
}

func TestFormatTimeCN(t *testing.T) {
	// 2026-07-16T09:00:00Z => 2026-07-16 17:00:00 CST
	got := formatTimeCN("2026-07-16T09:00:00Z")
	if got != "2026-07-16 17:00:00" {
		t.Fatalf("got %q", got)
	}
}
