// Package credits parses the proxy /v1/credits response envelope and computes
// the remaining balance.
package credits

import (
	"encoding/json"
	"fmt"
)

// Credits is the inner credits object.
type Credits struct {
	BelowThreshold   bool    `json:"belowThreshold"`
	CreditThreshold  float64 `json:"creditThreshold"`
	MonthlyCredits   float64 `json:"monthlyCredits"`
	PurchasedCredits float64 `json:"purchasedCredits"`
	FreeCredits      float64 `json:"freeCredits"`
}

// Window is one rolling rate-limit window.
type Window struct {
	Used     float64 `json:"used"`
	Cap      float64 `json:"cap"`
	Exceeded bool    `json:"exceeded"`
	ResetAt  int64   `json:"resetAt"` // epoch milliseconds
}

// WindowLimits groups the rolling windows.
type WindowLimits struct {
	Limited  bool   `json:"limited"`
	FiveHour Window `json:"fiveHour"`
	Weekly   Window `json:"weekly"`
}

// Subscription is the billing subscription summary.
type Subscription struct {
	PlanID             string `json:"planId"`
	Status             string `json:"status"`
	CurrentPeriodStart string `json:"currentPeriodStart"`
	CurrentPeriodEnd   string `json:"currentPeriodEnd"`
}

// Snapshot is the parsed /v1/credits data.
type Snapshot struct {
	Credits      Credits
	WindowLimits WindowLimits
	Subscription Subscription
	HasSub       bool
}

// Balance is the remaining spendable credits. Window usage is already
// reflected inside MonthlyCredits and must not be added here.
func (s *Snapshot) Balance() float64 {
	return s.Credits.MonthlyCredits + s.Credits.PurchasedCredits + s.Credits.FreeCredits
}

// Parse decodes the proxy /v1/credits envelope. Missing fields decode as zero.
func Parse(raw []byte) (*Snapshot, error) {
	var envelope struct {
		Credits struct {
			Credits      Credits      `json:"credits"`
			WindowLimits WindowLimits `json:"windowLimits"`
		} `json:"credits"`
		Subscriptions struct {
			Data *Subscription `json:"data"`
		} `json:"subscriptions"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("credits: decode response: %w", err)
	}
	snap := &Snapshot{
		Credits:      envelope.Credits.Credits,
		WindowLimits: envelope.Credits.WindowLimits,
	}
	if envelope.Subscriptions.Data != nil {
		snap.Subscription = *envelope.Subscriptions.Data
		snap.HasSub = true
	}
	return snap, nil
}
