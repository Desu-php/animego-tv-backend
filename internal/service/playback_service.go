package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"animego/backend/internal/domain"
)

type PlaybackService struct {
	provider                    PlayerProvider
	players                     SourceResolver
	animeTimeout, playerTimeout time.Duration
	logger                      *slog.Logger
}

func NewPlaybackService(p PlayerProvider, players SourceResolver, animeTimeout, playerTimeout time.Duration, logger *slog.Logger) *PlaybackService {
	return &PlaybackService{provider: p, players: players, animeTimeout: animeTimeout, playerTimeout: playerTimeout, logger: logger}
}

func (s *PlaybackService) GetPlayback(ctx context.Context, animeID, episodeID, playerID string) (playback *domain.Playback, err error) {
	if !domain.ValidAnimeID(animeID) || !domain.ValidEpisodeID(episodeID) || !domain.ValidPlayerID(playerID, episodeID) {
		return nil, domain.ErrInvalidIdentifier
	}
	ctx, cancel := context.WithTimeout(ctx, s.animeTimeout+s.playerTimeout)
	defer cancel()
	start := time.Now()
	stage, providerName, translationID := "animego", "animego", ""
	defer func() {
		statusCode := 0
		var httpError interface{ UpstreamStatus() int }
		if errors.As(err, &httpError) {
			statusCode = httpError.UpstreamStatus()
		}
		mediaType := ""
		if playback != nil && len(playback.Sources) > 0 {
			mediaType = string(playback.Sources[0].Type)
		}
		attributes := []any{"provider", providerName, "stage", stage, "anime_id", animeID, "episode_id", episodeID, "translation_id", translationID, "media_type", mediaType, "duration", time.Since(start), "status_code", statusCode}
		if err != nil {
			attributes = append(attributes, "error", err)
			if errors.Is(err, context.Canceled) {
				s.logger.InfoContext(ctx, "playback resolution canceled", attributes...)
			} else {
				s.logger.WarnContext(ctx, "playback resolution failed", attributes...)
			}
		} else {
			s.logger.InfoContext(ctx, "playback resolved", attributes...)
		}
	}()
	selection, err := s.provider.GetPlayer(ctx, animeID, episodeID, playerID)
	if err != nil {
		return nil, fmt.Errorf("discover playback player: %w", err)
	}
	if selection == nil || selection.Player.ID != playerID || selection.EpisodeID != episodeID || selection.TranslationID != strings.Split(playerID, ":")[1] || !domain.ValidAnimeID(selection.AnimeID) {
		return nil, fmt.Errorf("provider returned an inconsistent player selection")
	}
	stage, providerName, translationID = "player", selection.Player.Provider, selection.TranslationID
	resolveCtx, stop := context.WithTimeout(ctx, s.playerTimeout)
	defer stop()
	sources, err := s.players.Resolve(resolveCtx, *selection)
	if err != nil {
		return nil, fmt.Errorf("resolve playback source: %w", err)
	}
	return &domain.Playback{AnimeID: selection.AnimeID, EpisodeID: episodeID, TranslationID: selection.TranslationID, PlayerID: playerID, Sources: sources}, nil
}
