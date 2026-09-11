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
