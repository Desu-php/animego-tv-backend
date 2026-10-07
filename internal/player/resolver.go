package player

import (
	"context"
	"fmt"

	"animego/backend/internal/domain"
)

type Resolver interface {
	Supports(domain.PlayerSelection) bool
	Resolve(context.Context, domain.PlayerSelection) ([]domain.VideoSource, error)
}

type Registry struct{ resolvers map[string]Resolver }

func NewRegistry(resolvers map[string]Resolver) *Registry {
	registered := make(map[string]Resolver, len(resolvers))
	for name, resolver := range resolvers {
		registered[name] = resolver
	}
	return &Registry{resolvers: registered}
}

func (r *Registry) Resolve(ctx context.Context, selection domain.PlayerSelection) ([]domain.VideoSource, error) {
	resolver, exists := r.resolvers[selection.Player.Provider]
	if !exists || resolver == nil || !resolver.Supports(selection) {
		return nil, domain.ErrUnsupportedPlayer
	}
	sources, err := resolver.Resolve(ctx, selection)
	if err != nil {
		return nil, fmt.Errorf("resolve %s player: %w", selection.Player.Provider, err)
	}
	if len(sources) == 0 {
		return nil, domain.ErrPlaybackUnavailable
	}
	return sources, nil
}
