package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"animego/backend/internal/domain"
	"animego/backend/internal/provider"
	"github.com/gin-gonic/gin"
)

type PlaybackResolver interface {
	GetPlayback(context.Context, string, string, string) (*domain.Playback, error)
}

type PlaybackHandler struct {
	service PlaybackResolver
	logger  *slog.Logger
}

func NewPlaybackHandler(service PlaybackResolver, logger *slog.Logger) *PlaybackHandler {
	return &PlaybackHandler{service: service, logger: logger}
}

func (h *PlaybackHandler) GetPlayback(c *gin.Context) {
	// Fresh media metadata must not be persisted by intermediary caches.
	c.Header("Cache-Control", "no-store")
	result, err := h.service.GetPlayback(c.Request.Context(), c.Param("id"), c.Param("episodeID"), c.Param("playerID"))
	if err == nil {
		c.JSON(http.StatusOK, gin.H{"data": result})
		return
	}
	status, message := http.StatusInternalServerError, "internal server error"
	switch {
	case errors.Is(err, domain.ErrInvalidIdentifier):
		status, message = http.StatusBadRequest, "invalid anime, episode, or player identifier"
	case errors.Is(err, domain.ErrAnimeNotFound):
		status, message = http.StatusNotFound, "anime not found"
	case errors.Is(err, domain.ErrEpisodeNotFound):
		status, message = http.StatusNotFound, "episode not found"
	case errors.Is(err, domain.ErrPlayerNotFound):
		status, message = http.StatusNotFound, "player not found"
	case errors.Is(err, domain.ErrUnsupportedPlayer):
		status, message = http.StatusUnprocessableEntity, "player is not supported"
	case errors.Is(err, domain.ErrPlaybackUnavailable):
		status, message = http.StatusUnprocessableEntity, "playback is unavailable"
	case errors.Is(err, provider.ErrUpstream):
		status, message = http.StatusBadGateway, "playback provider is temporarily unavailable"
	}
	if status >= 500 {
		h.logger.ErrorContext(c.Request.Context(), "playback request failed", "anime_id", c.Param("id"), "episode_id", c.Param("episodeID"), "error", err)
	}
	c.JSON(status, gin.H{"error": message})
}
