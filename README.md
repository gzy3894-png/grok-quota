# Grok Quota

> **中文** | [English](README.en.md)

[![Release](https://img.shields.io/badge/release-v0.1.9-blue)](https://github.com/gzy3894-png/grok-quota/releases/tag/v0.1.9)
[![CPA Plugin](https://img.shields.io/badge/CLIProxyAPI-plugin-111827)](https://github.com/router-for-me/CLIProxyAPI)
[![Platform](https://img.shields.io/badge/platform-windows%20amd64-0f766e)](./README.md)
[![License](https://img.shields.io/badge/license-MIT-green)](./LICENSE)
[![LINUX DO](https://img.shields.io/badge/LINUX%20DO-社区认可-0066cc)](https://linux.do/)

Grok Quota 是一个给 [CLIProxyAPI（CPA）](https://github.com/router-for-me/CLIProxyAPI) 用的本地插件。它做的事情很简单：**从 CPAMP 请求日志里，整理每个 xAI / Grok 账号近 24 小时的真实用量**，只在日志出现额度错误码时标记问题，并可选择是否自动停用。

一句话：它是「过滤额度日志」的观测插件；默认不踢号。2M 只是参考基线，不是硬上限。

## 它解决什么问题

很多人用 CPA 挂一批 Grok 免费账号时，会碰到这些问题：

- 面板里看到的累计用量、估算值，和「最近 24 小时滚动量」不是一回事
- 上游 xAI **没有公开的剩余额度 API**，没法直接问官方「还剩多少」
- 一旦再叠加上自动禁用、巡检、封禁插件，很容易分不清「只是用得多」还是「真的被额度卡了」

Grok Quota 专门读本地 [CPAMP](https://github.com/router-for-me/CLIProxyAPI) / 用量库里的 `usage_events`，按 **滚动 24 小时**汇总成功请求的 token，并可选地合并全局账号状态总线（如 `account-status.json`）里的冷却信息，给面板做只读展示。

默认把 `2,000,000 tokens / 24h` 当作**参考基线**。若账号真实用量到了 3M，动态上限就是 3M，进度条与统计都会如实显示——**绝不会因为到了 2M 就当成「本地已满」或停止记录。**

**额度问题只认日志**：`free-usage-exhausted` / `spending-limit` 等错误码。可选开关「自动停用额度问题账号」（默认关）。

## 社区

本开源项目已链接并认可 [LINUX DO 社区](https://linux.do/)。

使用方法、安装排错和改进建议，欢迎在 GitHub Discussions 交流：

```text
https://github.com/gzy3894-png/grok-quota/discussions
```

也感谢 Linux Do 社区对 CPA / Grok 运维实践与反馈的支持。

## 和谁配合

| 组件 | 关系 |
| --- | --- |
| [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) | 宿主；本插件以 CPA 原生插件形式加载 |
| CPAMP `usage.sqlite` | 用量数据源（`usage_events` 近 24h） |
| [cpa-plugin-grok-panel](https://github.com/TizenryA/cpa-plugin-grok-panel) | 展示侧；应只读本插件输出，不要自己重算 2M/24h |
| [grok-inspection](https://github.com/ywddd/grok-inspection) | 人工批量验号 / 启停 |
| 本机 `account-status.json`（可选） | 全局冷却状态 join，仅展示 |

### 角色边界（重要）

| 能力 | 本插件是否做 |
| --- | --- |
| 查滚动 24h 用量 | **是** |
| 写控制台 / JSON / `grok-quota-state.json` | **是**（仅本文件） |
| 写 auth `disabled=true` | **默认否**；可选自动停用（仅日志额度证据） |
| 替代 xAI 官方额度 API | **否** |
| 自动封禁 401/403 | **否**（请用其它 REALTIME 插件） |

## 安装

### 方式一：下载 Release（推荐）

1. 打开 [Releases](https://github.com/gzy3894-png/grok-quota/releases)
2. 下载与平台匹配的包（当前公开发布以 **Windows amd64** 为主）
3. 将 `grok-quota.dll` 放到 CPA 插件目录，例如：

```text
<CPA>/plugins/windows/amd64/grok-quota.dll
```

4. 在 CPA 配置中启用：

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    grok-quota:
      enabled: true
      # 可选：数值越小，人工路径优先级越高；QUERY 角色建议 50 左右
      priority: 50
```

5. 重启 CPA，或按你环境允许的方式热加载插件。

### 方式二：从源码构建

需要 Go 1.21+、Windows amd64，以及可用的 C 编译器（`CGO_ENABLED=1`）：

```powershell
$env:CGO_ENABLED = '1'
pwsh -File .\build.ps1
```

产物：

```text
dist/grok-quota.dll
dist/grok-quota-v0.1.8.dll
```

复制到 CPA 插件目录即可。

### CPA 插件源（可选）

若你的 CPA 支持 `plugins.store-sources`，可加入本仓库的 registry：

```yaml
plugins:
  store-sources:
    - "https://raw.githubusercontent.com/gzy3894-png/grok-quota/main/registry.json"
```

> 是否出现在商店 UI、是否支持一键安装，取决于你使用的 CPA 版本与商店实现；失败时请改用 Release 手工安装。

## 使用

安装并启用后，在 CPA 管理端可打开菜单 **Grok Quota**，或直接访问资源接口：

| 路径 | 说明 |
| --- | --- |
| `GET /v0/resource/plugins/grok-quota/status` | HTML 控制台 |
| `GET /v0/resource/plugins/grok-quota/data` | 完整快照 JSON |
| `GET /v0/resource/plugins/grok-quota/accounts` | 账号列表 |
| `GET /v0/resource/plugins/grok-quota/summary` | 池子汇总 |

状态文件（供 Panel / 脚本 join）：

```text
<CPA>/plugins/grok-quota-state.json
```

## 字段语义

| 字段 | 含义 |
| --- | --- |
| `tokens_24h` / `quota_used` | 近 24h 成功请求的 `total_tokens` 合计 |
| `limit_tokens` / `quota_limit` | 本地观测上限，默认 `2_000_000` |
| `quota_remaining` | 本地窗口剩余量（观测值） |
| `health=cooldown` | 日志出现额度错误码且仍在恢复窗口 → 标记「额度问题」，建议停用 |
| `over_reference` | 真实用量超过参考基线，**不等于**额度耗尽 |
| `suggest_disable` | 有日志额度证据且账号仍启用 |
| `limit_tokens` | 动态上限 = max(参考基线, 真实用量) |
| `source` | `cpamp_usage_events_rolling_24h` |

Panel 侧应优先使用 `quota_*` / `cooldown_until` 等别名字段，**不要**把历史累计 token 当成 2M/24h。

## 环境变量（可选）

| 变量 | 作用 |
| --- | --- |
| `GROK_QUOTA_CPAMP_DB` / `CPAMP_USAGE_DB` | 指定 `usage.sqlite` 路径 |
| `GROK_QUOTA_AUTH_DIR` / `CPA_AUTH_DIR` | 指定 CPA auth 目录 |
| `GROK_QUOTA_STATE_PATH` | 指定状态文件路径 |
| `GROK_QUOTA_GLOBAL_STATUS` / `CPA_ACCOUNT_STATUS` | 指定全局 `account-status.json` |

未设置时，会按常见相对路径与本机候选路径自动探测。

## 当前版本

| 项目 | 值 |
| --- | --- |
| 插件名 | `grok-quota` |
| 版本 | `0.1.9` |
| 角色 | QUERY（默认只读；可选自动停用） |
| 主要平台 | Windows amd64（`.dll`） |
| 许可证 | MIT |

## 非目标

- 不替代 xAI 官方额度查询
- 不写 auth `disabled`、不做自动踢号
- 不上传账号、密钥或用量到第三方
- 不是 OpenAI / xAI / CLIProxyAPI 官方维护渠道

## 致谢

- [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) — CPA 宿主与插件 ABI
- [cpa-plugin-grok-panel](https://github.com/TizenryA/cpa-plugin-grok-panel) — 面板展示侧参考
- [grok-inspection](https://github.com/ywddd/grok-inspection) — 巡检插件协作边界
- [LINUX DO 社区](https://linux.do/) — 社区认可与使用反馈

## License

MIT。见 [LICENSE](./LICENSE)。
