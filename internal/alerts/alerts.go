// Package alerts evaluates dashboard alert rules from the credit snapshot and
// proxy health.
package alerts

import "fmt"

// Level is an alert severity.
type Level string

const (
	LevelInfo     Level = "info"
	LevelWarning  Level = "warning"
	LevelCritical Level = "critical"
)

// Alert is one rendered alert.
type Alert struct {
	Level  Level  `json:"level"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// Thresholds tunes the rules.
type Thresholds struct {
	WindowPercent float64
	LowCreditUSD  float64
}

// WindowUsage is a rolling window expressed for rule evaluation.
type WindowUsage struct {
	Name     string
	Used     float64
	Cap      float64
	Exceeded bool
}

// Health captures proxy reachability.
type Health struct {
	Reachable   bool
	LivenessOK  bool
	ReadinessOK bool
}

// Input is the evaluation input.
type Input struct {
	Balance        float64
	BelowThreshold bool
	Windows        []WindowUsage
	Health         Health
}

// Evaluate returns all triggered alerts (empty when healthy).
func Evaluate(in Input, th Thresholds) []Alert {
	out := []Alert{}
	for _, w := range in.Windows {
		if w.Cap <= 0 {
			continue
		}
		pct := w.Used / w.Cap * 100
		switch {
		case w.Exceeded || pct >= 95:
			out = append(out, Alert{LevelCritical,
				fmt.Sprintf("%s 用量已达上限", w.Name),
				fmt.Sprintf("%.1f%% ($%.4f / $%.4f)", pct, w.Used, w.Cap)})
		case pct >= th.WindowPercent:
			out = append(out, Alert{LevelWarning,
				fmt.Sprintf("%s 用量接近上限", w.Name),
				fmt.Sprintf("%.1f%% ($%.4f / $%.4f)", pct, w.Used, w.Cap)})
		}
	}
	switch {
	case in.Balance <= 0:
		out = append(out, Alert{LevelCritical, "余额已耗尽", fmt.Sprintf("剩余 $%.4f", in.Balance)})
	case in.Balance <= th.LowCreditUSD:
		out = append(out, Alert{LevelWarning, "余额偏低",
			fmt.Sprintf("剩余 $%.4f（阈值 $%.4f）", in.Balance, th.LowCreditUSD)})
	}
	if in.BelowThreshold {
		out = append(out, Alert{LevelWarning, "账户低于阈值", "上游报告 belowThreshold=true"})
	}
	switch {
	case !in.Health.Reachable:
		out = append(out, Alert{LevelCritical, "代理不可达", "无法连接代理实例"})
	case !in.Health.LivenessOK:
		out = append(out, Alert{LevelCritical, "代理存活检查失败", "/healthz 未返回 200"})
	case !in.Health.ReadinessOK:
		out = append(out, Alert{LevelWarning, "代理未就绪", "/readyz 未返回 200"})
	}
	return out
}
