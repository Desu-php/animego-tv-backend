package handler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"animego/backend/internal/apperror"
	"animego/backend/internal/domain"
	"animego/backend/internal/provider"
	"github.com/gin-gonic/gin"
)

type fakePlayback struct{ err error }

func (f fakePlayback) GetPlayback(context.Context, string, string, string) (*domain.Playback, error) {
	return &domain.Playback{Sources: []domain.VideoSource{{Type: domain.VideoDASH, URL: "https://cdn.example/manifest.mpd"}}}, f.err
}
func TestPlaybackErrorMapping(t *testing.T) {
	for _, tt := range []struct {
		err    error
		status int
	}{
		{nil, 200}, {domain.ErrInvalidIdentifier, 400}, {domain.ErrAnimeNotFound, 404}, {domain.ErrEpisodeNotFound, 404}, {domain.ErrPlayerNotFound, 404},
		{domain.ErrUnsupportedPlayer, 422}, {domain.ErrPlaybackUnavailable, 422}, {provider.ErrUpstream, 502}, {provider.ErrParsing, 502},
		{errors.Join(domain.ErrPlaybackUnavailable, &provider.HTTPError{StatusCode: 403, Err: errors.New("secret-detail")}), 422},
		{errors.New("secret-detail"), 500},
		{context.DeadlineExceeded, 504},
		{&provider.HTTPError{Err: context.DeadlineExceeded}, 504},
		{apperror.ErrBusy, 503},
	} {
		t.Run(http.StatusText(tt.status)+" "+fmtError(tt.err), func(t *testing.T) {
			r := gin.New()
			r.GET("/anime/:id/episodes/:episodeID/players/:playerID/playback", NewPlaybackHandler(fakePlayback{tt.err}, slog.New(slog.NewTextHandler(io.Discard, nil))).GetPlayback)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/anime/a-1/episodes/1/players/1:2:3/playback", nil))
			if w.Code != tt.status || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("got %d %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "secret-detail") {
				t.Fatal("internal error exposed")
			}
			if tt.status == 503 && w.Header().Get("Retry-After") != "1" {
				t.Fatal("busy response missing Retry-After")
			}
		})
	}
}
func fmtError(err error) string {
	if err == nil {
		return "success"
	}
	return err.Error()
}
