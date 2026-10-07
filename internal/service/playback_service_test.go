package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"animego/backend/internal/domain"
	"animego/backend/internal/player"
)

type blockingResolver struct{}

func (blockingResolver) Supports(domain.PlayerSelection) bool { return true }
func (blockingResolver) Resolve(ctx context.Context, _ domain.PlayerSelection) ([]domain.VideoSource, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestPlaybackValidationAndTimeout(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := fakeProvider{getPlayer: func(ctx context.Context, a, e, p string) (*domain.PlayerSelection, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("discovery deadline missing")
		}
		return &domain.PlayerSelection{AnimeID: a, EpisodeID: e, TranslationID: "2", Player: domain.PlayerReference{ID: p, Provider: "test"}}, nil
	}}
	svc := NewPlaybackService(p, player.NewRegistry(map[string]player.Resolver{"test": blockingResolver{}}), time.Second, 10*time.Millisecond, logger)
	if _, err := svc.GetPlayback(context.Background(), "anime-1", "1", "1:2:3"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost timeout: %v", err)
	}
	svc.provider = fakeProvider{getPlayer: func(context.Context, string, string, string) (*domain.PlayerSelection, error) {
		t.Fatal("invalid identifiers reached provider")
		return nil, nil
	}}
	for _, tt := range [][3]string{{"../1", "1", "1:2:3"}, {"anime-1", "0", "0:2:3"}, {"anime-1", "1", "2:2:3"}, {"anime-1", "1", "1:2:3:4"}, {"anime-1", "1", "1:02:3"}} {
		if _, err := svc.GetPlayback(context.Background(), tt[0], tt[1], tt[2]); !errors.Is(err, domain.ErrInvalidIdentifier) {
			t.Errorf("accepted %#v: %v", tt, err)
		}
	}
	svc.provider = fakeProvider{getPlayer: func(context.Context, string, string, string) (*domain.PlayerSelection, error) {
		return nil, domain.ErrPlayerNotFound
	}}
	if _, err := svc.GetPlayback(context.Background(), "anime-1", "1", "1:2:3"); !errors.Is(err, domain.ErrPlayerNotFound) {
		t.Errorf("lost typed discovery error: %v", err)
	}
}
