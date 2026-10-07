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

// AnimeCatalog lets the HTTP transport depend on catalog behavior only.
type AnimeCatalog interface {
	Search(context.Context, string) ([]domain.Anime, error)
	GetAnime(context.Context, string) (*domain.AnimeDetails, error)
	GetEpisodes(context.Context, string) ([]domain.Episode, error)
	GetTranslations(context.Context, string, string) ([]domain.Translation, error)
}

type AnimeHandler struct {
	service AnimeCatalog
	logger  *slog.Logger
}

func NewAnimeHandler(service AnimeCatalog, logger *slog.Logger) *AnimeHandler {
	return &AnimeHandler{service: service, logger: logger}
}

func (h *AnimeHandler) Search(c *gin.Context) {
	results, err := h.service.Search(c.Request.Context(), c.Query("q"))
	if err != nil {
		h.writeError(c, "search", err)
		return
	}
	if results == nil {
		results = make([]domain.Anime, 0)
	}
	c.JSON(http.StatusOK, gin.H{"data": results})
}

func (h *AnimeHandler) GetAnime(c *gin.Context) {
	result, err := h.service.GetAnime(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.writeError(c, "details", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func (h *AnimeHandler) GetEpisodes(c *gin.Context) {
	results, err := h.service.GetEpisodes(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.writeError(c, "episodes", err)
		return
	}
	if results == nil {
		results = make([]domain.Episode, 0)
	}
	c.JSON(http.StatusOK, gin.H{"data": results})
}

func (h *AnimeHandler) GetTranslations(c *gin.Context) {
	results, err := h.service.GetTranslations(c.Request.Context(), c.Param("id"), c.Param("episodeID"))
	if err != nil {
		h.writeError(c, "translations", err)
		return
	}
	if results == nil {
		results = make([]domain.Translation, 0)
	}
	c.JSON(http.StatusOK, gin.H{"data": results})
}

func (h *AnimeHandler) writeError(c *gin.Context, operation string, err error) {
	status, message := http.StatusInternalServerError, "internal server error"
	switch {
	case errors.Is(err, domain.ErrInvalidQuery):
		status, message = http.StatusBadRequest, "q must contain 1 to 200 valid Unicode characters"
	case errors.Is(err, domain.ErrInvalidIdentifier):
		status, message = http.StatusBadRequest, "invalid anime or episode identifier"
	case errors.Is(err, domain.ErrAnimeNotFound):
		status, message = http.StatusNotFound, "anime not found"
	case errors.Is(err, domain.ErrEpisodeNotFound):
		status, message = http.StatusNotFound, "episode not found"
	case errors.Is(err, provider.ErrUpstream):
		status, message = http.StatusBadGateway, "anime catalog is temporarily unavailable"
	}
	var httpError *provider.HTTPError
	upstreamStatus := 0
	if errors.As(err, &httpError) {
		upstreamStatus = httpError.StatusCode
	}
	attributes := []any{"operation", operation, "anime_id", c.Param("id"), "episode_id", c.Param("episodeID"), "status_code", upstreamStatus, "error", err}
	if status >= 500 {
		h.logger.ErrorContext(c.Request.Context(), "anime catalog request failed", attributes...)
	} else {
		h.logger.DebugContext(c.Request.Context(), "anime catalog request rejected", attributes...)
	}
	c.JSON(status, gin.H{"error": message})
}
