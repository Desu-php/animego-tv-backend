package player

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"animego/backend/internal/domain"
	"animego/backend/internal/provider"
)

// MediaSource normalizes ordinary protocol-relative provider links to HTTPS.
func MediaSource(raw string, kind domain.VideoType, quality string) (domain.VideoSource, error) {
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	u, err := ValidateMediaURL(raw)
	if err != nil {
		return domain.VideoSource{}, fmt.Errorf("%w: invalid media source: %w", provider.ErrParsing, err)
	}
	return domain.VideoSource{URL: u.String(), Type: kind, Quality: quality}, nil
}

const MaxManifestRequests = 32
const MaxManifestDepth = 2

// ResourceFetcher is an optional transport capability for preserving redirect targets.
type ResourceFetcher interface {
	FetchResource(context.Context, string, int64) ([]byte, string, error)
}

func CheckManifest(ctx context.Context, fetcher Fetcher, source domain.VideoSource, requireMaster bool) error {
	check := manifestCheck{fetcher: fetcher, active: make(map[string]bool), checked: make(map[string]bool)}
	return check.validate(ctx, source, requireMaster, 0)
}

type manifestCheck struct {
	fetcher         Fetcher
	active, checked map[string]bool
	requests        int
}

func (c *manifestCheck) validate(ctx context.Context, source domain.VideoSource, requireMaster bool, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.active[source.URL] {
		return fmt.Errorf("%w: cyclic HLS playlists", provider.ErrParsing)
	}
	if c.checked[source.URL] {
		return nil
	}
	if depth > MaxManifestDepth || c.requests >= MaxManifestRequests {
		return fmt.Errorf("%w: manifest traversal exceeds limits", provider.ErrParsing)
	}
	c.requests++
	c.active[source.URL] = true
	defer delete(c.active, source.URL)
	data, finalURL, err := c.fetch(ctx, source.URL)
	if err != nil {
		return ResolutionError("read media manifest", err)
	}
	resolved := source
	resolved.URL = finalURL
	playlists, err := validateManifest(data, &resolved, requireMaster)
	if err != nil {
		return err
	}
	if source.Type == domain.VideoHLS {
		for _, raw := range playlists {
			child := domain.VideoSource{Type: domain.VideoHLS, URL: raw}
			if err := c.validate(ctx, child, false, depth+1); err != nil {
				return err
			}
		}
	}
	c.checked[source.URL] = true
	return nil
}

func (c *manifestCheck) fetch(ctx context.Context, raw string) ([]byte, string, error) {
	if fetcher, ok := c.fetcher.(ResourceFetcher); ok {
		return fetcher.FetchResource(ctx, raw, MaxManifestBytes)
	}
	data, err := c.fetcher.Fetch(ctx, raw, MaxManifestBytes)
	return data, raw, err
}

// AvailableSources preserves provider ordering and the selected episode/translation.
// Cancellation and unexpected adapter failures are never swallowed.
func AvailableSources(ctx context.Context, sources []domain.VideoSource, check func(context.Context, domain.VideoSource) error) ([]domain.VideoSource, error) {
	var available []domain.VideoSource
	var failures []error
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		err := check(ctx, source)
		if err == nil {
			available = append(available, source)
			continue
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		if !errors.Is(err, provider.ErrUpstream) && !errors.Is(err, domain.ErrPlaybackUnavailable) {
			return nil, err
		}
		failures = append(failures, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(available) > 0 {
		return available, nil
	}
	if len(failures) > 0 {
		return nil, errors.Join(failures...)
	}
	return nil, domain.ErrPlaybackUnavailable
}

func ResolutionError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var upstream *provider.HTTPError
	if errors.As(err, &upstream) && (upstream.StatusCode == 401 || upstream.StatusCode == 403 || upstream.StatusCode == 404 || upstream.StatusCode == 451) {
		return fmt.Errorf("%w: %s: %w", domain.ErrPlaybackUnavailable, operation, err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
