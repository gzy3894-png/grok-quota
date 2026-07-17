# Grok Quota

> [中文](README.md) | **English**

[![Release](https://img.shields.io/badge/release-v0.1.9-blue)](https://github.com/gzy3894-png/grok-quota/releases/tag/v0.1.9)
[![CPA Plugin](https://img.shields.io/badge/CLIProxyAPI-plugin-111827)](https://github.com/router-for-me/CLIProxyAPI)
[![Platform](https://img.shields.io/badge/platform-windows%20amd64-0f766e)](./README.en.md)
[![License](https://img.shields.io/badge/license-MIT-green)](./LICENSE)
[![LINUX DO](https://img.shields.io/badge/LINUX%20DO-community-0066cc)](https://linux.do/)

Grok Quota is a local plugin for [CLIProxyAPI (CPA)](https://github.com/router-for-me/CLIProxyAPI). It answers one practical question: **how much has each xAI / Grok credential used in the last rolling 24 hours**, and exposes that as a console plus JSON for panels, scripts, and operators.

In short: it is a **QUERY / observation** plugin, not a ban or kick tool.

## Why it exists

When you run many free-tier Grok accounts behind CPA, three things often collide:

- Panel lifetime totals and rough estimates are **not** the same as a rolling 24h window
- xAI does **not** expose a public remaining-quota API
- Auto-disable / inspection / ban plugins can blur “used a lot” with “actually quota-blocked”

Grok Quota reads local CPAMP `usage_events`, sums successful tokens over a rolling 24h window, optionally joins a global account-status bus for cooldown display, and writes a display snapshot for Grok Panel and other consumers.

The default local reference line is `2,000,000 tokens / 24h` (aligned with common free-tier rolling hints). **This is local observation policy, not an official balance API.**

## Community

This open-source project is linked with and acknowledges the [LINUX DO community](https://linux.do/).

Questions, install issues, and improvement ideas are welcome in GitHub Discussions:

```text
https://github.com/gzy3894-png/grok-quota/discussions
```

Thanks also to Linux Do community feedback around CPA / Grok operations.

## Ecosystem

| Component | Role |
| --- | --- |
| [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) | Host runtime; loads this plugin |
| CPAMP `usage.sqlite` | Usage source (`usage_events`, last 24h) |
| [cpa-plugin-grok-panel](https://github.com/TizenryA/cpa-plugin-grok-panel) | Display consumer; should render this plugin’s fields only |
| [grok-inspection](https://github.com/ywddd/grok-inspection) | Manual batch inspect / enable / disable |
| Local `account-status.json` (optional) | Global cooldown join for display |

### Boundaries

| Capability | This plugin |
| --- | --- |
| Observe rolling 24h usage | **Yes** |
| Write console / JSON / `grok-quota-state.json` | **Yes** (this file only) |
| Write auth `disabled=true` | **No** |
| Replace official xAI quota API | **No** |
| Auto-ban 401/403 | **No** (use a REALTIME plugin) |

## Install

### Option A: GitHub Release (recommended)

1. Open [Releases](https://github.com/gzy3894-png/grok-quota/releases)
2. Download the package for your platform (public release currently targets **Windows amd64**)
3. Place `grok-quota.dll` under the CPA plugins directory, for example:

```text
<CPA>/plugins/windows/amd64/grok-quota.dll
```

4. Enable in CPA config:

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    grok-quota:
      enabled: true
      priority: 50
```

5. Restart CPA, or hot-reload plugins if your build supports it.

### Option B: Build from source

Requires Go 1.21+, Windows amd64, and a working C toolchain (`CGO_ENABLED=1`):

```powershell
$env:CGO_ENABLED = '1'
pwsh -File .\build.ps1
```

Artifacts:

```text
dist/grok-quota.dll
dist/grok-quota-v0.1.8.dll
```

### Optional CPA store source

```yaml
plugins:
  store-sources:
    - "https://raw.githubusercontent.com/gzy3894-png/grok-quota/main/registry.json"
```

Whether the store UI can one-click install depends on your CPA version.

## Usage

After install, open **Grok Quota** in the CPA management UI, or call:

| Path | Description |
| --- | --- |
| `GET /v0/resource/plugins/grok-quota/status` | HTML console |
| `GET /v0/resource/plugins/grok-quota/data` | Full snapshot JSON |
| `GET /v0/resource/plugins/grok-quota/accounts` | Account list |
| `GET /v0/resource/plugins/grok-quota/summary` | Pool summary |

State file for Panel / scripts:

```text
<CPA>/plugins/grok-quota-state.json
```

## Field semantics

| Field | Meaning |
| --- | --- |
| `tokens_24h` / `quota_used` | Sum of successful `total_tokens` in last 24h |
| `limit_tokens` / `quota_limit` | Local reference limit, default `2_000_000` |
| `quota_remaining` | Remaining in the local window (observation only) |
| `health=cooldown` | Hard quota-related signal still in cooldown |
| `health=soft_exhausted` | Local rolling usage hit the reference line without hard failure; display only |
| `source` | `cpamp_usage_events_rolling_24h` |

Panel consumers should prefer `quota_*` / `cooldown_until` aliases and **must not** treat lifetime totals as the 2M/24h window.

## Optional environment variables

| Variable | Purpose |
| --- | --- |
| `GROK_QUOTA_CPAMP_DB` / `CPAMP_USAGE_DB` | Path to `usage.sqlite` |
| `GROK_QUOTA_AUTH_DIR` / `CPA_AUTH_DIR` | CPA auth directory |
| `GROK_QUOTA_STATE_PATH` | State file path |
| `GROK_QUOTA_GLOBAL_STATUS` / `CPA_ACCOUNT_STATUS` | Global `account-status.json` |

## Version

| Item | Value |
| --- | --- |
| Plugin name | `grok-quota` |
| Version | `0.1.9` |
| Role | QUERY (observe by default; optional auto-disable) |
| Primary platform | Windows amd64 (`.dll`) |
| License | MIT |

## Non-goals

- Not an official xAI remaining-quota API
- Does not write auth `disabled` or auto-kick accounts
- Does not upload accounts, secrets, or usage to third parties
- Not an official OpenAI / xAI / CLIProxyAPI release channel

## Thanks

- [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)
- [cpa-plugin-grok-panel](https://github.com/TizenryA/cpa-plugin-grok-panel)
- [grok-inspection](https://github.com/ywddd/grok-inspection)
- [LINUX DO community](https://linux.do/)

## License

MIT. See [LICENSE](./LICENSE).
