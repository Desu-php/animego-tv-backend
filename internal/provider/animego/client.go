package animego

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"animego/backend/internal/apperror"
	"animego/backend/internal/cache"
	"animego/backend/internal/domain"
	"animego/backend/internal/provider"
)

const (
	maxResponseBytes = 4 << 20
	userAgent        = "animego-tv-backend/0.1"
)

type Client struct {
	baseURL                 *url.URL
	httpClient              *http.Client
	logger                  *slog.Logger
	cacheCapacity           int
	detailsTTL, episodesTTL time.Duration
	pages                   *cache.Cache[*animePage]
	episodes                *cache.Cache[[]domain.Episode]
	requestConcurrency      int
	requestSlots            chan struct{}
}

type ClientOption func(*Client)

func WithLogger(logger *slog.Logger) ClientOption {
	return func(c *Client) {
		if logger != nil {
			c.logger = logger
		}
	}
}

var _ provider.AnimeProvider = (*Client)(nil)

// NewClient requires a finite HTTP timeout. The injected client is reusable.
func NewClient(baseURL string, httpClient *http.Client, options ...ClientOption) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse AnimeGO base URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("AnimeGO base URL must be an absolute HTTP(S) origin without credentials, query, or fragment")
	}
	if httpClient == nil || httpClient.Timeout <= 0 {
		return nil, fmt.Errorf("AnimeGO HTTP client must have a positive timeout")
	}
	// Copy the injected client so applying redirect restrictions does not mutate it.
	checkedClient := *httpClient
	previousRedirect := httpClient.CheckRedirect
	checkedClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !sameOrigin(u, req.URL) {
			return fmt.Errorf("redirect outside configured AnimeGO origin rejected")
		}
		if len(via) >= 10 {
			return fmt.Errorf("too many AnimeGO redirects")
		}
		if previousRedirect != nil {
			return previousRedirect(req, via)
		}
		return nil
	}
	c := &Client{baseURL: u, httpClient: &checkedClient, logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), requestConcurrency: 16}
	for _, option := range options {
		option(c)
	}
	if c.requestConcurrency <= 0 {
		return nil, fmt.Errorf("AnimeGO request concurrency must be positive")
	}
	c.requestSlots = make(chan struct{}, c.requestConcurrency)
	if err := c.initializeCache(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) getHTML(ctx context.Context, path string, query url.Values) ([]byte, *url.URL, error) {
	return c.request(ctx, path, query, requestOptions{accept: "text/html", operation: "search"})
}

// FetchEmbed reads AnimeGO's CVH wrapper using the configured-origin policy.
// It is only called with a freshly selected internal player reference.
func (c *Client) FetchEmbed(ctx context.Context, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || !sameOrigin(c.baseURL, u) || !strings.HasPrefix(u.Path, "/cdn-iframe/") || path.Clean(u.Path) != u.Path || u.Fragment != "" {
		return nil, fmt.Errorf("%w: invalid AnimeGO embed reference", provider.ErrParsing)
	}
	data, _, err := c.request(ctx, u.Path, u.Query(), requestOptions{accept: "text/html", referer: "/", operation: "embed", limit: 2 << 20})
	return data, err
}

type requestOptions struct {
	limit     int64
	accept    string
	referer   string
	operation string
	animeID   string
	episodeID string
}

func sameOrigin(base, candidate *url.URL) bool {
	return strings.EqualFold(base.Scheme, candidate.Scheme) && strings.EqualFold(base.Host, candidate.Host) && candidate.User == nil
}

func (c *Client) request(ctx context.Context, path string, query url.Values, options requestOptions) ([]byte, *url.URL, error) {
	limit := options.limit
	if limit == 0 {
		limit = maxResponseBytes
	}
	u := c.baseURL.ResolveReference(&url.URL{Path: path, RawQuery: query.Encode()})
	if !sameOrigin(c.baseURL, u) {
		return nil, nil, fmt.Errorf("%w: request outside configured origin rejected", provider.ErrHTTP)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, &provider.HTTPError{Err: err}
	}
	select {
	case c.requestSlots <- struct{}{}:
		defer func() { <-c.requestSlots }()
	default:
		return nil, nil, apperror.ErrBusy
	}
	start := time.Now()
	statusCode := 0
	defer func() {
		c.logger.InfoContext(ctx, "AnimeGO request", "provider", "animego", "operation", options.operation, "anime_id", options.animeID, "episode_id", options.episodeID, "status_code", statusCode, "duration", time.Since(start))
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create AnimeGO request: %w", &provider.HTTPError{Err: err})
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", options.accept)
	if options.accept == "application/json" {
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
	}
	if options.referer != "" {
		req.Header.Set("Referer", c.baseURL.ResolveReference(&url.URL{Path: options.referer}).String())
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if urlErr, ok := err.(*url.Error); ok {
			err = urlErr.Err
		}
		return nil, nil, fmt.Errorf("request AnimeGO %s: %w", options.operation, &provider.HTTPError{Err: err})
	}
	defer resp.Body.Close()
	statusCode = resp.StatusCode
	fail := func(err error) ([]byte, *url.URL, error) {
		return nil, nil, &provider.HTTPError{StatusCode: resp.StatusCode, Err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fail(fmt.Errorf("unexpected upstream status %d", resp.StatusCode))
	}
	contentType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	validType := contentType == options.accept || options.accept == "text/html" && contentType == "application/xhtml+xml"
	if err != nil || !validType {
		return nil, nil, fmt.Errorf("%w: AnimeGO returned unexpected content type %q", provider.ErrParsing, resp.Header.Get("Content-Type"))
	}
	if resp.ContentLength > limit {
		return fail(fmt.Errorf("AnimeGO response exceeds %d bytes", limit))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return fail(fmt.Errorf("read AnimeGO response: %w", err))
	}
	if int64(len(body)) > limit {
		return fail(fmt.Errorf("AnimeGO response exceeds %d bytes", limit))
	}
	if strings.TrimSpace(string(body)) == "" {
		return nil, nil, fmt.Errorf("%w: AnimeGO returned an empty body", provider.ErrParsing)
	}
	return body, resp.Request.URL, nil
}
