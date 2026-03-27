package main

import (
	"log/slog"
	"os"

	"github.com/jaar23/hub-router/internal/config"
	"github.com/jaar23/hub-router/internal/server"
)

func main() {
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
