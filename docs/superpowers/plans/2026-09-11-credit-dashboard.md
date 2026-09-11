# Credit Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a zero-dependency Go dashboard that visualizes a `commandcode-proxy` account's key balance, rate-limit windows, alerts, Prometheus metrics, and health/version.

**Architecture:** A single Go binary serves an embedded vanilla-JS frontend and two JSON endpoints (`/api/summary`, `/api/metrics`). The backend holds the proxy API key, calls the proxy's `/v1/credits`, `/version`, `/healthz`, `/readyz`, `/metrics`, aggregates them, and degrades per-source on failure.

**Tech Stack:** Go 1.27 standard library (`net/http`, `encoding/json`, `embed`, `log/slog`) + vanilla HTML/CSS/JS. No third-party dependencies. Module path `github.com/FFFold/command-code-reverse-dashboard`.

**Design reference:** `docs/superpowers/specs/2026-09-11-credit-dashboard-design.md`.

---

## Data contract (verified against live proxy)

`GET {proxy}/v1/credits` (Bearer) returns a two-level nested envelope:

```json
{
  "credits": {
    "credits": { "belowThreshold": false, "creditThreshold": 0,
                 "monthlyCredits": 9.679693662, "purchasedCredits": 0, "freeCredits": 0 },
    "windowLimits": { "limited": true,
      "fiveHour": { "used": 0.105286556, "cap": 3, "exceeded": false, "resetAt": 1789116010242 },
      "weekly":   { "used": 0.320306338, "cap": 6, "exceeded": false, "resetAt": 1789213109428 } }
  },
  "subscriptions": { "success": true, "data": { "planId": "individual-go", "status": "active",
                     "currentPeriodStart": "2026-09-05T11:30:04Z", "currentPeriodEnd": "2026-10-05T11:30:04Z" } }
}
```

- `monthlyCredits` = monthly **remaining** (already net of usage).
- **Balance = `monthlyCredits + purchasedCredits + freeCredits`** (window `used` is already inside `monthlyCredits`; never add it again).
- `resetAt` is epoch **milliseconds**.

## File structure

```
command-code-reverse-dashboard/
├── go.mod
├── cmd/credit-dashboard/main.go        # entrypoint: config, wiring, graceful shutdown
├── internal/
│   ├── config/config.go                # env/.env loading + validation
│   ├── credits/parse.go                # /v1/credits envelope parsing + balance
│   ├── promparse/parse.go              # Prometheus text -> typed summary
│   ├── alerts/alerts.go                # threshold + health alert rules
│   ├── upstream/client.go              # proxy HTTP client
│   └── api/server.go                   # HTTP routes + JSON handlers
├── web/
│   ├── embed.go                        # //go:embed of static files
│   ├── index.html
│   ├── app.js
│   └── style.css
├── .env.example
└── README.md
```

Each `internal` package has one responsibility and its own `_test.go`. `api` depends on interfaces, not concrete client, for testability.

---

### Task 1: Module skeleton

**Files:**
- Create: `go.mod`

- [ ] **Step 1: Initialize the Go module**

Run (from repo root):
```
go mod init github.com/FFFold/command-code-reverse-dashboard
```
Expected: `go.mod` created containing `module github.com/FFFold/command-code-reverse-dashboard` and `go 1.27`.

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: no output (no packages yet is fine, exit 0).

- [ ] **Step 3: Commit**

```
git add go.mod
git commit -m "chore: initialize go module"
```

---

### Task 2: config package

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

`internal/config/config_test.go`:
```go
package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PROXY_API_KEY", "secret")
	t.Setenv("PROXY_BASE_URL", "")
	t.Setenv("HOST", "")
	t.Setenv("PORT", "")
	t.Setenv("ALERT_WINDOW_PERCENT", "")
	t.Setenv("ALERT_LOW_CREDIT_USD", "")
	t.Setenv("UPSTREAM_TIMEOUT_SECONDS", "")
	t.Setenv("LOG_LEVEL", "")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.ProxyBaseURL != "http://localhost:3050" {
		t.Errorf("ProxyBaseURL = %q", c.ProxyBaseURL)
	}
	if c.Port != 8787 {
		t.Errorf("Port = %d", c.Port)
	}
	if c.AlertWindowPercent != 80 {
		t.Errorf("AlertWindowPercent = %v", c.AlertWindowPercent)
	}
	if c.AlertLowCreditUSD != 5 {
		t.Errorf("AlertLowCreditUSD = %v", c.AlertLowCreditUSD)
	}
	if c.Addr() != "0.0.0.0:8787" {
		t.Errorf("Addr = %q", c.Addr())
	}
}

func TestLoadRequiresAPIKey(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PROXY_API_KEY", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when PROXY_API_KEY missing")
	}
}

func TestLoadRejectsBadPort(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PROXY_API_KEY", "secret")
	t.Setenv("PORT", "70000")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for out-of-range port")
	}
}

func TestLoadRejectsBadWindowPercent(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PROXY_API_KEY", "secret")
	t.Setenv("ALERT_WINDOW_PERCENT", "150")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for window percent > 100")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/`
Expected: FAIL — `undefined: Load` / package build error.

- [ ] **Step 3: Write minimal implementation**

`internal/config/config.go`:
```go
// Package config loads dashboard configuration from environment variables
// (optionally seeded from a .env file). Real environment variables win.
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration.
type Config struct {
	ProxyBaseURL       string
	ProxyAPIKey        string
	Host               string
	Port               int
	AlertWindowPercent float64
	AlertLowCreditUSD  float64
	UpstreamTimeout    time.Duration
	LogLevel           string
}

// Load reads configuration from the environment, seeding missing keys from a
// .env file in the working directory when present.
func Load() (*Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return nil, err
	}
	c := &Config{
		ProxyBaseURL:       getEnv("PROXY_BASE_URL", "http://localhost:3050"),
		ProxyAPIKey:        os.Getenv("PROXY_API_KEY"),
		Host:               getEnv("HOST", "0.0.0.0"),
		Port:               getEnvInt("PORT", 8787),
		AlertWindowPercent: getEnvFloat("ALERT_WINDOW_PERCENT", 80),
		AlertLowCreditUSD:  getEnvFloat("ALERT_LOW_CREDIT_USD", 5),
		UpstreamTimeout:    time.Duration(getEnvInt("UPSTREAM_TIMEOUT_SECONDS", 10)) * time.Second,
		LogLevel:           getEnv("LOG_LEVEL", "info"),
	}
	return c, c.Validate()
}

// Validate enforces fail-fast invariants at startup.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.ProxyAPIKey) == "" {
		return fmt.Errorf("config: PROXY_API_KEY is required")
	}
	if strings.TrimSpace(c.ProxyBaseURL) == "" {
		return fmt.Errorf("config: PROXY_BASE_URL must not be empty")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("config: PORT out of range: %d", c.Port)
	}
	if c.AlertWindowPercent < 0 || c.AlertWindowPercent > 100 {
		return fmt.Errorf("config: ALERT_WINDOW_PERCENT must be 0..100, got %v", c.AlertWindowPercent)
	}
	if c.AlertLowCreditUSD < 0 {
		return fmt.Errorf("config: ALERT_LOW_CREDIT_USD must be >= 0")
	}
	return nil
}

// Addr returns the listen address.
func (c *Config) Addr() string { return fmt.Sprintf("%s:%d", c.Host, c.Port) }

func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("config: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if k != "" && os.Getenv(k) == "" {
			_ = os.Setenv(k, v)
		}
	}
	return sc.Err()
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n
		}
	}
	return fallback
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/`
Expected: PASS (ok).

