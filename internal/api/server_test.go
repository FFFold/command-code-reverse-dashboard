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
