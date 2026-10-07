package aniboom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strings"

	"animego/backend/internal/domain"
	"animego/backend/internal/player"
	"animego/backend/internal/provider"
	"github.com/PuerkitoBio/goquery"
)

const (
	EmbedHost             = "aniboom.one"
	configurationSelector = `video#video[data-parameters]`
)

type Resolver struct {
	embedFetcher player.Fetcher
	mediaFetcher player.Fetcher
}

func NewResolver(embedFetcher, mediaFetcher player.Fetcher) *Resolver {
	return &Resolver{embedFetcher: embedFetcher, mediaFetcher: mediaFetcher}
}

func (r *Resolver) Supports(selection domain.PlayerSelection) bool {
	return selection.Player.Provider == "aniboom"
}

func (r *Resolver) Resolve(ctx context.Context, selection domain.PlayerSelection) ([]domain.VideoSource, error) {
	if !r.Supports(selection) {
		return nil, domain.ErrUnsupportedPlayer
	}
	u, err := player.ValidateURL(selection.EmbedURL, []string{EmbedHost})
	if err != nil || !validEmbedPath(u) {
		return nil, fmt.Errorf("%w: unsupported AniBoom embed reference", domain.ErrUnsupportedPlayer)
	}
	html, err := r.embedFetcher.Fetch(ctx, u.String(), player.MaxResponseBytes)
	if err != nil {
		return nil, player.ResolutionError("read AniBoom embed", err)
	}
	source, err := parseConfiguration(html)
	if err != nil {
		return nil, err
	}
	if err := player.CheckManifest(ctx, r.mediaFetcher, *source, true); err != nil {
		return nil, err
	}
	return []domain.VideoSource{*source}, nil
}

func validEmbedPath(u *url.URL) bool {
	if u == nil || !strings.HasPrefix(u.Path, "/embed/") {
		return false
	}
	id := strings.TrimPrefix(u.Path, "/embed/")
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func parseConfiguration(html []byte) (*domain.VideoSource, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("%w: read AniBoom HTML: %w", provider.ErrParsing, err)
	}
	video := doc.Find(configurationSelector)
	parameters, exists := video.Attr("data-parameters")
	if video.Length() != 1 || !exists {
		return nil, fmt.Errorf("%w: AniBoom player configuration missing", provider.ErrParsing)
	}
	var config struct {
		HLS  string `json:"hls"`
		DASH string `json:"dash"`
	}
	if err := json.Unmarshal([]byte(parameters), &config); err != nil {
		return nil, fmt.Errorf("%w: invalid AniBoom configuration JSON", provider.ErrParsing)
	}
	// The observed fields contain JSON strings, not executable JavaScript or
	// encrypted payloads. Prefer the adaptive HLS master when it is available.
	for _, candidate := range []struct {
		raw             string
		kind            domain.VideoType
		mime, extension string
	}{
		{config.HLS, domain.VideoHLS, "application/x-mpegurl", ".m3u8"},
		{config.DASH, domain.VideoDASH, "application/dash+xml", ".mpd"},
	} {
		if candidate.raw == "" || candidate.raw == "null" {
			continue
		}
		var media struct {
			Src  string `json:"src"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(candidate.raw), &media); err != nil {
			return nil, fmt.Errorf("%w: invalid AniBoom source JSON", provider.ErrParsing)
		}
		if media.Src == "" {
			continue
		}
		if strings.ToLower(media.Type) != candidate.mime {
			return nil, fmt.Errorf("%w: unsupported AniBoom media format", domain.ErrPlaybackUnavailable)
		}
		// The source comes from the verified AniBoom embed, not client input.
		// Validate its URL independently of the provider's changing CDN names.
		u, err := player.ValidateMediaURL(media.Src)
		if err != nil {
			return nil, fmt.Errorf("%w: untrusted AniBoom media URL: %w", provider.ErrParsing, err)
		}
		if strings.ToLower(path.Ext(u.Path)) != candidate.extension {
			return nil, fmt.Errorf("%w: unsupported AniBoom media extension", domain.ErrPlaybackUnavailable)
		}
		return &domain.VideoSource{Type: candidate.kind, URL: u.String(), Quality: "auto"}, nil
	}
	return nil, domain.ErrPlaybackUnavailable
}
