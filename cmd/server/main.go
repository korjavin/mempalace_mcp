package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/korjavin/mempalace_mcp/internal/api"
	"github.com/korjavin/mempalace_mcp/internal/auth"
	"github.com/korjavin/mempalace_mcp/internal/palace"
	"github.com/korjavin/mempalace_mcp/internal/proxy"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	port := envOr("PORT", "8080")
	baseURL := envOr("BASE_URL", "http://localhost:"+port)
	palacePath := envOr("PALACE_PATH", "/data/palace")

	if err := palace.EnsureInitialized(palacePath); err != nil {
		slog.Error("failed to auto-init mempalace", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	authCfg := auth.Config{
		ClientID:     mustEnv("GOOGLE_CLIENT_ID"),
		ClientSecret: mustEnv("GOOGLE_CLIENT_SECRET"),
		RedirectURL:  envOr("GOOGLE_REDIRECT_URL", baseURL+"/auth/callback"),
		AdminEmails:  envOr("ADMIN_EMAILS", ""),
		CookieSecret: mustEnv("COOKIE_SECRET"),
	}

	authService, err := auth.New(ctx, authCfg)
	if err != nil {
		slog.Error("failed to init auth", "error", err)
		os.Exit(1)
	}

	mcpProxy, err := proxy.New(ctx, palacePath)
	if err != nil {
		slog.Error("failed to start mempalace proxy", "error", err)
		os.Exit(1)
	}

	handler := api.NewHandler(authService, mcpProxy)

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // SSE needs no write timeout
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		slog.Info("server starting", "port", port, "base_url", baseURL)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down server")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server forced to shutdown", "error", err)
	}
	if err := mcpProxy.Stop(); err != nil {
		slog.Error("mempalace subprocess stop error", "error", err)
	}
	slog.Info("server stopped")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required environment variable %s is not set", key))
	}
	return v
}
