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
