package httptransport

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"animego/backend/docs"
	"animego/backend/internal/transport/http/handler"
	"github.com/gin-gonic/gin"
)

type routerConfig struct {
	playback                    handler.PlaybackResolver
	hasPlayback                 bool
	catalogLimit, playbackLimit int
}

type RouterOption func(*routerConfig)

func WithPlayback(service handler.PlaybackResolver) RouterOption {
	return func(cfg *routerConfig) { cfg.playback, cfg.hasPlayback = service, true }
}

func NewRouter(catalog handler.AnimeCatalog, logger *slog.Logger, options ...RouterOption) *gin.Engine {
	cfg := routerConfig{catalogLimit: 16, playbackLimit: 4}
	for _, option := range options {
		option(&cfg)
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.InfoContext(c.Request.Context(), "HTTP request", "method", c.Request.Method, "path", c.Request.URL.Path, "status", c.Writer.Status(), "duration", time.Since(start))
	})
	router.Use(func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(c.Request.Context(), "HTTP handler panic", "panic", recovered, "stack", string(debug.Stack()))
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			}
		}()
		c.Next()
	})
	router.Use(concurrencyLimits(cfg.catalogLimit, cfg.playbackLimit))
	router.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	docs.Register(router)
	animeHandler := handler.NewAnimeHandler(catalog, logger)
	router.GET("/api/v1/anime/search", animeHandler.Search)
	router.GET("/api/v1/anime/:id", animeHandler.GetAnime)
	router.GET("/api/v1/anime/:id/episodes", animeHandler.GetEpisodes)
	router.GET("/api/v1/anime/:id/episodes/:episodeID/translations", animeHandler.GetTranslations)
	if cfg.hasPlayback {
		router.GET("/api/v1/anime/:id/episodes/:episodeID/players/:playerID/playback", handler.NewPlaybackHandler(cfg.playback, logger).GetPlayback)
	}
	return router
}