- [ ] **Step 5: Commit**

```
git add internal/config/
git commit -m "feat: add config loading"
```

---

### Task 3: credits package

**Files:**
- Create: `internal/credits/parse.go`
- Test: `internal/credits/parse_test.go`

- [ ] **Step 1: Write the failing test**

`internal/credits/parse_test.go`:
```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/credits/`
Expected: FAIL — `undefined: Parse`.

- [ ] **Step 3: Write minimal implementation**

`internal/credits/parse.go`:
```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/credits/`
Expected: PASS.

- [ ] **Step 5: Commit**

```
git add internal/credits/
git commit -m "feat: parse credits envelope and compute balance"
```

---

### Task 4: promparse package

**Files:**
- Create: `internal/promparse/parse.go`
- Test: `internal/promparse/parse_test.go`

- [ ] **Step 1: Write the failing test**

`internal/promparse/parse_test.go`:
```go
package promparse

import (
	"math"
	"testing"
)

const realMetrics = `# HELP commandcode_proxy_requests_total Chat completion requests by model/stream/result.
# TYPE commandcode_proxy_requests_total counter
commandcode_proxy_requests_total{model="deepseek/deepseek-v4-flash",stream="false",result="ok"} 2
commandcode_proxy_requests_total{model="deepseek/deepseek-v4.1-flash",stream="false",result="ok"} 10
commandcode_proxy_requests_total{model="deepseek/deepseek-v4.1-flash",stream="true",result="ok"} 30
commandcode_proxy_requests_total{model="deepseek/deepseek-v4.1-flash",stream="true",result="timeout"} 1
commandcode_proxy_requests_total{model="moonshotai/Kimi-K2.6",stream="false",result="ok"} 1
# HELP commandcode_proxy_tokens_total Tokens served, by kind (input/output/cached).
# TYPE commandcode_proxy_tokens_total counter
commandcode_proxy_tokens_total{kind="cached"} 1468544
commandcode_proxy_tokens_total{kind="input"} 1619633
commandcode_proxy_tokens_total{kind="output"} 37506
# HELP commandcode_proxy_cost_microdollars_total Upstream cost in micro-dollars, by model.
# TYPE commandcode_proxy_cost_microdollars_total counter
commandcode_proxy_cost_microdollars_total{model="deepseek/deepseek-v4-flash"} 480
commandcode_proxy_cost_microdollars_total{model="deepseek/deepseek-v4.1-flash"} 88892
commandcode_proxy_cost_microdollars_total{model="moonshotai/Kimi-K2.6"} 18748
# HELP commandcode_proxy_latency_ms End-to-end request latency (milliseconds).
# TYPE commandcode_proxy_latency_ms summary
commandcode_proxy_latency_ms_count{model="deepseek/deepseek-v4.1-flash",stream="true"} 30
commandcode_proxy_latency_ms_sum{model="deepseek/deepseek-v4.1-flash",stream="true"} 210045
# An unknown metric must be ignored.
some_other_metric{foo="bar"} 123
`

func TestParseRealMetrics(t *testing.T) {
	s, err := Parse(realMetrics)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.RequestTotal != 44 {
		t.Errorf("RequestTotal = %v, want 44", s.RequestTotal)
	}
	if s.TokenTotals["input"] != 1619633 || s.TokenTotals["cached"] != 1468544 || s.TokenTotals["output"] != 37506 {
		t.Errorf("TokenTotals = %+v", s.TokenTotals)
	}
	if got := s.CostMicroByModel["deepseek/deepseek-v4-flash"]; got != 480 {
		t.Errorf("cost flash = %v", got)
	}
	if len(s.Latency) != 1 {
		t.Fatalf("Latency len = %d, want 1", len(s.Latency))
	}
	if s.Latency[0].Count != 30 || s.Latency[0].SumMs != 210045 {
		t.Errorf("Latency = %+v", s.Latency[0])
	}
}

func TestParseSkipsCommentsAndBlanks(t *testing.T) {
	s, err := Parse("\n# a comment\n\n")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.RequestTotal != 0 || len(s.Requests) != 0 {
		t.Errorf("expected empty summary, got %+v", s)
	}
}

func TestParseMalformedValueErrors(t *testing.T) {
	_, err := Parse(`commandcode_proxy_tokens_total{kind="input"} notanumber`)
	if err == nil {
		t.Fatal("expected error for non-numeric value")
	}
}

func TestParseLabelWithSlashValue(t *testing.T) {
	s, err := Parse(`commandcode_proxy_requests_total{model="moonshotai/Kimi-K2.6",stream="false",result="ok"} 5`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Requests[0].Model != "moonshotai/Kimi-K2.6" || s.Requests[0].Count != 5 {
		t.Errorf("Requests[0] = %+v", s.Requests[0])
	}
	if math.Abs(s.RequestTotal-5) > 1e-9 {
		t.Errorf("RequestTotal = %v", s.RequestTotal)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/promparse/`
Expected: FAIL — `undefined: Parse`.

- [ ] **Step 3: Write minimal implementation**

