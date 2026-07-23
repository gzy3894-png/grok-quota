package main

import (
	"fmt"
	"strings"
	"time"
)

var chinaTZ = time.FixedZone("CST", 8*3600)

func tokensToM(n int64) float64 {
	return float64(n) / 1_000_000.0
}

func formatTokensM(n int64) string {
	return fmt.Sprintf("%.2fM", tokensToM(n))
}

func formatTimeCN(iso string) string {
	iso = strings.TrimSpace(iso)
	if iso == "" {
		return ""
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05",
	}
	var t time.Time
	var err error
	for _, layout := range layouts {
		t, err = time.Parse(layout, iso)
		if err == nil {
			return t.In(chinaTZ).Format("2006-01-02 15:04:05")
		}
	}
	// already local-ish raw
	return iso
}

func formatTimeCNFromTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(chinaTZ).Format("2006-01-02 15:04:05")
}

func healthLabelZH(health string) string {
	switch strings.ToLower(strings.TrimSpace(health)) {
	case "healthy":
		return "正常"
	case "cooldown", "quota_issue", "quota_exhausted":
		return "额度问题"
	case "soft_exhausted":
		// Deprecated compatibility label (not used for new marks).
		return "高用量(旧)"
	case "unknown", "":
		return "未知"
	default:
		return health
	}
}

func statusKindLabelZH(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "active":
		return "正常"
	case "disabled":
		return "已停用"
	case "quota_issue":
		return "额度问题"
	case "high_usage":
		return "高用量"
	default:
		return kind
	}
}

func reasonLabelZH(reason string) string {
	r := strings.TrimSpace(reason)
	if r == "" {
		return ""
	}
	low := strings.ToLower(r)
	// exact / contains map (longer first)
	pairs := []struct {
		key string
		zh  string
	}{
		{"subscription:free-usage-exhausted", "免费额度用尽"},
		{"personal-team-blocked:spending-limit", "个人团队消费限额"},
		{"free-usage-exhausted", "免费额度用尽"},
		{"spending-limit", "消费限额"},
		{"out of credits", "积分不足"},
		{"insufficient credits", "积分不足"},
		{"resource_exhausted", "资源耗尽"},
		{"resource exhausted", "资源耗尽"},
		{"quota exceeded", "额度超限"},
		{"quota_exceeded", "额度超限"},
		{"quota_exhausted", "额度耗尽"},
	}
	for _, p := range pairs {
		if low == p.key || strings.Contains(low, p.key) {
			return p.zh
		}
	}
	// keep original if unknown
	return r
}

func enrichAccountDisplay(a *accountQuota) {
	if a == nil {
		return
	}
	a.HealthLabel = healthLabelZH(a.Health)
	if a.StatusLabel == "" {
		a.StatusLabel = statusKindLabelZH(a.StatusKind)
	}
	a.ReasonLabel = reasonLabelZH(a.Reason)
	a.Tokens24hM = formatTokensM(a.Tokens24h)
	if a.LimitTokens != nil {
		a.LimitTokensM = formatTokensM(*a.LimitTokens)
	} else {
		a.LimitTokensM = "未知"
	}
	if a.Remaining != nil {
		a.RemainingM = formatTokensM(*a.Remaining)
	} else {
		a.RemainingM = "未知"
	}
	a.HistoricalTokensM = formatTokensM(a.HistoricalTokens)
	a.ReferenceTokensM = formatTokensM(a.ReferenceTokens)
	a.FailureAtCN = formatTimeCN(a.FailureAt)
	a.RecoverAtCN = formatTimeCN(a.RecoverAt)
	a.LastUsageAtCN = formatTimeCN(a.LastUsageAt)
	a.LimitObservedAtCN = formatTimeCN(a.LimitObservedAt)
	a.EmailMasked = maskEmailDisplay(a.Email)
	a.AuthFileMasked = maskAtAndAfter(a.AuthFile)
	if a.ActionHint == "" && a.SuggestDisable {
		a.ActionHint = "日志含额度错误，建议停用"
	}
}
