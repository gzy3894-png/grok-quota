package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	stateMu       sync.RWMutex
	cachedSnap    quotaSnapshot
	cachedAt      time.Time
	cacheTTL      = 5 * time.Second
	refreshEvery  = 10 * time.Second
	stateFilePath = defaultStatePath()
	stopRefresh   chan struct{}
	refreshOnce   sync.Once
)

func defaultStatePath() string {
	if v := strings.TrimSpace(os.Getenv("GROK_QUOTA_STATE_PATH")); v != "" {
		return v
	}
	// Prefer an existing plugins directory under CPA roots.
	for _, c := range underRoots("plugins", "grok-quota-state.json") {
		dir := filepath.Dir(c)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return c
		}
	}
	return filepath.Join("plugins", "grok-quota-state.json")
}

func startBackgroundRefresh() {
	refreshOnce.Do(func() {
		stopRefresh = make(chan struct{})
		// Warm cache immediately.
		_ = refreshSnapshot(true)
		go func() {
			ticker := time.NewTicker(refreshEvery)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					_ = refreshSnapshot(true)
				case <-stopRefresh:
					return
				}
			}
		}()
	})
}

func stopBackgroundRefresh() {
	if stopRefresh != nil {
		select {
		case <-stopRefresh:
		default:
			close(stopRefresh)
		}
	}
}

func getSnapshot(force bool) quotaSnapshot {
	stateMu.RLock()
	age := time.Since(cachedAt)
	snap := cachedSnap
	has := !cachedAt.IsZero()
	stateMu.RUnlock()
	if !force && has && age < cacheTTL && snap.Error == "" {
		return snap
	}
	return refreshSnapshot(force)
}

func refreshSnapshot(force bool) quotaSnapshot {
	stateMu.Lock()
	if !force && !cachedAt.IsZero() && time.Since(cachedAt) < cacheTTL && cachedSnap.Error == "" {
		snap := cachedSnap
		stateMu.Unlock()
		return snap
	}
	stateMu.Unlock()

	snap := computeLiveSnapshot()

	stateMu.Lock()
	cachedSnap = snap
	cachedAt = time.Now()
	stateMu.Unlock()

	_ = writeStateFile(snap)
	return snap
}

func parsePluginVersion(v string) (int, int, int, bool) {
	v = strings.TrimSpace(strings.TrimPrefix(v, "v"))
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return 0, 0, 0, false
	}
	var maj, min, pat int
	if _, err := fmt.Sscanf(parts[0], "%d", &maj); err != nil {
		return 0, 0, 0, false
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &min); err != nil {
		return 0, 0, 0, false
	}
	if len(parts) >= 3 {
		// tolerate suffixes like 1.6-display
		fmt.Sscanf(parts[2], "%d", &pat)
	}
	return maj, min, pat, true
}

func versionLess(a, b string) bool {
	am, an, ap, aok := parsePluginVersion(a)
	bm, bn, bp, bok := parsePluginVersion(b)
	if !aok || !bok {
		return a < b
	}
	if am != bm {
		return am < bm
	}
	if an != bn {
		return an < bn
	}
	return ap < bp
}

func writeStateFile(snap quotaSnapshot) error {
	path := stateFilePath
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	// Refuse to clobber a newer observer snapshot (hot-reload may leave an old
	// plugin goroutine alive briefly; it must not stomp the active version).
	if rawExisting, err := os.ReadFile(path); err == nil && len(rawExisting) > 0 {
		var existing struct {
			Version string `json:"version"`
			Plugin  string `json:"plugin"`
		}
		if json.Unmarshal(rawExisting, &existing) == nil {
			if strings.EqualFold(strings.TrimSpace(existing.Plugin), pluginName) &&
				existing.Version != "" && versionLess(snap.Version, existing.Version) {
				return nil
			}
		}
	}

	// Persist a lean copy: by_auth_index is useful for live API but doubles disk size
	// and is rebuilt by consumers from accounts[].
	disk := snap
	disk.ByAuthIndex = nil
	raw, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	// Unique temp avoids clobber races with external readers/writers on Windows.
	tmp := fmt.Sprintf("%s.%d.tmp", path, time.Now().UnixNano())
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		// Windows: destination exists — try replace via remove+rename.
		_ = os.Remove(path)
		if err2 := os.Rename(tmp, path); err2 != nil {
			_ = os.Remove(tmp)
			return err2
		}
	}
	return nil
}

func accountsByAuthIndex(snap quotaSnapshot) map[string]accountQuota {
	out := make(map[string]accountQuota, len(snap.Accounts))
	for _, a := range snap.Accounts {
		out[a.AuthIndex] = a
	}
	return out
}
