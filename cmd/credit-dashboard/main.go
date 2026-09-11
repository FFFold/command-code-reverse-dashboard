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
