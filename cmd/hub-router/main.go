package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jaar23/hub-router/internal/config"
	"github.com/jaar23/hub-router/internal/server"
	"github.com/jaar23/hub-router/internal/tui"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "run a one-shot health check against the running server and exit")
	tuiMode := flag.Bool("tui", false, "run the terminal dashboard (connects to the running server via HTTP)")
	flag.Parse()

	if *tuiMode {
		port := envOrDefault("HR_PORT", "8080")
		addr := fmt.Sprintf("http://127.0.0.1:%s", port)
		apiKey := os.Getenv("HR_ADMIN_API_KEY")
		if err := tui.Run(addr, apiKey); err != nil {
			fmt.Fprintf(os.Stderr, "tui: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if *healthcheck {
		port := envOrDefault("HR_PORT", "8080")
		url := fmt.Sprintf("http://127.0.0.1:%s/health", port)
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get(url)
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		resp.Body.Close()
		os.Exit(0)
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration error", "error", err)
		os.Exit(1)
	}

	logger := buildLogger(cfg)
	logger.Info("hub-router starting",
		"port", cfg.Server.Port,
		"queue_max_size", cfg.Queue.MaxSize,
		"request_ttl", cfg.Queue.RequestTTL.String(),
		"longpoll_timeout", cfg.Result.LongPollTimeout.String(),
	)

	srv := server.New(cfg, logger)
	if err := srv.Run(); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func buildLogger(cfg *config.Config) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.Log.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if cfg.Log.Format == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(handler)
}
