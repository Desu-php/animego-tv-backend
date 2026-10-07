package player

import (
	"animego/backend/internal/domain"
	"context"
	"errors"
	"testing"
)

type stubResolver struct {
	supports bool
	sources  []domain.VideoSource
	err      error
}

func (s stubResolver) Supports(domain.PlayerSelection) bool { return s.supports }
func (s stubResolver) Resolve(context.Context, domain.PlayerSelection) ([]domain.VideoSource, error) {
	return s.sources, s.err
}
func TestRegistry(t *testing.T) {
	sentinel := errors.New("upstream fixture")
	for _, tt := range []struct {
		name     string
		resolver Resolver
		want     error
	}{
		{"unregistered", nil, domain.ErrUnsupportedPlayer},
		{"unsupported", stubResolver{}, domain.ErrUnsupportedPlayer},
		{"empty", stubResolver{supports: true}, domain.ErrPlaybackUnavailable},
		{"wrapped error", stubResolver{supports: true, err: sentinel}, sentinel},
		{"success", stubResolver{supports: true, sources: []domain.VideoSource{{Type: domain.VideoHLS, URL: "https://cdn.example/master.m3u8"}}}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			entries := map[string]Resolver{"fixture": tt.resolver}
			registry := NewRegistry(entries)
			delete(entries, "fixture")
			sources, err := registry.Resolve(context.Background(), domain.PlayerSelection{Player: domain.PlayerReference{Provider: "fixture"}})
			if tt.want != nil {
				if !errors.Is(err, tt.want) {
					t.Fatalf("got %v", err)
				}
			} else if err != nil || len(sources) != 1 {
				t.Fatalf("got %#v,%v", sources, err)
			}
		})
	}
}
