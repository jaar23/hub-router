package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jaar23/hub-router/internal/config"
	"github.com/jaar23/hub-router/internal/handler"
	"github.com/jaar23/hub-router/internal/middleware"
	"github.com/jaar23/hub-router/internal/queue"
	"github.com/jaar23/hub-router/internal/store"
)

// Server wires together the HTTP server, handlers, and middleware.
type Server struct {
	cfg     *config.Config
	logger  *slog.Logger
	q       *queue.MemoryQueue
	s       *store.MemoryStore
	rl      *middleware.RateLimiter
	lockout *middleware.AuthLockout
	http    *http.Server
}

// New creates a Server from configuration.
func New(cfg *config.Config, logger *slog.Logger) *Server {
	q := queue.NewMemoryQueue(cfg.Queue.MaxSize)
	s := store.NewMemoryStore(cfg.Result.ResultTTL)

	auth := middleware.NewAPIKeyMiddleware(cfg.Auth.OnlineAPIKeys, cfg.Auth.LocalAPIKeys, cfg.Auth.AdminAPIKey)

	// Per-IP rate limiter (token bucket). Disabled when RateLimitRPS == 0.
	var rl *middleware.RateLimiter
	if cfg.Security.RateLimitRPS > 0 {
		rl = middleware.NewRateLimiter(
			cfg.Security.RateLimitRPS,
			cfg.Security.RateLimitBurst,
			cfg.Security.LockoutWindow,
		)
		logger.Info("rate limiting enabled",
			"rps", cfg.Security.RateLimitRPS,
			"burst", cfg.Security.RateLimitBurst,
		)
	}

	// Per-IP auth failure lockout.
	lockout := middleware.NewAuthLockout(
		cfg.Security.LockoutThreshold,
		cfg.Security.LockoutDuration,
		cfg.Security.LockoutWindow,
		logger,
	)
	logger.Info("auth lockout enabled",
		"threshold", cfg.Security.LockoutThreshold,
		"duration", cfg.Security.LockoutDuration,
	)

	onlineH := handler.NewOnlineHandler(q, s, cfg)
	localH := handler.NewLocalHandler(q, s, cfg)
	adminH := handler.NewAdminHandler(q, s, rl, lockout)

	mux := http.NewServeMux()

	// Online server routes — lockout wraps the auth check so failures are tracked.
	mux.Handle("POST /request",
		lockout.Wrap("X-Online-API-Key", auth.ValidOnlineKey,
			http.HandlerFunc(onlineH.HandleSubmit)))
	mux.Handle("POST /request/sync",
		lockout.Wrap("X-Online-API-Key", auth.ValidOnlineKey,
			http.HandlerFunc(onlineH.HandleSync)))
	mux.Handle("GET /result/{id}",
		lockout.Wrap("X-Online-API-Key", auth.ValidOnlineKey,
			http.HandlerFunc(onlineH.HandleResult)))

	// Local server routes — lockout wraps the auth check.
	mux.Handle("GET /queue/pull",
		lockout.Wrap("X-Local-API-Key", auth.ValidLocalKey,
			http.HandlerFunc(localH.HandlePull)))
	mux.Handle("POST /queue/result",
		lockout.Wrap("X-Local-API-Key", auth.ValidLocalKey,
			http.HandlerFunc(localH.HandleResult)))

	// Admin routes (optional auth — lockout not applied, admin key is single).
	mux.Handle("GET /health",
		auth.AdminAuth(http.HandlerFunc(adminH.HandleHealth)))
	mux.Handle("GET /metrics",
		auth.AdminAuth(adminH.HandleMetrics()))
	mux.Handle("GET /debug/stats",
		auth.AdminAuth(http.HandlerFunc(adminH.HandleStats)))

	// Global middleware chain (outermost → innermost):
	//   SecureHeaders → RateLimit → BodyLimit → Recovery → Logging → mux
	var h http.Handler = mux
	h = middleware.Logging(logger)(h)
	h = middleware.Recovery(logger)(h)
	h = middleware.BodyLimit(cfg.Security.MaxBodyBytes)(h)
	if rl != nil {
		h = rl.Middleware(h)
	}
	h = middleware.SecureHeaders(h)

	httpSrv := &http.Server{
		Addr:         cfg.Addr(),
		Handler:      h,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	return &Server{
		cfg:     cfg,
		logger:  logger,
		q:       q,
		s:       s,
		rl:      rl,
		lockout: lockout,
		http:    httpSrv,
	}
}

// Run starts the HTTP server and blocks until SIGINT/SIGTERM is received,
// then performs a graceful shutdown.
func (srv *Server) Run() error {
	errCh := make(chan error, 1)
	go func() {
		srv.logger.Info("hub-router listening", "addr", srv.cfg.Addr())
		if err := srv.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("http server: %w", err)
		}
		close(errCh)
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-quit:
		srv.logger.Info("shutting down", "signal", sig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), srv.cfg.Server.ShutdownTimeout)
	defer cancel()

	if err := srv.http.Shutdown(ctx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	// Close background goroutines.
	_ = srv.s.Close()
	srv.lockout.Close()
	if srv.rl != nil {
		srv.rl.Close()
	}

	srv.logger.Info("shutdown complete")
	return nil
}
