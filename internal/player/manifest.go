package player

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"strings"

	"animego/backend/internal/domain"
	"animego/backend/internal/provider"
)

const MaxManifestBytes int64 = 256 << 10

// ValidateManifest checks an adaptive HLS/DASH manifest or a quality-specific HLS playlist.
// Referenced media is validated without downloading any video segments.
func ValidateManifest(data []byte, source *domain.VideoSource, requireMaster bool) error {
	_, err := validateManifest(data, source, requireMaster)
	return err
}

// validateManifest returns only child playlists; segment URLs are validated but
// never returned to the traversal or fetched by the backend.
func validateManifest(data []byte, source *domain.VideoSource, requireMaster bool) ([]string, error) {
	if int64(len(data)) > MaxManifestBytes {
		return nil, fmt.Errorf("%w: manifest exceeds size limit", provider.ErrParsing)
	}
	if source == nil {
		return nil, fmt.Errorf("%w: missing manifest source", provider.ErrParsing)
	}
	if _, err := ValidateMediaURL(source.URL); err != nil {
		return nil, fmt.Errorf("%w: invalid manifest URL", provider.ErrParsing)
	}
	if source.Type == domain.VideoDASH {
		return nil, validateDASH(data, source.URL)
	}
	if source.Type != domain.VideoHLS {
		return nil, fmt.Errorf("%w: unsupported manifest format", provider.ErrParsing)
	}
	return parseHLS(data, source.URL, requireMaster)
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
