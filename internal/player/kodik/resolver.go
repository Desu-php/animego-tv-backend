package kodik

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"animego/backend/internal/domain"
	"animego/backend/internal/player"
	"animego/backend/internal/provider"
	"github.com/PuerkitoBio/goquery"
)

const EmbedHost = "kodikplayer.com"

var (
	embedPath          = regexp.MustCompile(`^/(seria|video)/([1-9][0-9]{0,17})/([a-f0-9]{32})/[0-9]{3,4}p$`)
	paramsAssignment   = regexp.MustCompile(`\bvar\s+urlParams\s*=\s*'(\{[^'\r\n]*\})'\s*;`)
	bundlePath         = regexp.MustCompile(`^/assets/js/app\.player_single\.[a-f0-9]+\.js$`)
	endpointAssignment = regexp.MustCompile(`\burl\s*:\s*atob\("([A-Za-z0-9+/=]+)"\)`)
	endpointPath       = regexp.MustCompile(`^/[A-Za-z0-9_-]{1,64}$`)
)

type Resolver struct {
	api   player.FormFetcher
	media player.Fetcher
}

func NewResolver(api player.FormFetcher, media player.Fetcher) *Resolver {
	return &Resolver{api: api, media: media}
}
func (*Resolver) Supports(s domain.PlayerSelection) bool { return s.Player.Provider == "kodik" }

func (r *Resolver) Resolve(ctx context.Context, s domain.PlayerSelection) ([]domain.VideoSource, error) {
	if !r.Supports(s) {
		return nil, domain.ErrUnsupportedPlayer
	}
	u, err := player.ValidateURL(s.EmbedURL, []string{EmbedHost})
	if err != nil {
		return nil, domain.ErrUnsupportedPlayer
	}
	identity := embedPath.FindStringSubmatch(u.Path)
	if identity == nil {
		return nil, domain.ErrUnsupportedPlayer
	}
	body, err := r.api.Fetch(ctx, u.String(), player.MaxResponseBytes)
	if err != nil {
		return nil, player.ResolutionError("read Kodik embed", err)
	}
	form, scriptURL, err := parseEmbed(body, u, identity)
	if err != nil {
		return nil, err
	}
	script, err := r.api.Fetch(ctx, scriptURL, player.MaxResponseBytes)
	if err != nil {
		return nil, player.ResolutionError("read Kodik player contract", err)
	}
	endpoint, err := parseEndpoint(script, u)
	if err != nil {
		return nil, err
	}
	data, err := r.api.PostForm(ctx, endpoint, form, player.MaxResponseBytes)
	if err != nil {
		return nil, player.ResolutionError("read Kodik sources", err)
	}
	sources, err := parseSources(data)
	if err != nil {
		return nil, err
	}
	return player.AvailableSources(ctx, sources, func(ctx context.Context, source domain.VideoSource) error {
		return player.CheckManifest(ctx, r.media, source, source.Quality == "auto")
	})
}

func parseEmbed(data []byte, base *url.URL, identity []string) (url.Values, string, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("%w: invalid Kodik HTML", provider.ErrParsing)
	}
	var params []string
	var scripts []string
	doc.Find("script").Each(func(_ int, s *goquery.Selection) {
		for _, match := range paramsAssignment.FindAllStringSubmatch(s.Text(), -1) {
			params = append(params, match[1])
		}
		if src, ok := s.Attr("src"); ok && bundlePath.MatchString(src) {
			scripts = append(scripts, src)
		}
	})
	if len(params) != 1 || len(scripts) != 1 {
		return nil, "", fmt.Errorf("%w: Kodik bootstrap missing or ambiguous", provider.ErrParsing)
	}
	var values map[string]json.RawMessage
	if json.Unmarshal([]byte(params[0]), &values) != nil {
		return nil, "", fmt.Errorf("%w: invalid Kodik parameters", provider.ErrParsing)
	}
	form := url.Values{}
	for _, key := range []string{"d", "d_sign", "pd", "pd_sign", "ref", "ref_sign"} {
		var value string
		if json.Unmarshal(values[key], &value) != nil || len(value) > 2048 || key != "ref" && value == "" {
			return nil, "", fmt.Errorf("%w: missing Kodik source parameters", provider.ErrParsing)
		}
		if key == "ref" {
			value, err = url.QueryUnescape(value)
			if err != nil {
				return nil, "", fmt.Errorf("%w: invalid Kodik referrer", provider.ErrParsing)
			}
		}
		form.Set(key, value)
	}
	form.Set("type", identity[1])
	form.Set("id", identity[2])
	form.Set("hash", identity[3])
	form.Set("bad_user", "false")
	form.Set("cdn_is_working", "true")
	ref, _ := url.Parse(scripts[0])
	return form, base.ResolveReference(ref).String(), nil
}

