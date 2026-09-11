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
