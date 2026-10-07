package handler

import (
	"context"
	"log/slog"
	"net/http"

	"animego/backend/internal/domain"
	"github.com/gin-gonic/gin"
)

type PlaybackResolver interface {
	GetPlayback(context.Context, string, string, string) (*domain.Playback, error)
}

type PlaybackHandler struct {
	service PlaybackResolver
	errors  errorResponder
}

func NewPlaybackHandler(service PlaybackResolver, logger *slog.Logger) *PlaybackHandler {
	return &PlaybackHandler{service: service, errors: errorResponder{
		logger:              logger,
		invalidIdentifier:   "invalid anime, episode, or player identifier",
		upstreamUnavailable: "playback provider is temporarily unavailable",
		upstreamTimeout:     "playback provider request timed out",
	}}
}

func (h *PlaybackHandler) GetPlayback(c *gin.Context) {
	// Fresh media metadata must not be persisted by intermediary caches.
	c.Header("Cache-Control", "no-store")
	result, err := h.service.GetPlayback(c.Request.Context(), c.Param("id"), c.Param("episodeID"), c.Param("playerID"))
	if err == nil {
		c.JSON(http.StatusOK, gin.H{"data": result})
		return
	}
	h.errors.write(c, "playback", err)
}
