package domain

import (
	"errors"
	"strings"
	"time"
)

type VideoType string

const (
	VideoHLS  VideoType = "hls"
	VideoDASH VideoType = "dash"
	VideoMP4  VideoType = "mp4"
)

type VideoSource struct {
	Type      VideoType         `json:"type"`
	URL       string            `json:"url"`
	Quality   string            `json:"quality,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	ExpiresAt *time.Time        `json:"expires_at,omitempty"`
}

type Playback struct {
	AnimeID       string        `json:"anime_id"`
	EpisodeID     string        `json:"episode_id"`
	TranslationID string        `json:"translation_id"`
	PlayerID      string        `json:"player_id"`
	Sources       []VideoSource `json:"sources"`
}

// PlayerSelection is internal resolver input. EmbedURL never appears in API JSON.
type PlayerSelection struct {
	AnimeID       string
	EpisodeID     string
	TranslationID string
	Player        PlayerReference
	EmbedURL      string `json:"-"`
}

var (
	ErrPlayerNotFound      = errors.New("player not found")
	ErrUnsupportedPlayer   = errors.New("unsupported player")
	ErrPlaybackUnavailable = errors.New("playback unavailable")
)

func ValidPlayerID(id, episodeID string) bool {
	parts := strings.Split(id, ":")
	return len(parts) == 3 && parts[0] == episodeID && ValidEpisodeID(parts[0]) && ValidEpisodeID(parts[1]) && ValidEpisodeID(parts[2])
}
