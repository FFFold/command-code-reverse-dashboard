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
