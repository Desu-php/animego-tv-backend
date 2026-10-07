package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"animego/backend/internal/apperror"
	"animego/backend/internal/domain"
	"github.com/gin-gonic/gin"
)

// errorResponder maps application errors without knowing provider adapters.
type errorResponder struct {
	logger              *slog.Logger
	invalidIdentifier   string
	upstreamUnavailable string
	upstreamTimeout     string
}

func (r errorResponder) write(c *gin.Context, operation string, err error) {
	ctx := c.Request.Context()
	attributes := []any{"operation", operation, "anime_id", c.Param("id"), "episode_id", c.Param("episodeID")}
	if errors.Is(ctx.Err(), context.Canceled) {
		r.logger.InfoContext(ctx, "HTTP request canceled", attributes...)
		c.Abort()
		return
	}
	status, message := r.classify(err)
	upstreamStatus := 0
	var upstream interface{ UpstreamStatus() int }
	if errors.As(err, &upstream) {
		upstreamStatus = upstream.UpstreamStatus()
	}
	attributes = append(attributes, "status", status, "status_code", upstreamStatus, "error", err)
	if status >= 500 {
		r.logger.ErrorContext(ctx, "API request failed", attributes...)
	} else {
		r.logger.DebugContext(ctx, "API request rejected", attributes...)
	}
	if status == http.StatusServiceUnavailable {
		c.Header("Retry-After", "1")
	}
	c.JSON(status, gin.H{"error": message})
}

func (r errorResponder) classify(err error) (int, string) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, r.upstreamTimeout
	case errors.Is(err, apperror.ErrBusy):
		return http.StatusServiceUnavailable, "server is busy; retry shortly"
	case errors.Is(err, domain.ErrInvalidQuery):
		return http.StatusBadRequest, "q must contain 1 to 200 valid Unicode characters"
	case errors.Is(err, domain.ErrInvalidIdentifier):
		return http.StatusBadRequest, r.invalidIdentifier
	case errors.Is(err, domain.ErrAnimeNotFound):
		return http.StatusNotFound, "anime not found"
	case errors.Is(err, domain.ErrEpisodeNotFound):
		return http.StatusNotFound, "episode not found"
	case errors.Is(err, domain.ErrPlayerNotFound):
		return http.StatusNotFound, "player not found"
	case errors.Is(err, domain.ErrUnsupportedPlayer):
		return http.StatusUnprocessableEntity, "player is not supported"
	case errors.Is(err, domain.ErrPlaybackUnavailable):
		return http.StatusUnprocessableEntity, "playback is unavailable"
	case errors.Is(err, apperror.ErrUpstream):
		return http.StatusBadGateway, r.upstreamUnavailable
	default:
		return http.StatusInternalServerError, "internal server error"
	}
}
