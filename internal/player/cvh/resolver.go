package cvh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"

	"animego/backend/internal/domain"
	"animego/backend/internal/player"
	"animego/backend/internal/provider"
	"github.com/PuerkitoBio/goquery"
)

const APIHost = "plapi.cdnvideohub.com"
const apiOrigin = "https://" + APIHost + "/api/v1"

type EmbedFetcher interface {
	FetchEmbed(context.Context, string) ([]byte, error)
}
type Resolver struct {
	origin *url.URL
	embeds EmbedFetcher
	api    player.Fetcher
	media  player.MediaFetcher
}

func NewResolver(origin string, embeds EmbedFetcher, api player.Fetcher, media player.MediaFetcher) (*Resolver, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("invalid CVH wrapper origin")
	}
	return &Resolver{origin: u, embeds: embeds, api: api, media: media}, nil
}
func (*Resolver) Supports(s domain.PlayerSelection) bool { return s.Player.Provider == "cvh" }

type configuration struct {
	title, publisher, aggregator, voice, onlyVoice string
	episode, season                                int
}

func (r *Resolver) Resolve(ctx context.Context, s domain.PlayerSelection) ([]domain.VideoSource, error) {
	if !r.Supports(s) {
		return nil, domain.ErrUnsupportedPlayer
	}
	u, err := url.Parse(s.EmbedURL)
	if err != nil || u.Scheme != r.origin.Scheme || !strings.EqualFold(u.Host, r.origin.Host) || u.User != nil || u.Fragment != "" || !strings.HasPrefix(u.Path, "/cdn-iframe/") || path.Clean(u.Path) != u.Path {
		return nil, domain.ErrUnsupportedPlayer
	}
	body, err := r.embeds.FetchEmbed(ctx, u.String())
	if err != nil {
		return nil, player.ResolutionError("read CVH wrapper", err)
	}
	config, err := parseConfiguration(body)
	if err != nil {
		return nil, err
	}
	query := url.Values{"id": {config.title}, "pub": {config.publisher}}
	if config.aggregator != "" {
		query.Set("aggr", config.aggregator)
	}
	if config.episode != 0 {
		query.Set("ep", strconv.Itoa(config.episode))
	}
	if config.season != 0 {
		query.Set("sn", strconv.Itoa(config.season))
	}
	if config.onlyVoice != "" {
		query.Set("vstu", config.onlyVoice)
	}
	data, err := r.api.Fetch(ctx, apiOrigin+"/player/sv/playlist?"+query.Encode(), player.MaxResponseBytes)
	if err != nil {
		return nil, player.ResolutionError("read CVH playlist", err)
	}
	id, err := selectVideo(data, config)
	if err != nil {
		return nil, err
	}
	data, err = r.api.Fetch(ctx, apiOrigin+"/player/sv/video/"+id, player.MaxResponseBytes)
	if err != nil {
		return nil, player.ResolutionError("read CVH sources", err)
	}
	sources, err := parseSources(data)
	if err != nil {
		return nil, err
	}
	return player.AvailableSources(ctx, sources, func(ctx context.Context, source domain.VideoSource) error {
		if source.Type == domain.VideoMP4 {
			return player.ResolutionError("read CVH MP4 metadata", r.media.Probe(ctx, source.URL))
		}
		return player.CheckManifest(ctx, r.media, source, true)
	})
}

func parseConfiguration(data []byte) (*configuration, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid CVH HTML", provider.ErrParsing)
	}
	widget := doc.Find("video-player#pcvh")
	if widget.Length() != 1 {
		return nil, fmt.Errorf("%w: CVH configuration missing or ambiguous", provider.ErrParsing)
	}
	c := &configuration{}
	c.title, _ = widget.Attr("data-title-id")
	c.publisher, _ = widget.Attr("data-publisher-id")
	if !domain.ValidEpisodeID(c.title) || !domain.ValidEpisodeID(c.publisher) {
		return nil, fmt.Errorf("%w: invalid CVH title/publisher", provider.ErrParsing)
	}
	c.voice, _ = widget.Attr("priority-voice")
	c.onlyVoice, _ = widget.Attr("only-voice")
	c.aggregator, _ = widget.Attr("data-aggregator")
	if c.onlyVoice != "" {
		c.voice = c.onlyVoice
	}
	if len(c.voice) > 200 || len(c.aggregator) > 64 {
		return nil, fmt.Errorf("%w: invalid CVH selection", provider.ErrParsing)
	}
	for _, attribute := range []struct {
		name   string
		target *int
	}{{"episode", &c.episode}, {"season", &c.season}} {
		if raw, ok := widget.Attr(attribute.name); ok {
			value, err := strconv.Atoi(raw)
			if err != nil || value <= 0 || value > 10000 {
				return nil, fmt.Errorf("%w: invalid CVH episode/season", provider.ErrParsing)
			}
			*attribute.target = value
		}
	}
	return c, nil
}