`internal/promparse/parse.go`:
```go
// Package promparse parses the Prometheus text exposition format emitted by
// the proxy /metrics endpoint into structured summaries.
package promparse

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// RequestStat is a request count keyed by model/stream/result.
type RequestStat struct {
	Model  string  `json:"model"`
	Stream string  `json:"stream"`
	Result string  `json:"result"`
	Count  float64 `json:"count"`
}

// LatencyStat pairs count and sum for one model/stream label set.
type LatencyStat struct {
	Model  string  `json:"model"`
	Stream string  `json:"stream"`
	Count  float64 `json:"count"`
	SumMs  float64 `json:"sumMs"`
}

// Summary is the aggregated metrics view.
type Summary struct {
	Requests         []RequestStat
	RequestTotal     float64
	TokenTotals      map[string]float64
	CostMicroByModel map[string]float64
	Latency          []LatencyStat
	UpstreamErrors   map[string]float64
}

// Parse reads the exposition text, ignoring unknown metrics.
func Parse(text string) (*Summary, error) {
	s := &Summary{
		TokenTotals:      map[string]float64{},
		CostMicroByModel: map[string]float64{},
		UpstreamErrors:   map[string]float64{},
	}
	latency := map[string]*LatencyStat{}

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sp := strings.LastIndexByte(line, ' ')
		if sp < 0 {
			continue
		}
		left := strings.TrimSpace(line[:sp])
		valStr := strings.TrimSpace(line[sp+1:])
		val, err := strconv.ParseFloat(valStr, 64)
		if err != nil {
			return nil, fmt.Errorf("promparse: bad value %q: %w", valStr, err)
		}

		name := left
		labels := map[string]string{}
		if i := strings.IndexByte(left, '{'); i >= 0 {
			name = left[:i]
			labels = parseLabels(left[i:])
		}

		switch name {
		case "commandcode_proxy_requests_total":
			s.Requests = append(s.Requests, RequestStat{
				Model: labels["model"], Stream: labels["stream"], Result: labels["result"], Count: val,
			})
			s.RequestTotal += val
		case "commandcode_proxy_tokens_total":
			s.TokenTotals[labels["kind"]] += val
		case "commandcode_proxy_cost_microdollars_total":
			s.CostMicroByModel[labels["model"]] += val
		case "commandcode_proxy_upstream_errors_total":
			s.UpstreamErrors[labels["class"]] += val
		case "commandcode_proxy_latency_ms_count", "commandcode_proxy_latency_ms_sum":
			key := labels["model"] + "\x00" + labels["stream"]
			st := latency[key]
			if st == nil {
				st = &LatencyStat{Model: labels["model"], Stream: labels["stream"]}
				latency[key] = st
			}
			if name == "commandcode_proxy_latency_ms_count" {
				st.Count += val
			} else {
				st.SumMs += val
			}
		}
	}

	for _, st := range latency {
		s.Latency = append(s.Latency, *st)
	}
	sort.Slice(s.Requests, func(i, j int) bool {
		a, b := s.Requests[i], s.Requests[j]
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		if a.Stream != b.Stream {
			return a.Stream < b.Stream
		}
		return a.Result < b.Result
	})
	sort.Slice(s.Latency, func(i, j int) bool {
		a, b := s.Latency[i], s.Latency[j]
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return a.Stream < b.Stream
	})
	return s, nil
}

// parseLabels parses `{k="v",...}`; commas inside quotes are preserved.
func parseLabels(s string) map[string]string {
	out := map[string]string{}
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")
	if s == "" {
		return out
	}
	var parts []string
	var b strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			b.WriteRune(r)
		case r == ',' && !inQuote:
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	parts = append(parts, b.String())
	for _, p := range parts {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/promparse/`
Expected: PASS.

- [ ] **Step 5: Commit**

```
git add internal/promparse/
git commit -m "feat: parse prometheus metrics text"
```

---

### Task 5: alerts package

**Files:**
- Create: `internal/alerts/alerts.go`
- Test: `internal/alerts/alerts_test.go`

- [ ] **Step 1: Write the failing test**

`internal/alerts/alerts_test.go`:
```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/alerts/`
Expected: FAIL — `undefined: Evaluate`.

- [ ] **Step 3: Write minimal implementation**

`internal/alerts/alerts.go`:
```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/alerts/`
Expected: PASS.

- [ ] **Step 5: Commit**

```
git add internal/alerts/
git commit -m "feat: add alert rule evaluation"
```

---

### Task 6: upstream client

**Files:**
- Create: `internal/upstream/client.go`
- Test: `internal/upstream/client_test.go`

- [ ] **Step 1: Write the failing test**

`internal/upstream/client_test.go`:
```go
package upstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCreditsSendsBearerAndReturnsBody(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"credits":{"credits":{"monthlyCredits":9.5}}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "secret-key", 5*time.Second)
	body, err := c.Credits(context.Background())
	if err != nil {
		t.Fatalf("Credits: %v", err)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if len(body) == 0 {
		t.Error("empty body")
	}
}

func TestCreditsNon2xxErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("bad key"))
	}))
	defer srv.Close()

	c := New(srv.URL, "secret", 5*time.Second)
	if _, err := c.Credits(context.Background()); err == nil {
		t.Fatal("expected error on 401")
	}
}

func TestVersionParsesJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ccVersion":"1.53.0","version":"v0.1.6"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "secret", 5*time.Second)
	v, err := c.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v.Version != "v0.1.6" || v.CCVersion != "1.53.0" {
		t.Errorf("Version = %+v", v)
	}
}

func TestMetricsReturnsText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("commandcode_proxy_requests_total{model=\"m\",stream=\"false\",result=\"ok\"} 1\n"))
	}))
	defer srv.Close()

	c := New(srv.URL, "secret", 5*time.Second)
	text, err := c.Metrics(context.Background())
	if err != nil {
		t.Fatalf("Metrics: %v", err)
	}
	if len(text) == 0 {
		t.Error("empty metrics")
	}
}

func TestHealthStatuses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "secret", 5*time.Second)
	h, err := c.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !h.Reachable || !h.LivenessOK || h.ReadinessOK {
		t.Errorf("Health = %+v", h)
	}
}

func TestHealthUnreachableErrors(t *testing.T) {
	c := New("http://127.0.0.1:1", "secret", 200*time.Millisecond)
	if _, err := c.Health(context.Background()); err == nil {
		t.Fatal("expected error when unreachable")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/upstream/`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write minimal implementation**

