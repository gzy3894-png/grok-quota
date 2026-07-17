package main

import "testing"

func TestMaskAtAndAfter(t *testing.T) {
	cases := map[string]string{
		"alice@example.com":     "alice@***",
		"xai-alice@x.com.json":  "xai-alice@***.json",
		"no-at-here":            "no-at-here",
		"@only.com":             "@***",
		"":                      "",
	}
	for in, want := range cases {
		if got := maskAtAndAfter(in); got != want {
			t.Fatalf("maskAtAndAfter(%q)=%q want %q", in, got, want)
		}
	}
}