func blocked(tags []int) bool {
	for _, tag := range tags {
		if tag == 5 {
			return true
		}
	}
	return false
}

func selectVideo(data []byte, c *configuration) (string, error) {
	var playlist struct {
		IsSerial bool  `json:"isSerial"`
		Tags     []int `json:"tags"`
		Items    *[]struct {
			ID              string `json:"vkId"`
			Voice           string `json:"voiceStudio"`
			Episode, Season int
			Tags            []int `json:"tags"`
		} `json:"items"`
	}
	if json.Unmarshal(data, &playlist) != nil {
		return "", fmt.Errorf("%w: invalid CVH playlist JSON", provider.ErrParsing)
	}
	if blocked(playlist.Tags) {
		return "", domain.ErrPlaybackUnavailable
	}
	if playlist.Items == nil {
		return "", fmt.Errorf("%w: CVH playlist items missing", provider.ErrParsing)
	}
	if len(*playlist.Items) > 10000 {
		return "", fmt.Errorf("%w: CVH playlist too large", provider.ErrParsing)
	}
	if playlist.IsSerial && c.episode == 0 {
		return "", fmt.Errorf("%w: CVH serial episode missing", provider.ErrParsing)
	}
	var selected string
	for _, item := range *playlist.Items {
		if c.episode != 0 && item.Episode != c.episode || c.season != 0 && item.Season != c.season || c.voice != "" && !strings.EqualFold(strings.TrimSpace(item.Voice), strings.TrimSpace(c.voice)) {
			continue
		}
		if blocked(item.Tags) {
			return "", domain.ErrPlaybackUnavailable
		}
		if !domain.ValidEpisodeID(item.ID) {
			return "", fmt.Errorf("%w: invalid CVH video identity", provider.ErrParsing)
		}
		if selected != "" {
			return "", fmt.Errorf("%w: ambiguous CVH episode/voice", provider.ErrParsing)
		}
		selected = item.ID
	}
	if selected == "" {
		return "", domain.ErrPlaybackUnavailable
	}
	return selected, nil
}

func parseSources(data []byte) ([]domain.VideoSource, error) {
	var video struct {
		Tags    []int `json:"tags"`
		Sources *struct {
			HLS    string `json:"hlsUrl"`
			DASH   string `json:"dashUrl"`
			QHD    string `json:"mpegQhdUrl"`
			TwoK   string `json:"mpeg2kUrl"`
			FourK  string `json:"mpeg4kUrl"`
			High   string `json:"mpegHighUrl"`
			FullHD string `json:"mpegFullHdUrl"`
			Medium string `json:"mpegMediumUrl"`
			Low    string `json:"mpegLowUrl"`
			Lowest string `json:"mpegLowestUrl"`
			Tiny   string `json:"mpegTinyUrl"`
		} `json:"sources"`
	}
	if json.Unmarshal(data, &video) != nil {
		return nil, fmt.Errorf("%w: invalid CVH source JSON", provider.ErrParsing)
	}
	if blocked(video.Tags) || video.Sources == nil {
		return nil, domain.ErrPlaybackUnavailable
	}
	raw, kind := video.Sources.HLS, domain.VideoHLS
	if raw == "" {
		raw, kind = video.Sources.DASH, domain.VideoDASH
	}
	if raw != "" {
		source, err := player.MediaSource(raw, kind, "auto")
		if err != nil {
			return nil, err
		}
		return []domain.VideoSource{source}, nil
	}
	var sources []domain.VideoSource
	for _, item := range []struct{ raw, quality string }{
		{video.Sources.FourK, "4k"}, {video.Sources.TwoK, "2k"}, {video.Sources.QHD, "qhd"},
		{video.Sources.FullHD, "1080"}, {video.Sources.High, "720"}, {video.Sources.Medium, "480"},
		{video.Sources.Low, "360"}, {video.Sources.Lowest, "240"}, {video.Sources.Tiny, "144"},
	} {
		if item.raw == "" {
			continue
		}
		source, err := player.MediaSource(item.raw, domain.VideoMP4, item.quality)
		if err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	if len(sources) == 0 {
		return nil, domain.ErrPlaybackUnavailable
	}
	return sources, nil
}