`internal/upstream/client.go`:
```go
// Package upstream is a minimal HTTP client for the commandcode-proxy
// observability endpoints.
package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to the proxy's unauthenticated-observability and credits routes.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New builds a client. The API key is only sent to /v1/credits.
func New(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: timeout},
	}
}

// Version is the proxy /version payload.
type Version struct {
	Version   string `json:"version"`
	CCVersion string `json:"ccVersion"`
}

// Health is the combined /healthz + /readyz result.
type Health struct {
	Reachable       bool
	LivenessOK      bool
	ReadinessOK     bool
	LivenessStatus  int
	ReadinessStatus int
}

// Credits fetches the raw /v1/credits body.
func (c *Client) Credits(ctx context.Context) ([]byte, error) {
	status, body, err := c.get(ctx, "/v1/credits", true)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("upstream: /v1/credits returned %d: %s", status, snippet(body))
	}
	return body, nil
}

// Version fetches /version.
func (c *Client) Version(ctx context.Context) (Version, error) {
	var v Version
	status, body, err := c.get(ctx, "/version", false)
	if err != nil {
		return v, err
	}
	if status < 200 || status >= 300 {
		return v, fmt.Errorf("upstream: /version returned %d", status)
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return v, fmt.Errorf("upstream: decode /version: %w", err)
	}
	return v, nil
}

// Metrics fetches the raw /metrics text.
func (c *Client) Metrics(ctx context.Context) (string, error) {
	status, body, err := c.get(ctx, "/metrics", false)
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("upstream: /metrics returned %d", status)
	}
	return string(body), nil
}

// Health probes /healthz and /readyz. It errors only on transport failure.
func (c *Client) Health(ctx context.Context) (Health, error) {
	var h Health
	ls, _, lerr := c.get(ctx, "/healthz", false)
	rs, _, rerr := c.get(ctx, "/readyz", false)
	if lerr != nil && rerr != nil {
		return h, fmt.Errorf("upstream: health probe: %v; %v", lerr, rerr)
	}
	h.Reachable = true
	h.LivenessStatus = ls
	h.ReadinessStatus = rs
	h.LivenessOK = ls == http.StatusOK
	h.ReadinessOK = rs == http.StatusOK
	return h, nil
}

func (c *Client) get(ctx context.Context, path string, auth bool) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("upstream: build %s request: %w", path, err)
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("upstream: %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("upstream: read %s: %w", path, err)
	}
	return resp.StatusCode, body, nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/upstream/`
Expected: PASS.

- [ ] **Step 5: Commit**

```
git add internal/upstream/
git commit -m "feat: add proxy upstream client"
```

---

### Task 7: api handlers

**Files:**
- Create: `internal/api/server.go`
- Test: `internal/api/server_test.go`

- [ ] **Step 1: Write the failing test**

`internal/api/server_test.go`:
```go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/FFFold/command-code-reverse-dashboard/internal/alerts"
	"github.com/FFFold/command-code-reverse-dashboard/internal/upstream"
)

type fakeUpstream struct {
	creditsRaw []byte
	creditsErr error
	version    upstream.Version
	versionErr error
	health     upstream.Health
	healthErr  error
	metrics    string
	metricsErr error
}

func (f *fakeUpstream) Credits(context.Context) ([]byte, error) { return f.creditsRaw, f.creditsErr }
func (f *fakeUpstream) Version(context.Context) (upstream.Version, error) {
	return f.version, f.versionErr
}
func (f *fakeUpstream) Health(context.Context) (upstream.Health, error) { return f.health, f.healthErr }
func (f *fakeUpstream) Metrics(context.Context) (string, error)        { return f.metrics, f.metricsErr }

const creditsBody = `{
  "credits": {"credits": {"monthlyCredits": 9.5, "purchasedCredits": 1, "freeCredits": 0.5, "belowThreshold": false},
              "windowLimits": {"fiveHour": {"used": 1, "cap": 3}, "weekly": {"used": 6, "cap": 6, "exceeded": true}}},
  "subscriptions": {"data": {"planId": "individual-go", "status": "active"}}
}`

const metricsBody = `commandcode_proxy_requests_total{model="m",stream="true",result="ok"} 7
commandcode_proxy_tokens_total{kind="input"} 100
commandcode_proxy_cost_microdollars_total{model="m"} 500000
`

func newTestHandler(f *fakeUpstream) http.Handler {
	static := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html><body>dashboard</body></html>")},
	}
	return New(f, alerts.Thresholds{WindowPercent: 80, LowCreditUSD: 5}, static, 5*time.Second)
}

func TestSummaryHappyPath(t *testing.T) {
	f := &fakeUpstream{
		creditsRaw: []byte(creditsBody),
		version:    upstream.Version{Version: "v0.1.6", CCVersion: "1.53.0"},
		health:     upstream.Health{Reachable: true, LivenessOK: true, ReadinessOK: true},
	}
	rec := httptest.NewRecorder()
	newTestHandler(f).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/summary", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got summaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Balance != 11.0 {
		t.Errorf("Balance = %v, want 11.0", got.Balance)
	}
	if got.Version.Version != "v0.1.6" {
		t.Errorf("Version = %+v", got.Version)
	}
	// weekly exceeded -> at least one alert
	if len(got.Alerts) == 0 {
		t.Errorf("expected alerts for exceeded weekly window")
	}
	if len(got.Errors) != 0 {
		t.Errorf("unexpected errors: %+v", got.Errors)
	}
}

func TestSummaryDegradesOnCreditsError(t *testing.T) {
	f := &fakeUpstream{
		creditsErr: errors.New("boom"),
		health:     upstream.Health{Reachable: true, LivenessOK: true, ReadinessOK: true},
	}
	rec := httptest.NewRecorder()
	newTestHandler(f).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/summary", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got summaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Errors) != 1 || got.Errors[0].Source != "credits" {
		t.Fatalf("errors = %+v", got.Errors)
	}
}

func TestMetricsHappyPath(t *testing.T) {
	f := &fakeUpstream{metrics: metricsBody}
	rec := httptest.NewRecorder()
	newTestHandler(f).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got metricsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Enabled || got.RequestsTotal != 7 {
		t.Errorf("metrics = %+v", got)
	}
	if got.CostUSD != 0.5 {
		t.Errorf("CostUSD = %v, want 0.5", got.CostUSD)
	}
}

func TestMetricsDisabled(t *testing.T) {
	f := &fakeUpstream{metricsErr: errors.New("404")}
	rec := httptest.NewRecorder()
	newTestHandler(f).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
	var got metricsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Enabled {
		t.Error("expected Enabled=false")
	}
}

func TestStaticServed(t *testing.T) {
	f := &fakeUpstream{}
	rec := httptest.NewRecorder()
	newTestHandler(f).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
		t.Fatalf("static not served: code=%d body=%q", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write minimal implementation**

`internal/api/server.go`:
```go
// Package api exposes the dashboard HTTP surface: /api/summary, /api/metrics,
// and the embedded static frontend.
package api

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"sort"
	"time"

	"github.com/FFFold/command-code-reverse-dashboard/internal/alerts"
	"github.com/FFFold/command-code-reverse-dashboard/internal/credits"
	"github.com/FFFold/command-code-reverse-dashboard/internal/promparse"
	"github.com/FFFold/command-code-reverse-dashboard/internal/upstream"
)

