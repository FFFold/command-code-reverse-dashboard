package credits

import (
	"math"
	"testing"
)

const realPayload = `{
  "credits": {
    "credits": {"belowThreshold": false, "creditThreshold": 0,
                "monthlyCredits": 9.679693662, "purchasedCredits": 1.5, "freeCredits": 0.25},
    "windowLimits": {"limited": true, "exceeded": null,
      "fiveHour": {"used": 0.105286556, "cap": 3, "exceeded": false, "resetAt": 1789116010242},
      "weekly":   {"used": 0.320306338, "cap": 6, "exceeded": true, "resetAt": 1789213109428}}
  },
  "subscriptions": {"success": true, "data": {"planId": "individual-go", "status": "active",
    "currentPeriodStart": "2026-09-05T11:30:04Z", "currentPeriodEnd": "2026-10-05T11:30:04Z"}}
}`

func TestParseRealPayload(t *testing.T) {
	snap, err := Parse([]byte(realPayload))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := snap.Balance(); math.Abs(got-11.429693662) > 1e-9 {
		t.Errorf("Balance = %v, want 11.429693662", got)
	}
	if snap.WindowLimits.FiveHour.Cap != 3 {
		t.Errorf("fiveHour cap = %v", snap.WindowLimits.FiveHour.Cap)
	}
	if !snap.WindowLimits.Weekly.Exceeded {
		t.Error("weekly expected exceeded")
	}
	if snap.WindowLimits.Weekly.ResetAt != 1789213109428 {
		t.Errorf("weekly resetAt = %d", snap.WindowLimits.Weekly.ResetAt)
	}
	if !snap.HasSub || snap.Subscription.PlanID != "individual-go" {
		t.Errorf("subscription = %+v hasSub=%v", snap.Subscription, snap.HasSub)
	}
}

// Balance must be monthly+purchased+free AND must NOT include window used.
func TestBalanceExcludesWindowUsed(t *testing.T) {
	raw := `{"credits":{"credits":{"monthlyCredits":9.5,"purchasedCredits":1,"freeCredits":0.5},
	           "windowLimits":{"weekly":{"used":99,"cap":100}}}}`
	snap, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := snap.Balance(); math.Abs(got-11.0) > 1e-9 {
		t.Errorf("Balance = %v, want 11.0 (window used must be excluded)", got)
	}
}

func TestParseMissingCreditsIsZero(t *testing.T) {
	snap, err := Parse([]byte(`{}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if snap.Balance() != 0 {
		t.Errorf("Balance = %v, want 0", snap.Balance())
	}
	if snap.HasSub {
		t.Error("HasSub should be false")
	}
}

func TestParseMalformedErrors(t *testing.T) {
	if _, err := Parse([]byte(`{not json`)); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}
