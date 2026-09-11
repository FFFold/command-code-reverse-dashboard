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
