package main

import (
	"strings"
	"testing"
)

func TestStatusPageSeparatesHistoryAndDoesNotInventUnknownCeilings(t *testing.T) {
	page := statusPage()
	for _, want := range []string{
		"24h 已用额度 / 动态额度上限",
		"历史已用额度",
		"function historicalCell(a)",
		"function hasDynamicLimit(a)",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("status page missing %q", want)
		}
	}
	if strings.Contains(page, "a.limit_tokens||Math.max(ref,tokens)") {
		t.Fatal("unknown ceiling must not fall back to a reference amount")
	}
}

func TestStatusPageConfinesWideTableToItsScroller(t *testing.T) {
	page := statusPage()
	for _, want := range []string{
		`<section class="flex min-h-0 min-w-0 w-full`,
		`<div class="w-full min-w-0 flex-1 overflow-x-auto">`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("status page does not confine table overflow: missing %q", want)
		}
	}
}
