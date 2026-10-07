package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"animego/backend/internal/config"
	"animego/backend/internal/player"
	"animego/backend/internal/player/aniboom"
	"animego/backend/internal/player/cvh"
	"animego/backend/internal/player/kodik"
	"animego/backend/internal/provider/animego"
	"animego/backend/internal/service"
	httptransport "animego/backend/internal/transport/http"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("API stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	client := &http.Client{Timeout: cfg.AnimeGOTimeout}
	defer client.CloseIdleConnections()
	upstream, err := animego.NewClient(cfg.AnimeGOBaseURL, client,
		animego.WithLogger(logger),
		animego.WithCatalogCache(cfg.CatalogCacheCapacity, cfg.CatalogDetailsTTL, cfg.CatalogEpisodesTTL),
		animego.WithRequestConcurrency(cfg.CatalogConcurrency+cfg.PlaybackConcurrency),
	)
	if err != nil {
		return fmt.Errorf("initialize AnimeGO provider: %w", err)
	}
	playerClient, err := player.NewHTTPClient(cfg.PlayerTimeout, []string{aniboom.EmbedHost}, player.WithReferer(aniboom.EmbedHost, cfg.AnimeGOBaseURL))
	if err != nil {
		return fmt.Errorf("initialize player HTTP client: %w", err)
	}
	defer playerClient.CloseIdleConnections()
	mediaClient, err := player.NewMediaHTTPClient(cfg.PlayerTimeout)
	if err != nil {
		return fmt.Errorf("initialize media HTTP client: %w", err)
	}
	defer mediaClient.CloseIdleConnections()
	kodikClient, err := player.NewHTTPClient(cfg.PlayerTimeout, []string{kodik.EmbedHost}, player.WithReferer(kodik.EmbedHost, cfg.AnimeGOBaseURL))
	if err != nil {
		return fmt.Errorf("initialize Kodik HTTP client: %w", err)
	}
	defer kodikClient.CloseIdleConnections()
	cvhClient, err := player.NewHTTPClient(cfg.PlayerTimeout, []string{cvh.APIHost})
	if err != nil {
		return fmt.Errorf("initialize CVH HTTP client: %w", err)
	}
	defer cvhClient.CloseIdleConnections()
	cvhResolver, err := cvh.NewResolver(cfg.AnimeGOBaseURL, upstream, cvhClient, mediaClient)
	if err != nil {
		return fmt.Errorf("initialize CVH resolver: %w", err)
	}
	registry := player.NewRegistry(map[string]player.Resolver{
		"aniboom": aniboom.NewResolver(playerClient, mediaClient),
		"kodik":   kodik.NewResolver(kodikClient, mediaClient),
		"cvh":     cvhResolver,
	})
	playbackService := service.NewPlaybackService(upstream, registry, cfg.AnimeGOTimeout, cfg.PlayerTimeout, logger)
	server := &http.Server{
		Addr: cfg.HTTPAddr, Handler: httptransport.NewRouter(service.NewAnimeService(upstream), logger, httptransport.WithPlayback(playbackService), httptransport.WithConcurrencyLimits(cfg.CatalogConcurrency, cfg.PlaybackConcurrency)),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		IdleTimeout: 60 * time.Second, WriteTimeout: cfg.AnimeGOTimeout + cfg.PlayerTimeout + 10*time.Second,
		MaxHeaderBytes: 16 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.ListenAndServe() }()
	logger.Info("API starting", "address", cfg.HTTPAddr)
	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
		stop()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.AnimeGOTimeout+cfg.PlayerTimeout+5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}
	logger.Info("API stopped gracefully")
	return nil
}
