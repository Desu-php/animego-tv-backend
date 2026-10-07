package player

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"animego/backend/internal/domain"
	"animego/backend/internal/provider"
)

type hlsURIKind uint8

const (
	hlsNoURI hlsURIKind = iota
	hlsVariantURI
	hlsSegmentURI
)

type hlsManifest struct {
	base               *url.URL
	pending            hlsURIKind
	variants, segments int
	masterTags         bool
	playlists          []string
	seen               map[string]bool
}

func parseHLS(data []byte, rawURL string, requireMaster bool) ([]string, error) {
	if !utf8.Valid(data) {
		return nil, malformedHLS("invalid UTF-8")
	}
	text := strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
	lines := strings.Split(text, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "#EXTM3U" {
		return nil, malformedHLS("missing header")
	}
	base, _ := url.Parse(rawURL) // validateManifest has already checked the source.
	manifest := hlsManifest{base: base, seen: make(map[string]bool)}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if err := manifest.readLine(line); err != nil {
			return nil, err
		}
	}
	if manifest.pending != hlsNoURI {
		return nil, malformedHLS("missing URI after a media tag")
	}
	if manifest.variants == 0 && manifest.segments == 0 {
		return nil, malformedHLS("empty playlist")
	}
	if manifest.masterTags && manifest.segments > 0 {
		return nil, malformedHLS("mixed master and media playlist")
	}
	if requireMaster && manifest.variants == 0 {
		return nil, malformedHLS("expected master playlist")
	}
	return manifest.playlists, nil
}

func (m *hlsManifest) readLine(line string) error {
	if !strings.HasPrefix(line, "#") {
		return m.readURI(line)
	}
	if !strings.HasPrefix(line, "#EXT") {
		return nil
	} // Ordinary comments.
	if m.pending == hlsVariantURI {
		return malformedHLS("stream tag is not followed by a URI")
	}
	tag, value, _ := strings.Cut(line, ":")
	switch tag {
	case "#EXTM3U":
		return malformedHLS("duplicate header")
	case "#EXT-X-SESSION-KEY":
		return fmt.Errorf("%w: encrypted HLS is not supported", domain.ErrPlaybackUnavailable)
	case "#EXTINF":
		if m.pending != hlsNoURI {
			return malformedHLS("segment tag is not followed by a URI")
		}
		duration, _, comma := strings.Cut(value, ",")
		seconds, err := strconv.ParseFloat(duration, 64)
		if !comma || err != nil || seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			return malformedHLS("invalid segment duration")
		}
		m.pending = hlsSegmentURI
		return nil
	case "#EXT-X-STREAM-INF", "#EXT-X-MEDIA", "#EXT-X-I-FRAME-STREAM-INF", "#EXT-X-KEY", "#EXT-X-MAP", "#EXT-X-SESSION-DATA":
		return m.readAttributes(tag, value)
	default:
		// Validate URI attributes in extension tags without fetching media parts.
		if strings.Contains(value, "URI=") {
			return m.readAttributes(tag, value)
		}
		return nil
	}
}

func (m *hlsManifest) readURI(raw string) error {
	if m.pending == hlsNoURI {
		return malformedHLS("URI without a stream or segment tag")
	}
	if err := validateReference(m.base, raw); err != nil {
		return err
	}
	if m.pending == hlsVariantURI {
		m.variants++
		m.addPlaylist(raw)
	} else {
		m.segments++
	}
	m.pending = hlsNoURI
	return nil
}

func (m *hlsManifest) readAttributes(tag, value string) error {
	attributes, err := parseHLSAttributes(value)
	if err != nil {
		return err
	}
	switch tag {
	case "#EXT-X-KEY":
		if attributes["METHOD"].value != "NONE" {
			return fmt.Errorf("%w: encrypted HLS is not supported", domain.ErrPlaybackUnavailable)
		}
		if len(attributes) != 1 {
			return malformedHLS("unexpected attributes for METHOD=NONE")
		}
	case "#EXT-X-STREAM-INF", "#EXT-X-I-FRAME-STREAM-INF":
		if m.pending != hlsNoURI {
			return malformedHLS("mixed stream and segment tags")
		}
		bandwidth := attributes["BANDWIDTH"]
		rate, err := strconv.ParseUint(bandwidth.value, 10, 64)
		if err != nil || rate == 0 || bandwidth.quoted {
			return malformedHLS("missing or invalid bandwidth")
		}
		m.masterTags = true
		if tag == "#EXT-X-STREAM-INF" {
			m.pending = hlsVariantURI
		}
	case "#EXT-X-MEDIA":
		m.masterTags = true
	}
	uri, hasURI := attributes["URI"]
	if !hasURI {
		if tag == "#EXT-X-I-FRAME-STREAM-INF" || tag == "#EXT-X-MAP" || tag == "#EXT-X-MEDIA" && attributes["TYPE"].value == "SUBTITLES" {
			return malformedHLS("required URI attribute missing")
		}
		return nil
	}
	if !uri.quoted || uri.value == "" {
		return malformedHLS("URI must be a nonempty quoted string")
	}
	if err := validateReference(m.base, uri.value); err != nil {
		return err
	}
	if tag == "#EXT-X-MEDIA" || tag == "#EXT-X-I-FRAME-STREAM-INF" {
		m.addPlaylist(uri.value)
	}
	return nil
}

func (m *hlsManifest) addPlaylist(raw string) {
	reference, _ := url.Parse(raw) // readURI/readAttributes have validated the reference.
	resolved := m.base.ResolveReference(reference).String()
	if !m.seen[resolved] {
		m.seen[resolved] = true
		m.playlists = append(m.playlists, resolved)
	}
}

type hlsAttribute struct {
	value  string
	quoted bool
}

// Attribute values can contain commas inside quotes (CODECS and signed URLs).
func parseHLSAttributes(raw string) (map[string]hlsAttribute, error) {
	attributes := make(map[string]hlsAttribute)
	for raw != "" {
		name, rest, found := strings.Cut(raw, "=")
		name = strings.TrimSpace(name)
		if !found || name == "" {
			return nil, malformedHLS("invalid attribute list")
		}
		for _, c := range name {
			if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return nil, malformedHLS("invalid attribute name")
			}
		}
		if _, duplicate := attributes[name]; duplicate {
			return nil, malformedHLS("duplicate attribute")
		}
		rest = strings.TrimSpace(rest)
		attribute := hlsAttribute{quoted: strings.HasPrefix(rest, `"`)}
		if attribute.quoted {
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				return nil, malformedHLS("unterminated quoted attribute")
			}
			attribute.value, raw = rest[1:end+1], strings.TrimSpace(rest[end+2:])
			if raw != "" {
				if raw[0] != ',' {
					return nil, malformedHLS("missing attribute separator")
				}
				raw = raw[1:]
				if strings.TrimSpace(raw) == "" {
					return nil, malformedHLS("trailing attribute separator")
				}
			}
		} else {
			var comma bool
			attribute.value, raw, comma = strings.Cut(rest, ",")
			attribute.value = strings.TrimSpace(attribute.value)
			if attribute.value == "" || strings.ContainsAny(attribute.value, "\"\r\n") {
				return nil, malformedHLS("invalid attribute value")
			}
			if comma && strings.TrimSpace(raw) == "" {
				return nil, malformedHLS("trailing attribute separator")
			}
		}
		attributes[name] = attribute
	}
	if len(attributes) == 0 {
		return nil, malformedHLS("empty attribute list")
	}
	return attributes, nil
}

func malformedHLS(reason string) error {
	return fmt.Errorf("%w: invalid HLS manifest: %s", provider.ErrParsing, reason)
}
