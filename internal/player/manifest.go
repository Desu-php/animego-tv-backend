package player

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"

	"animego/backend/internal/domain"
	"animego/backend/internal/provider"
)

const MaxManifestBytes int64 = 256 << 10

var hlsURIAttribute = regexp.MustCompile(`(?:^|[:,])URI="([^"]+)"`)

// ValidateManifest checks an adaptive HLS/DASH manifest or a quality-specific HLS playlist.
// Referenced media is validated without downloading any video segments.
func ValidateManifest(data []byte, source *domain.VideoSource, requireMaster bool) error {
	if source == nil {
		return fmt.Errorf("%w: missing manifest source", provider.ErrParsing)
	}
	if _, err := ValidateMediaURL(source.URL); err != nil {
		return fmt.Errorf("%w: invalid manifest URL", provider.ErrParsing)
	}
	if source.Type == domain.VideoDASH {
		return validateDASH(data, source.URL)
	}
	if source.Type != domain.VideoHLS {
		return fmt.Errorf("%w: unsupported manifest format", provider.ErrParsing)
	}
	text := strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
	if !strings.HasPrefix(text, "#EXTM3U\n") && !strings.HasPrefix(text, "#EXTM3U\r\n") {
		return fmt.Errorf("%w: invalid HLS manifest", provider.ErrParsing)
	}
	base, _ := url.Parse(source.URL)
	variants := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#EXT-X-KEY:") && line != "#EXT-X-KEY:METHOD=NONE" || strings.HasPrefix(line, "#EXT-X-SESSION-KEY:") {
			return fmt.Errorf("%w: encrypted HLS is not supported", domain.ErrPlaybackUnavailable)
		}
		for _, attribute := range hlsURIAttribute.FindAllStringSubmatch(line, -1) {
			if err := validateReference(base, attribute[1]); err != nil {
				return err
			}
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if err := validateReference(base, line); err != nil {
			return err
		}
		variants++
	}
	if variants == 0 || requireMaster && !strings.Contains(text, "#EXT-X-STREAM-INF:") || !requireMaster && !strings.Contains(text, "#EXT-X-STREAM-INF:") && !strings.Contains(text, "#EXTINF:") {
		return fmt.Errorf("%w: unexpected HLS playlist structure", provider.ErrParsing)
	}
	return nil
}

func validateDASH(data []byte, rawURL string) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	base, _ := url.Parse(rawURL)
	root, representations := false, 0
	depth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: invalid DASH XML", provider.ErrParsing)
		}
		if _, ok := token.(xml.EndElement); ok {
			depth--
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if root && depth == 0 {
			return fmt.Errorf("%w: multiple DASH roots", provider.ErrParsing)
		}
		depth++
		if !root {
			if start.Name.Local != "MPD" {
				return fmt.Errorf("%w: expected DASH MPD", provider.ErrParsing)
			}
			root = true
		}
		if start.Name.Local == "ContentProtection" {
			return fmt.Errorf("%w: protected DASH is not supported", domain.ErrPlaybackUnavailable)
		}
		if start.Name.Local == "Representation" {
			representations++
		}
		for _, attribute := range start.Attr {
			switch attribute.Name.Local {
			case "href", "media", "initialization", "sourceURL":
				if err := validateReference(base, attribute.Value); err != nil {
					return err
				}
			}
		}
		if start.Name.Local == "BaseURL" || start.Name.Local == "Location" {
			var reference string
			if err := decoder.DecodeElement(&reference, &start); err != nil {
				return fmt.Errorf("%w: invalid DASH reference", provider.ErrParsing)
			}
			depth--
			if err := validateReference(base, strings.TrimSpace(reference)); err != nil {
				return err
			}
		}
	}
	if !root || representations == 0 {
		return fmt.Errorf("%w: DASH representations missing", provider.ErrParsing)
	}
	return nil
}

func validateReference(base *url.URL, raw string) error {
	reference, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: invalid manifest reference", provider.ErrParsing)
	}
	if _, err := ValidateMediaURL(base.ResolveReference(reference).String()); err != nil {
		return fmt.Errorf("%w: untrusted manifest reference: %w", provider.ErrParsing, err)
	}
	return nil
}
