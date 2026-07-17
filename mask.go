package main

import (
	"strings"
	"unicode/utf8"
)

// maskAtAndAfter hides "@" and everything after it for display.
// Full original remains in API fields for copy / panel join.
// Examples:
//
//	alice@example.com  -> alice@***
//	xai-alice@x.com.json -> xai-alice@***.json (keeps trailing extension after domain when present)
func maskAtAndAfter(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	i := strings.Index(s, "@")
	if i < 0 {
		return s
	}
	local := s[:i]
	rest := s[i+1:] // after @
	// Only preserve auth-file style extensions, not email TLDs like .com.
	ext := ""
	for _, e := range []string{".json", ".JSON", ".txt", ".yaml", ".yml"} {
		if strings.HasSuffix(rest, e) {
			ext = e
			break
		}
	}
	if local == "" {
		return "@***" + ext
	}
	return local + "@***" + ext
}

// maskEmailDisplay prefers email masking; falls back to generic @ mask.
func maskEmailDisplay(email string) string {
	return maskAtAndAfter(email)
}

// shortLocal keeps a short preview of the local-part for very long names (UI only).
func shortLocal(local string, keep int) string {
	if keep < 1 {
		keep = 2
	}
	if utf8.RuneCountInString(local) <= keep+1 {
		return local
	}
	runes := []rune(local)
	return string(runes[:keep]) + "…"
}
