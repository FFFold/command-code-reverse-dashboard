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