func parseEndpoint(script []byte, base *url.URL) (string, error) {
	matches := endpointAssignment.FindAllSubmatch(script, -1)
	if len(matches) != 1 {
		return "", fmt.Errorf("%w: Kodik source endpoint missing or ambiguous", provider.ErrParsing)
	}
	decoded, err := base64.StdEncoding.DecodeString(string(matches[0][1]))
	if err != nil || !endpointPath.Match(decoded) {
		return "", fmt.Errorf("%w: invalid Kodik source endpoint", provider.ErrParsing)
	}
	return base.ResolveReference(&url.URL{Path: string(decoded), RawQuery: ""}).String(), nil
}

func parseSources(data []byte) ([]domain.VideoSource, error) {
	var response struct {
		Link  string `json:"link"`
		Links map[string][]struct {
			Src  string `json:"src"`
			Type string `json:"type"`
		} `json:"links"`
	}
	if json.Unmarshal(data, &response) != nil {
		return nil, fmt.Errorf("%w: invalid Kodik source JSON", provider.ErrParsing)
	}
	if response.Link != "" {
		source, err := player.MediaSource(response.Link, domain.VideoHLS, "auto")
		if err != nil {
			return nil, err
		}
		return []domain.VideoSource{source}, nil
	}
	if len(response.Links) > 12 {
		return nil, fmt.Errorf("%w: too many Kodik qualities", provider.ErrParsing)
	}
	var sources []domain.VideoSource
	for quality, entries := range response.Links {
		height, err := strconv.Atoi(quality)
		if err != nil || height < 144 || height > 4320 || len(entries) > 8 {
			return nil, fmt.Errorf("%w: invalid Kodik quality", provider.ErrParsing)
		}
		for _, entry := range entries {
			if strings.ToLower(entry.Type) != "application/x-mpegurl" && strings.ToLower(entry.Type) != "application/vnd.apple.mpegurl" {
				continue
			}
			raw, err := decodeSource(entry.Src)
			if err != nil {
				return nil, err
			}
			source, err := player.MediaSource(raw, domain.VideoHLS, quality)
			if err != nil {
				return nil, err
			}
			sources = append(sources, source)
			break // The first provider-supplied source is the player's primary CDN.
		}
	}
	if len(sources) == 0 {
		return nil, domain.ErrPlaybackUnavailable
	}
	sort.Slice(sources, func(i, j int) bool {
		a, _ := strconv.Atoi(sources[i].Quality)
		b, _ := strconv.Atoi(sources[j].Quality)
		return a > b
	})
	return sources, nil
}

// Kodik's own player applies ROT18 to ASCII letters, then ordinary Base64.
// This is URL serialization, not media decryption or signature generation.
func decodeSource(raw string) (string, error) {
	if strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, "https://") {
		return raw, nil
	}
	if len(raw) == 0 || len(raw) > 16<<10 {
		return "", fmt.Errorf("%w: invalid Kodik encoded source", provider.ErrParsing)
	}
	rotated := []byte(raw)
	for i, c := range rotated {
		if c >= 'a' && c <= 'z' {
			rotated[i] = 'a' + (c-'a'+18)%26
		} else if c >= 'A' && c <= 'Z' {
			rotated[i] = 'A' + (c-'A'+18)%26
		}
	}
	decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(string(rotated), "="))
	if err != nil {
		return "", fmt.Errorf("%w: invalid Kodik encoded source", provider.ErrParsing)
	}
	return string(decoded), nil
}
