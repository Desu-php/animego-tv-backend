package handler

import (
	"context"
	"log/slog"
	"net/http"

	"animego/backend/internal/domain"
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
	errors  errorResponder
}

func NewAnimeHandler(service AnimeCatalog, logger *slog.Logger) *AnimeHandler {
	return &AnimeHandler{service: service, errors: errorResponder{
		logger:              logger,
		invalidIdentifier:   "invalid anime or episode identifier",
		upstreamUnavailable: "anime catalog is temporarily unavailable",
		upstreamTimeout:     "anime catalog request timed out",
	}}
}

func (h *AnimeHandler) Search(c *gin.Context) {
	results, err := h.service.Search(c.Request.Context(), c.Query("q"))
	if err != nil {
		h.errors.write(c, "search", err)
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
		h.errors.write(c, "details", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func (h *AnimeHandler) GetEpisodes(c *gin.Context) {
	results, err := h.service.GetEpisodes(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.errors.write(c, "episodes", err)
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
		h.errors.write(c, "translations", err)
		return
	}
	if results == nil {
		results = make([]domain.Translation, 0)
	}
	c.JSON(http.StatusOK, gin.H{"data": results})
}
