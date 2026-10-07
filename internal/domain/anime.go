package domain

import (
	"errors"
	"fmt"
)

// Anime is the catalog representation shared with clients and providers.
type Anime struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	AltTitle string `json:"alt_title,omitempty"`
	Poster   string `json:"poster,omitempty"`
	Year     int    `json:"year,omitempty"`
	Status   string `json:"status,omitempty"`
}

var ErrInvalidQuery = errors.New("invalid search query")

var (
	ErrInvalidIdentifier = errors.New("invalid identifier")
	ErrAnimeNotFound     = errors.New("anime not found")
	ErrEpisodeNotFound   = errors.New("episode not found")
)

type AnimeDetails struct {
	Anime
	Description  string   `json:"description,omitempty"`
	Type         string   `json:"type,omitempty"`
	Genres       []string `json:"genres,omitempty"`
	EpisodeCount int      `json:"episode_count,omitempty"`
}

type Episode struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
	Title  string `json:"title,omitempty"`
}

// Translation groups the players available for one episode's translation.
// Type is omitted when the upstream does not explicitly identify it.
type Translation struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Type    string            `json:"type,omitempty"`
	Players []PlayerReference `json:"players"`
}

// PlayerReference identifies a choice that the provider can re-read in Phase 3.
// It contains no iframe URL or upstream markup.
type PlayerReference struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Name     string `json:"name,omitempty"`
}

type ValidationError struct {
	Reason string
}

func (e *ValidationError) Error() string { return fmt.Sprintf("%s: %s", ErrInvalidQuery, e.Reason) }
func (e *ValidationError) Unwrap() error { return ErrInvalidQuery }
