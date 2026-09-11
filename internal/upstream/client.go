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
