package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type liveAuth struct {
	Key       string // join key: auth_index if known, else file:/email:
	AuthIndex string
	Email     string
	AuthFile  string
	Disabled  bool
	Sub       string
	Present   bool
}

func detectAuthDir() string {
	if v := strings.TrimSpace(os.Getenv("GROK_QUOTA_AUTH_DIR")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("CPA_AUTH_DIR")); v != "" {
		return v
	}
	candidates := []string{
		filepath.Join("auths"),
		`E:\CPA\auths`,
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append([]string{
			filepath.Join(cwd, "auths"),
			filepath.Clean(filepath.Join(cwd, "..", "auths")),
			filepath.Clean(filepath.Join(cwd, "..", "CPA", "auths")),
		}, candidates...)
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	return filepath.Join("auths")
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// loadLiveAuthPool returns current xAI auth files as the membership source of truth.
// Deleted accounts disappear from this map and must not appear in the quota UI.
func loadLiveAuthPool(authDir string) (map[string]liveAuth, error) {
	out := map[string]liveAuth{}
	authDir = strings.TrimSpace(authDir)
	if authDir == "" || strings.EqualFold(authDir, "none") {
		return out, fmt.Errorf("auth dir empty")
	}
	entries, err := os.ReadDir(authDir)
	if err != nil {
		return out, err
	}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		// Only xAI auth files (CPA naming: xai-*.json or type=xai inside).
		path := filepath.Join(authDir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			continue
		}
		typ := strings.ToLower(strings.TrimSpace(fmt.Sprint(doc["type"])))
		if typ == "" {
			typ = strings.ToLower(strings.TrimSpace(fmt.Sprint(doc["provider"])))
		}
		if typ != "" && typ != "xai" && typ != "grok" {
			// Non-xAI auth file.
			if !strings.HasPrefix(strings.ToLower(name), "xai-") {
				continue
			}
		}
		if typ == "" && !strings.HasPrefix(strings.ToLower(name), "xai-") {
			continue
		}

		email := strings.TrimSpace(fmt.Sprint(doc["email"]))
		if email == "" || email == "<nil>" {
			email = deriveEmail(fmt.Sprint(doc["account"]), name)
		}
		sub := strings.TrimSpace(fmt.Sprint(doc["sub"]))
		if sub == "<nil>" {
			sub = ""
		}
		disabled := false
		switch v := doc["disabled"].(type) {
		case bool:
			disabled = v
		case string:
			disabled = strings.EqualFold(v, "true") || v == "1"
		}
		authIndex := strings.TrimSpace(fmt.Sprint(doc["auth_index"]))
		if authIndex == "" || authIndex == "<nil>" {
			authIndex = strings.TrimSpace(fmt.Sprint(doc["AuthIndex"]))
		}
		if authIndex == "" || authIndex == "<nil>" {
			// CPA runtime auth_index is often absent on disk; use stable file/email key.
			// Usage events still join later by email/auth_file.
			authIndex = "file:" + name
		}
		key := authIndex
		la := liveAuth{
			Key:       key,
			AuthIndex: authIndex,
			Email:     email,
			AuthFile:  name,
			Disabled:  disabled,
			Sub:       sub,
			Present:   true,
		}
		out[key] = la
		// Secondary indexes for usage join are built by caller via email/file maps.
	}
	return out, nil
}

func indexLiveAuths(pool map[string]liveAuth) (byEmail, byFile map[string]string) {
	byEmail = map[string]string{}
	byFile = map[string]string{}
	for key, a := range pool {
		if e := strings.ToLower(strings.TrimSpace(a.Email)); e != "" {
			byEmail[e] = key
		}
		if f := strings.ToLower(filepath.Base(strings.TrimSpace(a.AuthFile))); f != "" {
			byFile[f] = key
		}
	}
	return byEmail, byFile
}

func matchLiveKey(authIndex, email, authFile string, pool map[string]liveAuth, byEmail, byFile map[string]string) (string, bool) {
	authIndex = strings.TrimSpace(authIndex)
	if authIndex != "" {
		if _, ok := pool[authIndex]; ok {
			return authIndex, true
		}
		// usage may use real auth_index while disk uses file: key — try reverse via email/file.
	}
	if e := strings.ToLower(strings.TrimSpace(email)); e != "" {
		if k, ok := byEmail[e]; ok {
			return k, true
		}
	}
	if f := strings.ToLower(filepath.Base(strings.TrimSpace(authFile))); f != "" {
		if k, ok := byFile[f]; ok {
			return k, true
		}
		// also try without path
		if k, ok := byFile[strings.ToLower(f)]; ok {
			return k, true
		}
	}
	return "", false
}

func sortedLiveKeys(pool map[string]liveAuth) []string {
	keys := make([]string, 0, len(pool))
	for k := range pool {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
