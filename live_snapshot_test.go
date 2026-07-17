package main

import (
  "encoding/json"
  "os"
  "testing"
  "time"
)

func TestLiveSnapshot(t *testing.T) {
  if os.Getenv("GROK_QUOTA_LIVE") != "1" {
    t.Skip("set GROK_QUOTA_LIVE=1")
  }
  path := detectUsageDBPath()
  if path == "" { t.Fatal("no db") }
  snap := buildSnapshot(path, time.Now().UTC())
  enc := json.NewEncoder(os.Stdout)
  enc.SetIndent("", "  ")
  // compact summary only
  out := map[string]any{
    "db": path,
    "error": snap.Error,
    "summary": snap.Summary,
    "accounts": len(snap.Accounts),
    "top3": func() []accountQuota {
      n := 3
      if len(snap.Accounts) < n { n = len(snap.Accounts) }
      return snap.Accounts[:n]
    }(),
    "cooldown_sample": func() []accountQuota {
      var xs []accountQuota
      for _, a := range snap.Accounts {
        if a.Health == "cooldown" {
          xs = append(xs, a)
          if len(xs) >= 3 { break }
        }
      }
      return xs
    }(),
  }
  _ = enc.Encode(out)
  if snap.Error != "" { t.Fatalf("error %s", snap.Error) }
  if snap.Summary.AccountCount == 0 { t.Fatal("no accounts") }
}