// Upstream is the data source used by the handlers.
type Upstream interface {
	Credits(ctx context.Context) ([]byte, error)
	Version(ctx context.Context) (upstream.Version, error)
	Health(ctx context.Context) (upstream.Health, error)
	Metrics(ctx context.Context) (string, error)
}

// New builds the root handler.
func New(up Upstream, th alerts.Thresholds, static fs.FS, timeout time.Duration) http.Handler {
	s := &Server{up: up, thresholds: th, static: static, timeout: timeout}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/summary", s.handleSummary)
	mux.HandleFunc("GET /api/metrics", s.handleMetrics)
	mux.Handle("GET /", http.FileServer(http.FS(static)))
	return mux
}

// Server holds handler dependencies.
type Server struct {
	up         Upstream
	thresholds alerts.Thresholds
	static     fs.FS
	timeout    time.Duration
}

type errorItem struct {
	Source  string `json:"source"`
	Message string `json:"message"`
}

type healthResponse struct {
	Reachable       bool `json:"reachable"`
	LivenessOK      bool `json:"livenessOK"`
	ReadinessOK     bool `json:"readinessOK"`
	LivenessStatus  int  `json:"livenessStatus"`
	ReadinessStatus int  `json:"readinessStatus"`
}

type summaryResponse struct {
	FetchedAt    string                `json:"fetchedAt"`
	Balance      float64               `json:"balance"`
	Credits      credits.Credits       `json:"credits"`
	Windows      credits.WindowLimits  `json:"windows"`
	Subscription *credits.Subscription `json:"subscription,omitempty"`
	Health       healthResponse        `json:"health"`
	Version      upstream.Version      `json:"version"`
	Alerts       []alerts.Alert        `json:"alerts"`
	Errors       []errorItem           `json:"errors"`
}

type costModel struct {
	Model string  `json:"model"`
	USD   float64 `json:"usd"`
}

type errorCount struct {
	Class string  `json:"class"`
	Count float64 `json:"count"`
}

