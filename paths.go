package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// pathRoots returns candidate CPA install roots without hard-coded drive letters.
// Order: explicit env → process cwd → parent dirs → common relative layouts.
func pathRoots() []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		p = filepath.Clean(p)
		key := strings.ToLower(p)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, p)
	}

	for _, env := range []string{"CPA_ROOT", "CLIPROXY_ROOT", "CLIPROXYAPI_ROOT", "GROK_QUOTA_CPA_ROOT"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			add(v)
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		add(cwd)
		add(filepath.Join(cwd, "CPA"))
		add(filepath.Dir(cwd))
		add(filepath.Join(filepath.Dir(cwd), "CPA"))
		// plugin often runs with cwd = CPA root or plugins/<os>/<arch>
		add(filepath.Clean(filepath.Join(cwd, "..")))
		add(filepath.Clean(filepath.Join(cwd, "..", "..")))
		add(filepath.Clean(filepath.Join(cwd, "..", "..", "..")))
	}
	// relative placeholders resolved against cwd by callers
	add(".")
	return out
}

func firstExistingDir(candidates ...string) string {
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	return ""
}

func firstExistingFile(candidates ...string) string {
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return ""
}

func underRoots(parts ...string) []string {
	var out []string
	for _, root := range pathRoots() {
		out = append(out, filepath.Join(append([]string{root}, parts...)...))
	}
	// also bare relative
	out = append(out, filepath.Join(parts...))
	return out
}

// platformPluginsSubdir matches CPA layout: plugins/windows/amd64, plugins/linux/amd64, ...
func platformPluginsSubdir() string {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	switch goos {
	case "windows", "linux", "darwin":
		// ok
	default:
		goos = "linux"
	}
	return filepath.Join("plugins", goos, goarch)
}
