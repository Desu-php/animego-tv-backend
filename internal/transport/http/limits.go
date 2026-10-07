package httptransport

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// WithConcurrencyLimits bounds active API operations; surplus requests fail immediately.
func WithConcurrencyLimits(catalog, playback int) RouterOption {
	if catalog <= 0 || playback <= 0 {
		panic("API concurrency limits must be positive")
	}
	return func(cfg *routerConfig) { cfg.catalogLimit, cfg.playbackLimit = catalog, playback }
}

func concurrencyLimits(catalog, playback int) gin.HandlerFunc {
	catalogSlots, playbackSlots := make(chan struct{}, catalog), make(chan struct{}, playback)
	return func(c *gin.Context) {
		route := c.FullPath()
		if !strings.HasPrefix(route, "/api/v1/") {
			c.Next()
			return
		}
		slots := catalogSlots
		if strings.HasSuffix(route, "/playback") {
			slots = playbackSlots
			c.Header("Cache-Control", "no-store")
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			c.Next()
		default:
			c.Header("Retry-After", "1")
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "server is busy; retry shortly"})
		}
	}
}