type metricsResponse struct {
	FetchedAt      string                  `json:"fetchedAt"`
	Enabled        bool                    `json:"enabled"`
	Requests       []promparse.RequestStat `json:"requests"`
	RequestsTotal  float64                 `json:"requestsTotal"`
	Tokens         map[string]float64      `json:"tokens"`
	CostUSD        float64                 `json:"costUSD"`
	CostByModel    []costModel             `json:"costByModel"`
	Latency        []promparse.LatencyStat `json:"latency"`
	UpstreamErrors []errorCount            `json:"upstreamErrors"`
	Errors         []errorItem             `json:"errors"`
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	errs := []errorItem{}

	snap := &credits.Snapshot{}
	if raw, err := s.up.Credits(ctx); err != nil {
		errs = append(errs, errorItem{"credits", err.Error()})
	} else if parsed, perr := credits.Parse(raw); perr != nil {
		errs = append(errs, errorItem{"credits", perr.Error()})
	} else {
		snap = parsed
	}

	ver, err := s.up.Version(ctx)
	if err != nil {
		errs = append(errs, errorItem{"version", err.Error()})
	}

	h, err := s.up.Health(ctx)
	if err != nil {
		errs = append(errs, errorItem{"health", err.Error()})
	}

	windows := []alerts.WindowUsage{
		{Name: "5 小时窗口", Used: snap.WindowLimits.FiveHour.Used, Cap: snap.WindowLimits.FiveHour.Cap, Exceeded: snap.WindowLimits.FiveHour.Exceeded},
		{Name: "每周窗口", Used: snap.WindowLimits.Weekly.Used, Cap: snap.WindowLimits.Weekly.Cap, Exceeded: snap.WindowLimits.Weekly.Exceeded},
	}
	alertList := alerts.Evaluate(alerts.Input{
		Balance:        snap.Balance(),
		BelowThreshold: snap.Credits.BelowThreshold,
		Windows:        windows,
		Health:         alerts.Health{Reachable: h.Reachable, LivenessOK: h.LivenessOK, ReadinessOK: h.ReadinessOK},
	}, s.thresholds)

	resp := summaryResponse{
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
		Balance:   snap.Balance(),
		Credits:   snap.Credits,
		Windows:   snap.WindowLimits,
		Health: healthResponse{
			Reachable:       h.Reachable,
			LivenessOK:      h.LivenessOK,
			ReadinessOK:     h.ReadinessOK,
			LivenessStatus:  h.LivenessStatus,
			ReadinessStatus: h.ReadinessStatus,
		},
		Version: ver,
		Alerts:  alertList,
		Errors:  errs,
	}
	if snap.HasSub {
		sub := snap.Subscription
		resp.Subscription = &sub
	}
	writeJSON(w, resp)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	text, err := s.up.Metrics(ctx)
	if err != nil {
		writeJSON(w, metricsResponse{
			FetchedAt: time.Now().UTC().Format(time.RFC3339),
			Enabled:   false,
			Errors:    []errorItem{{"metrics", err.Error()}},
		})
		return
	}
	sum, err := promparse.Parse(text)
	if err != nil {
		writeJSON(w, metricsResponse{
			FetchedAt: time.Now().UTC().Format(time.RFC3339),
			Enabled:   true,
			Errors:    []errorItem{{"metrics", err.Error()}},
		})
		return
	}

	resp := metricsResponse{
		FetchedAt:     time.Now().UTC().Format(time.RFC3339),
		Enabled:       true,
		Requests:      sum.Requests,
		RequestsTotal: sum.RequestTotal,
		Tokens:        sum.TokenTotals,
		Latency:       sum.Latency,
		Errors:        []errorItem{},
	}
	models := make([]string, 0, len(sum.CostMicroByModel))
	for m := range sum.CostMicroByModel {
		models = append(models, m)
	}
	sort.Strings(models)
	for _, m := range models {
		usd := sum.CostMicroByModel[m] / 1_000_000
		resp.CostUSD += usd
		resp.CostByModel = append(resp.CostByModel, costModel{Model: m, USD: usd})
	}
	classes := make([]string, 0, len(sum.UpstreamErrors))
	for c := range sum.UpstreamErrors {
		classes = append(classes, c)
	}
	sort.Strings(classes)
	for _, c := range classes {
		resp.UpstreamErrors = append(resp.UpstreamErrors, errorCount{Class: c, Count: sum.UpstreamErrors[c]})
	}
	writeJSON(w, resp)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/api/`
Expected: PASS.

- [ ] **Step 5: Commit**

```
git add internal/api/
git commit -m "feat: add dashboard api handlers"
```

---

### Task 8: embedded frontend

**Files:**
- Create: `web/embed.go`
- Create: `web/index.html`
- Create: `web/app.js`
- Create: `web/style.css`

- [ ] **Step 1: Write the embed declaration**

`web/embed.go`:
```go
// Package web holds the dashboard's embedded static frontend.
package web

import "embed"

// Files is the embedded frontend (index.html, app.js, style.css).
//
//go:embed index.html app.js style.css
var Files embed.FS
```

- [ ] **Step 2: Write the HTML**

`web/index.html`:
```html
<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Command Code · 余额看板</title>
  <link rel="stylesheet" href="/style.css">
</head>
<body>
  <header class="topbar">
    <div class="brand">Command Code · 余额看板</div>
    <div class="actions">
      <span id="updated" class="muted"></span>
      <button id="refresh" type="button">刷新</button>
    </div>
  </header>

  <main>
    <section class="hero card">
      <div class="label">剩余可用</div>
      <div id="balance" class="big">—</div>
      <div id="balance-sub" class="muted"></div>
      <div class="windows">
        <div class="window">
          <div class="win-head"><span>5 小时窗口</span><span id="win5-pct" class="muted"></span></div>
          <div class="bar"><i id="win5-fill"></i></div>
          <div id="win5-text" class="muted"></div>
        </div>
        <div class="window">
          <div class="win-head"><span>每周窗口</span><span id="winw-pct" class="muted"></span></div>
          <div class="bar"><i id="winw-fill"></i></div>
          <div id="winw-text" class="muted"></div>
        </div>
      </div>
    </section>

    <section class="grid">
      <div class="card"><div class="label">月度剩余</div><div id="m-monthly" class="metric">—</div></div>
      <div class="card"><div class="label">已购额度</div><div id="m-purchased" class="metric">—</div></div>
      <div class="card"><div class="label">免费额度</div><div id="m-free" class="metric">—</div></div>
      <div class="card"><div class="label">套餐</div><div id="m-plan" class="metric small">—</div></div>
    </section>

    <section class="card">
      <h2>告警</h2>
      <ul id="alerts" class="alerts"><li class="muted">加载中…</li></ul>
    </section>

    <section class="card">
      <h2>健康与版本</h2>
      <div class="health">
        <span id="h-live" class="badge">liveness —</span>
        <span id="h-ready" class="badge">readiness —</span>
        <span id="h-ver" class="badge plain">proxy —</span>
        <span id="h-cc" class="badge plain">cli —</span>
      </div>
      <div id="summary-errors" class="errors"></div>
    </section>

    <section class="card">
      <h2>指标</h2>
      <div id="metrics-wrap">
        <div class="grid">
          <div class="card inner"><div class="label">请求总数</div><div id="mt-req" class="metric">—</div></div>
          <div class="card inner"><div class="label">输入 token</div><div id="mt-in" class="metric">—</div></div>
          <div class="card inner"><div class="label">输出 token</div><div id="mt-out" class="metric">—</div></div>
          <div class="card inner"><div class="label">缓存 token</div><div id="mt-cached" class="metric">—</div></div>
          <div class="card inner"><div class="label">累计成本 (USD)</div><div id="mt-cost" class="metric">—</div></div>
        </div>
        <h3>按模型</h3>
        <table id="mt-requests" class="table"></table>
        <div id="metrics-errors" class="errors"></div>
      </div>
    </section>
  </main>

  <script src="/app.js"></script>
</body>
</html>
```

- [ ] **Step 3: Write the CSS**

`web/style.css`:
```css
:root {
  --bg: #0f1420; --panel: #1a2334; --panel2: #131b2b; --line: #24304a;
  --text: #e8eefc; --muted: #8b9bb4; --accent: #4a90e2; --good: #5ad19a;
  --warn: #e2a14a; --bad: #e2614a;
}
* { box-sizing: border-box; }
body {
  margin: 0; background: var(--bg); color: var(--text);
  font: 14px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif;
}
.topbar {
  display: flex; justify-content: space-between; align-items: center;
  padding: 14px 20px; border-bottom: 1px solid var(--line); background: var(--panel2);
  position: sticky; top: 0; z-index: 5;
}
.brand { font-weight: 600; letter-spacing: .02em; }
.actions { display: flex; align-items: center; gap: 12px; }
button {
  background: var(--accent); color: #fff; border: 0; border-radius: 6px;
  padding: 7px 14px; font-size: 13px; cursor: pointer;
}
button:hover { filter: brightness(1.1); }
button:disabled { opacity: .5; cursor: default; }
main { max-width: 960px; margin: 0 auto; padding: 20px; display: flex; flex-direction: column; gap: 16px; }
.card { background: var(--panel); border: 1px solid var(--line); border-radius: 10px; padding: 16px; }
.card.inner { background: var(--panel2); }
.hero .big { font-size: 40px; font-weight: 700; margin: 2px 0 4px; }
.label { font-size: 11px; text-transform: uppercase; letter-spacing: .08em; color: var(--muted); }
.muted { color: var(--muted); }
.small { font-size: 13px; }
.metric { font-size: 22px; font-weight: 600; margin-top: 4px; }
.windows { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; margin-top: 16px; }
.win-head { display: flex; justify-content: space-between; font-size: 13px; margin-bottom: 6px; }
.bar { height: 8px; background: #2a3650; border-radius: 4px; overflow: hidden; }
.bar > i { display: block; height: 100%; width: 0; background: var(--accent); border-radius: 4px; transition: width .3s; }
.bar > i.warn { background: var(--warn); }
.bar > i.bad { background: var(--bad); }
.grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: 12px; }
h2 { font-size: 15px; margin: 0 0 12px; }
h3 { font-size: 13px; margin: 16px 0 8px; color: var(--muted); }
.alerts { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
.alerts li { border-radius: 8px; padding: 10px 12px; background: var(--panel2); border-left: 3px solid var(--line); }
.alerts li.warning { border-left-color: var(--warn); }
.alerts li.critical { border-left-color: var(--bad); }
.alerts li.info { border-left-color: var(--accent); }
.alerts .t { font-weight: 600; }
.alerts .d { color: var(--muted); font-size: 12px; }
.health { display: flex; flex-wrap: wrap; gap: 8px; }
.badge { background: var(--panel2); border: 1px solid var(--line); border-radius: 999px; padding: 4px 10px; font-size: 12px; }
.badge.ok { border-color: var(--good); color: var(--good); }
.badge.bad { border-color: var(--bad); color: var(--bad); }
.table { width: 100%; border-collapse: collapse; margin-top: 6px; font-size: 13px; }
.table th, .table td { text-align: left; padding: 6px 8px; border-bottom: 1px solid var(--line); }
.table th { color: var(--muted); font-weight: 500; }
.errors { margin-top: 12px; color: var(--warn); font-size: 12px; }
.errors div { margin-top: 4px; }
@media (max-width: 640px) { .windows { grid-template-columns: 1fr; } }
```

- [ ] **Step 4: Write the JS**

`web/app.js`:
```js
'use strict';

const $ = (id) => document.getElementById(id);

function fmtUSD(n) {
  if (n == null || isNaN(n)) return '—';
  return '$' + Number(n).toFixed(4);
}
function fmtNum(n) {
  if (n == null || isNaN(n)) return '—';
  return Number(n).toLocaleString('en-US');
}
function fmtTime(ms) {
  if (!ms) return '—';
  return new Date(ms).toLocaleString();
}

function setBadge(el, label, ok) {
  el.textContent = label + (ok ? ' OK' : ' DOWN');
  el.classList.remove('ok', 'bad');
  el.classList.add(ok ? 'ok' : 'bad');
}

function renderSummary(s) {
  $('balance').textContent = fmtUSD(s.balance);
  const c = s.credits || {};
  $('balance-sub').textContent =
    `月度 ${fmtUSD(c.monthlyCredits)} · 已购 ${fmtUSD(c.purchasedCredits)} · 免费 ${fmtUSD(c.freeCredits)}`;
  $('m-monthly').textContent = fmtUSD(c.monthlyCredits);
  $('m-purchased').textContent = fmtUSD(c.purchasedCredits);
  $('m-free').textContent = fmtUSD(c.freeCredits);

  const sub = s.subscription;
  $('m-plan').textContent = sub ? `${sub.planId} (${sub.status})` : '—';

  const w = s.windows || {};
  renderWindow('5', w.fiveHour);
  renderWindow('w', w.weekly);

  renderAlerts(s.alerts || []);

  const h = s.health || {};
  setBadge($('h-live'), 'liveness', !!h.livenessOK);
  setBadge($('h-ready'), 'readiness', !!h.readinessOK);
  $('h-ver').textContent = 'proxy ' + ((s.version && s.version.version) || '—');
  $('h-cc').textContent = 'cli ' + ((s.version && s.version.ccVersion) || '—');

  renderErrors('summary-errors', s.errors);
  $('updated').textContent = '更新于 ' + (s.fetchedAt ? new Date(s.fetchedAt).toLocaleTimeString() : '—');
}

function renderWindow(key, win) {
  win = win || { used: 0, cap: 0 };
  const cap = Number(win.cap) || 0;
  const used = Number(win.used) || 0;
  const pct = cap > 0 ? (used / cap) * 100 : 0;
  const fill = $('win' + key + '-fill');
  fill.style.width = Math.min(100, pct).toFixed(1) + '%';
  fill.classList.remove('warn', 'bad');
  if (win.exceeded || pct >= 95) fill.classList.add('bad');
  else if (pct >= 80) fill.classList.add('warn');
  $('win' + key + '-pct').textContent = pct.toFixed(1) + '%';
  $('win' + key + '-text').textContent =
    `${fmtUSD(used)} / ${fmtUSD(cap)} · 重置 ${fmtTime(win.resetAt)}`;
}

function renderAlerts(list) {
  const ul = $('alerts');
  ul.innerHTML = '';
  if (!list.length) {
    const li = document.createElement('li');
    li.className = 'info';
    li.textContent = '正常，无告警';
    ul.appendChild(li);
    return;
  }
  for (const a of list) {
    const li = document.createElement('li');
    li.className = a.level || 'info';
    const t = document.createElement('div');
    t.className = 't';
    t.textContent = a.title;
    const d = document.createElement('div');
    d.className = 'd';
    d.textContent = a.detail;
    li.append(t, d);
    ul.appendChild(li);
  }
}

function renderErrors(id, errs) {
  const box = $(id);
  box.innerHTML = '';
  if (!errs || !errs.length) return;
  for (const e of errs) {
    const div = document.createElement('div');
    div.textContent = `[${e.source}] ${e.message}`;
    box.appendChild(div);
  }
}

function renderMetrics(m) {
  if (!m.enabled) {
    $('metrics-wrap').innerHTML =
      '<div class="muted">指标未启用（代理 METRICS_ENABLED=false）</div>';
    return;
  }
  $('mt-req').textContent = fmtNum(m.requestsTotal);
  const t = m.tokens || {};
  $('mt-in').textContent = fmtNum(t.input);
  $('mt-out').textContent = fmtNum(t.output);
  $('mt-cached').textContent = fmtNum(t.cached);
  $('mt-cost').textContent = fmtUSD(m.costUSD);

  const table = $('mt-requests');
  table.innerHTML = '';
  const head = document.createElement('tr');
  for (const h of ['模型', 'stream', 'result', '次数']) {
    const th = document.createElement('th');
    th.textContent = h;
    head.appendChild(th);
  }
  table.appendChild(head);
  for (const r of (m.requests || [])) {
    const tr = document.createElement('tr');
    for (const v of [r.model, r.stream, r.result, fmtNum(r.count)]) {
      const td = document.createElement('td');
      td.textContent = v;
      tr.appendChild(td);
    }
    table.appendChild(tr);
  }
  renderErrors('metrics-errors', m.errors);
}

async function getJSON(url) {
  const res = await fetch(url, { cache: 'no-store' });
  return res.json();
}

async function refresh() {
  const btn = $('refresh');
  btn.disabled = true;
  try {
    const [summary, metrics] = await Promise.all([
      getJSON('/api/summary'),
      getJSON('/api/metrics'),
    ]);
    renderSummary(summary);
    renderMetrics(metrics);
  } catch (err) {
    console.error(err);
    $('updated').textContent = '刷新失败';
  } finally {
    btn.disabled = false;
  }
}

$('refresh').addEventListener('click', refresh);
refresh();
```

- [ ] **Step 5: Verify it compiles**

Run: `go build ./web/`
Expected: no output (exit 0).

- [ ] **Step 6: Commit**

```
git add web/
git commit -m "feat: add embedded dashboard frontend"
```

---

### Task 9: main entrypoint

**Files:**
- Create: `cmd/credit-dashboard/main.go`

- [ ] **Step 1: Write the entrypoint**

`cmd/credit-dashboard/main.go`:
```go
// credit-dashboard visualizes a commandcode-proxy account's key balance.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/FFFold/command-code-reverse-dashboard/internal/alerts"
	"github.com/FFFold/command-code-reverse-dashboard/internal/api"
	"github.com/FFFold/command-code-reverse-dashboard/internal/config"
	"github.com/FFFold/command-code-reverse-dashboard/internal/upstream"
	"github.com/FFFold/command-code-reverse-dashboard/web"
)

// buildVersion is stamped via -ldflags "-X main.buildVersion=...".
var buildVersion = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("credit-dashboard", buildVersion)
		return
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}
	initLogging(cfg.LogLevel)

	client := upstream.New(cfg.ProxyBaseURL, cfg.ProxyAPIKey, cfg.UpstreamTimeout)
	handler := api.New(client, alerts.Thresholds{
		WindowPercent: cfg.AlertWindowPercent,
		LowCreditUSD:  cfg.AlertLowCreditUSD,
	}, web.Files, cfg.UpstreamTimeout)

	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("credit-dashboard listening",
			"addr", cfg.Addr(),
			"version", buildVersion,
			"proxyBase", cfg.ProxyBaseURL,
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		slog.Error("server failed", "error", err)
		os.Exit(1)
	case sig := <-sigCh:
		slog.Info("shutdown signal received", "signal", sig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	slog.Info("credit-dashboard stopped")
}

func initLogging(level string) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})))
}
```

- [ ] **Step 2: Build everything**

Run: `go build ./...`
Expected: no output (exit 0).

- [ ] **Step 3: Vet and run the full test suite**

Run: `go vet ./... && go test -race ./...`
Expected: all packages PASS, vet clean.

- [ ] **Step 4: Commit**

```
git add cmd/
git commit -m "feat: add dashboard entrypoint"
```

---

### Task 10: docs, env template, and live smoke test

**Files:**
- Create: `.env.example`
- Create: `README.md`

- [ ] **Step 1: Write `.env.example`**

`.env.example`:
```
# Target commandcode-proxy instance.
PROXY_BASE_URL=http://localhost:3050

