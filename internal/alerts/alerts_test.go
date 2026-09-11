package alerts

import "testing"

var th = Thresholds{WindowPercent: 80, LowCreditUSD: 5}

func TestNoAlertsWhenHealthy(t *testing.T) {
	got := Evaluate(Input{
		Balance: 50,
		Windows: []WindowUsage{{Name: "5h", Used: 1, Cap: 3}, {Name: "week", Used: 1, Cap: 6}},
		Health:  Health{Reachable: true, LivenessOK: true, ReadinessOK: true},
	}, th)
	if len(got) != 0 {
		t.Fatalf("expected no alerts, got %+v", got)
	}
}

func TestWindowWarningAtThreshold(t *testing.T) {
	got := Evaluate(Input{
		Balance: 50,
		Windows: []WindowUsage{{Name: "5h", Used: 2.4, Cap: 3}}, // 80%
		Health:  Health{Reachable: true, LivenessOK: true, ReadinessOK: true},
	}, th)
	if len(got) != 1 || got[0].Level != LevelWarning {
		t.Fatalf("expected one warning, got %+v", got)
	}
}

func TestWindowCriticalWhenExceeded(t *testing.T) {
	got := Evaluate(Input{
		Balance: 50,
		Windows: []WindowUsage{{Name: "week", Used: 6, Cap: 6, Exceeded: true}},
		Health:  Health{Reachable: true, LivenessOK: true, ReadinessOK: true},
	}, th)
	if len(got) != 1 || got[0].Level != LevelCritical {
		t.Fatalf("expected one critical, got %+v", got)
	}
}

func TestLowCreditWarningAndZeroCritical(t *testing.T) {
	low := Evaluate(Input{Balance: 3,
		Health: Health{Reachable: true, LivenessOK: true, ReadinessOK: true}}, th)
	if len(low) != 1 || low[0].Level != LevelWarning {
		t.Fatalf("expected low-credit warning, got %+v", low)
	}
	zero := Evaluate(Input{Balance: 0,
		Health: Health{Reachable: true, LivenessOK: true, ReadinessOK: true}}, th)
	if len(zero) != 1 || zero[0].Level != LevelCritical {
		t.Fatalf("expected critical for zero balance, got %+v", zero)
	}
}

func TestBelowThresholdAlert(t *testing.T) {
	got := Evaluate(Input{Balance: 50, BelowThreshold: true,
		Health: Health{Reachable: true, LivenessOK: true, ReadinessOK: true}}, th)
	if len(got) != 1 || got[0].Level != LevelWarning {
		t.Fatalf("expected below-threshold warning, got %+v", got)
	}
}

func TestHealthAlerts(t *testing.T) {
	unreachable := Evaluate(Input{Balance: 50, Health: Health{Reachable: false}}, th)
	if len(unreachable) != 1 || unreachable[0].Level != LevelCritical {
		t.Fatalf("expected unreachable critical, got %+v", unreachable)
	}
	notReady := Evaluate(Input{Balance: 50,
		Health: Health{Reachable: true, LivenessOK: true, ReadinessOK: false}}, th)
	if len(notReady) != 1 || notReady[0].Level != LevelWarning {
		t.Fatalf("expected not-ready warning, got %+v", notReady)
	}
}
