package handler

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"animego/backend/internal/domain"
	"animego/backend/internal/service"
	"github.com/gin-gonic/gin"
)

func TestCanceledClientDoesNotReceiveAnErrorResponse(t *testing.T) {
	var logs bytes.Buffer
	router := gin.New()
	router.GET("/playback", NewPlaybackHandler(fakePlayback{context.Canceled}, slog.New(slog.NewJSONHandler(&logs, nil))).GetPlayback)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/playback", nil).WithContext(ctx))
	if w.Body.Len() != 0 || !strings.Contains(logs.String(), "HTTP request canceled") || strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Fatalf("canceled client treated as server failure: body=%s logs=%s", w.Body.String(), logs.String())
	}
}

type timeoutPlayerProvider struct{}

func (timeoutPlayerProvider) GetPlayer(_ context.Context, animeID, episodeID, playerID string) (*domain.PlayerSelection, error) {
	return &domain.PlayerSelection{AnimeID: animeID, EpisodeID: episodeID, TranslationID: "2", Player: domain.PlayerReference{ID: playerID}}, nil
}

type timeoutSourceResolver struct{}

func (timeoutSourceResolver) Resolve(ctx context.Context, _ domain.PlayerSelection) ([]domain.VideoSource, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestPlaybackServiceDeadlineReturnsGatewayTimeout(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	playback := service.NewPlaybackService(timeoutPlayerProvider{}, timeoutSourceResolver{}, time.Second, time.Millisecond, logger)
	router := gin.New()
	router.GET("/anime/:id/episodes/:episodeID/players/:playerID/playback", NewPlaybackHandler(playback, logger).GetPlayback)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/anime/anime-1/episodes/1/players/1:2:3/playback", nil))
	if w.Code != 504 || !strings.Contains(w.Body.String(), "timed out") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("resolver deadline became an internal error: %d %s", w.Code, w.Body.String())
	}
}