# Downstream proxy API key used to call /v1/credits. Server-side only.
PROXY_API_KEY=

# Dashboard listen address (no built-in auth; protect via firewall/reverse proxy).
HOST=0.0.0.0
PORT=8787

# Alerts.
ALERT_WINDOW_PERCENT=80
ALERT_LOW_CREDIT_USD=5

# Proxy request timeout (seconds).
UPSTREAM_TIMEOUT_SECONDS=10

# debug | info | warn | error
LOG_LEVEL=info
```

- [ ] **Step 2: Write `README.md`**

`README.md`:
```markdown
# command-code-reverse-dashboard

`commandcode-proxy` 的余额看板：可视化 key 余额、限额窗口、告警、Prometheus
指标与健康/版本。单个 Go 二进制，零第三方依赖，前端由 `go:embed` 内嵌。

## 快速开始

```bash
cp .env.example .env   # 填入 PROXY_BASE_URL 和 PROXY_API_KEY
go run ./cmd/credit-dashboard
# 打开 http://localhost:8787
```

## 配置

全部通过环境变量（支持 `.env`）：

| 变量 | 默认 | 说明 |
|---|---|---|
| `PROXY_BASE_URL` | `http://localhost:3050` | 目标代理地址 |
| `PROXY_API_KEY` | — (必填) | 调用 `/v1/credits` 的下游 key，仅服务端持有 |
| `HOST` / `PORT` | `0.0.0.0` / `8787` | 监听地址（无内置鉴权，请自行防护） |
| `ALERT_WINDOW_PERCENT` | `80` | 窗口用量告警阈值（百分比） |
| `ALERT_LOW_CREDIT_USD` | `5` | 低余额告警阈值（美元） |
| `UPSTREAM_TIMEOUT_SECONDS` | `10` | 请求代理的超时 |
| `LOG_LEVEL` | `info` | 日志级别 |

