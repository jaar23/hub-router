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
	cfg    *config.Config
	logger *slog.Logger
	q      *queue.MemoryQueue
	s      *store.MemoryStore
	http   *http.Server
}

// New creates a Server from configuration.
func New(cfg *config.Config, logger *slog.Logger) *Server {
	q := queue.NewMemoryQueue(cfg.Queue.MaxSize)
	s := store.NewMemoryStore(cfg.Result.ResultTTL)

	auth := middleware.NewAPIKeyMiddleware(cfg.Auth.OnlineAPIKeys, cfg.Auth.LocalAPIKeys, cfg.Auth.AdminAPIKey)

	onlineH := handler.NewOnlineHandler(q, s, cfg)
	localH := handler.NewLocalHandler(q, s, cfg)
	adminH := handler.NewAdminHandler(q, s)

	mux := http.NewServeMux()

	// Online server routes
	mux.Handle("POST /request",
		auth.OnlineAuth(http.HandlerFunc(onlineH.HandleSubmit)))
	mux.Handle("GET /result/{id}",
		auth.OnlineAuth(http.HandlerFunc(onlineH.HandleResult)))

	// Local server routes
	mux.Handle("GET /queue/pull",
		auth.LocalAuth(http.HandlerFunc(localH.HandlePull)))
	mux.Handle("POST /queue/result",
		auth.LocalAuth(http.HandlerFunc(localH.HandleResult)))

	// Admin routes (optional auth)
	mux.Handle("GET /health",
		auth.AdminAuth(http.HandlerFunc(adminH.HandleHealth)))
	mux.Handle("GET /metrics",
		auth.AdminAuth(adminH.HandleMetrics()))

	// Apply global middleware: Recovery → Logging → router
	var h http.Handler = mux
	h = middleware.Logging(logger)(h)
	h = middleware.Recovery(logger)(h)

	httpSrv := &http.Server{
		Addr:         cfg.Addr(),
		Handler:      h,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	return &Server{
		cfg:    cfg,
		logger: logger,
		q:      q,
		s:      s,
		http:   httpSrv,
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

	srv.logger.Info("shutdown complete")
	return nil
}