## 余额口径

- 后端调用 `GET /v1/credits`（`Authorization: Bearer <PROXY_API_KEY>`）。
- **剩余余额 = `monthlyCredits + purchasedCredits + freeCredits`**；
  `monthlyCredits` 已是月度剩余，窗口 `used` 不可重复计入。
- 用户手动点击「刷新」触发；无自动轮询。

## 接口

- `GET /api/summary` — 余额、窗口、订阅、健康、版本、告警（各源失败时部分降级）。
- `GET /api/metrics` — 解析后的 `/metrics` 数据。
- `GET /` — 内嵌前端。

## 开发

```bash
go vet ./... && go test -race ./...
```
```

- [ ] **Step 3: Full verification**

Run: `go vet ./... && go test -race ./... && go build ./...`
Expected: all green.

- [ ] **Step 4: Live smoke test against the running proxy**

Start the dashboard against the live instance and confirm it serves:
```
$env:PROXY_BASE_URL="http://100.87.49.15:9511"
$env:PROXY_API_KEY="afaf5b43c5cbf71f9bd02801d33fe5a06d50253e307b8d3d177b7ca0c3dde56d"
go run ./cmd/credit-dashboard
```
In a second terminal:
```
curl http://localhost:8787/api/summary
curl http://localhost:8787/api/metrics
```
Expected: `summary` shows a positive `balance`, both windows, `health.reachable=true`,
`version.version="v0.1.6"`, `version.ccVersion="1.53.0"`; `metrics.RequestsTotal` > 0.
Stop the dashboard with Ctrl+C.

- [ ] **Step 5: Commit**

```
git add .env.example README.md
git commit -m "docs: add readme and env template"
```

---

## Self-review

**Spec coverage:**
- §2 data semantics → Tasks 3 (parse + balance), 4 (metrics), plan data contract. ✓
- §3 architecture (embed, server-side key, timeout, partial degradation) → Tasks 6, 7, 8, 9. ✓
- §4 `/api/summary`, `/api/metrics`, static → Task 7. ✓
- §5 layout A frontend → Task 8. ✓
- §6 alert rules → Task 5. ✓
- §7 config → Task 2. ✓
- §8 error/degration → Task 7 (summary degrades; metrics disabled state) + Tasks 5 (unhealthy). ✓
- §9 tests → each task's TDD tests; Task 9 runs `-race`. ✓
- §10 structure → refined to add `internal/api` (handlers separated from `main` for testability); all other paths match. ✓
- §11 verification → Tasks 9 & 10. ✓

**Placeholder scan:** No TBD/TODO; every code step contains complete code.

**Type consistency:** `credits.Snapshot`/`Credits`/`WindowLimits`/`Window`, `promparse.RequestStat`/`LatencyStat`/`Summary`, `alerts.Alert`/`Thresholds`/`WindowUsage`/`Health`/`Input`/`Level`, `upstream.Version`/`Health`/`Client`, `api.Upstream` interface, and `web.Files` are consistent across tasks. `Balance()` defined in Task 3 is used in Tasks 5 (via value) and 7. Thresholds field names (`WindowPercent`, `LowCreditUSD`) match between Task 2, 5, and 9.
