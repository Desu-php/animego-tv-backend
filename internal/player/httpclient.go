package player

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"animego/backend/internal/provider"
)

const MaxResponseBytes int64 = 2 << 20

// Fetcher is the narrow HTTP dependency used by provider-specific resolvers.
type Fetcher interface {
	Fetch(context.Context, string, int64) ([]byte, error)
}

type FormFetcher interface {
	Fetcher
	PostForm(context.Context, string, url.Values, int64) ([]byte, error)
}

type MediaFetcher interface {
	Fetcher
	Probe(context.Context, string) error
}

type HTTPClient struct {
	client   *http.Client
	hosts    map[string]bool
	referers map[string]string
}

type ClientOption func(*HTTPClient) error

// WithReferer supplies a verified public origin header only to the named host.
// It cannot forward cookies or other upstream credentials.
func WithReferer(host, origin string) ClientOption {
	return func(c *HTTPClient) error {
		if err := c.validate("https://" + host + "/"); err != nil {
			return fmt.Errorf("untrusted Referer target host")
		}
		u, err := url.Parse(origin)
		if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return fmt.Errorf("player Referer must be an HTTP(S) origin without credentials, path, query, or fragment")
		}
		u.Path = "/"
		c.referers[strings.ToLower(host)] = u.String()
		return nil
	}
}

func NewHTTPClient(timeout time.Duration, hosts []string, options ...ClientOption) (*HTTPClient, error) {
	if timeout <= 0 || len(hosts) == 0 {
		return nil, fmt.Errorf("player client needs a positive timeout and explicit hosts")
	}
	allow := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		if _, err := ValidateURL("https://"+host+"/", hosts); err != nil {
			return nil, fmt.Errorf("invalid player host configuration: %w", err)
		}
		allow[strings.ToLower(host)] = true
	}
	c := newHTTPClient(timeout, allow)
	for _, option := range options {
		if err := option(c); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// NewMediaHTTPClient fetches URLs discovered in a trusted provider response.
// CDN names may rotate; HTTPS validation and public-address pinning apply to
// every connection, including redirects. Never use this for user-supplied URLs.
// This separate client has no embed Referer or other provider credentials.
func NewMediaHTTPClient(timeout time.Duration) (*HTTPClient, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("media client needs a positive timeout")
	}
	return newHTTPClient(timeout, nil), nil
}

func newHTTPClient(timeout time.Duration, hosts map[string]bool) *HTTPClient {
	transport := &http.Transport{
		// No environment proxy: the validated addresses are dialed directly.
		DialContext:           dialPublic,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       60 * time.Second,
		MaxIdleConns:          20,
		ForceAttemptHTTP2:     true,
	}
	c := &HTTPClient{client: &http.Client{Timeout: timeout, Transport: transport}, hosts: hosts, referers: make(map[string]string)}
	c.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("player redirect limit exceeded")
		}
		if err := c.validate(req.URL.String()); err != nil {
			return err
		}
		if via[0].Method == http.MethodPost && (req.URL.Scheme != via[0].URL.Scheme || !strings.EqualFold(req.URL.Host, via[0].URL.Host)) {
			return fmt.Errorf("player form redirect outside original origin rejected")
		}
		c.setHeaders(req)
		return nil
	}
	return c
}

func (c *HTTPClient) CloseIdleConnections() { c.client.CloseIdleConnections() }

// ValidateURL checks syntax and explicit host policy. dialPublic separately
// validates DNS answers and pins the connection to the address it checked.
func ValidateURL(raw string, hosts []string) (*url.URL, error) {
	u, err := validateHTTPSURL(raw)
	if err != nil {
		return nil, err
	}
	host := strings.ToLower(u.Hostname())
	for _, allowed := range hosts {
		if host == strings.ToLower(allowed) {
			return u, nil
		}
	}
	return nil, fmt.Errorf("player host is outside the explicit allowlist")
}

// ValidateMediaURL validates a provider-discovered HTTPS URL without assuming
// a CDN naming convention. DNS addresses are checked and pinned by dialPublic
// when the backend connects; this function itself performs no network I/O.
func ValidateMediaURL(raw string) (*url.URL, error) {
	u, err := validateHTTPSURL(raw)
	if err != nil {
		return nil, err
	}
	host := strings.ToLower(u.Hostname())
	labels := strings.Split(host, ".")
	if len(host) > 253 || len(labels) < 2 {
		return nil, fmt.Errorf("media host must be a DNS name")
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return nil, fmt.Errorf("invalid media DNS name")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return nil, fmt.Errorf("invalid media DNS name")
			}
		}
	}
	// Reject alternative numeric IP spellings and local/special-use DNS names.
	tld := labels[len(labels)-1]
	if !strings.ContainsAny(tld, "abcdefghijklmnopqrstuvwxyz") {
		return nil, fmt.Errorf("numeric media host rejected")
	}
	for _, suffix := range []string{"local", "internal", "localhost", "home.arpa"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return nil, fmt.Errorf("local media host rejected")
		}
	}
	return u, nil
}

func validateHTTPSURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid player URL syntax")
	}
	if u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return nil, fmt.Errorf("player URL must use HTTPS without credentials, fragments, or nonstandard ports")
	}
	host := strings.ToLower(u.Hostname())
	if _, err := netip.ParseAddr(host); err == nil || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return nil, fmt.Errorf("literal or local player host rejected")
	}
	return u, nil
}

func (c *HTTPClient) validate(raw string) error {
	if c.hosts == nil {
		_, err := ValidateMediaURL(raw)
		return err
	}
	hosts := make([]string, 0, len(c.hosts))
	for host := range c.hosts {
		hosts = append(hosts, host)
	}
	_, err := ValidateURL(raw, hosts)
	return err
}

func (c *HTTPClient) Fetch(ctx context.Context, raw string, limit int64) ([]byte, error) {
	return c.request(ctx, http.MethodGet, raw, nil, limit)
}

// PostForm performs the provider player's ordinary source lookup. Signed
// parameters come from its current embed and are never cached or logged.
func (c *HTTPClient) PostForm(ctx context.Context, raw string, values url.Values, limit int64) ([]byte, error) {
	body := values.Encode()
	if len(body) > 64<<10 {
		return nil, fmt.Errorf("player form exceeds size limit")
	}
	return c.request(ctx, http.MethodPost, raw, strings.NewReader(body), limit)
}

// Probe validates direct MP4 metadata with HEAD, without downloading video.
func (c *HTTPClient) Probe(ctx context.Context, raw string) error {
	_, err := c.request(ctx, http.MethodHead, raw, nil, 1)
	return err
}

// FetchResource preserves the final URL so manifest references follow redirects correctly.
func (c *HTTPClient) FetchResource(ctx context.Context, raw string, limit int64) ([]byte, string, error) {
	return c.requestResource(ctx, http.MethodGet, raw, nil, limit)
}

func (c *HTTPClient) request(ctx context.Context, method, raw string, body io.Reader, limit int64) ([]byte, error) {
	data, _, err := c.requestResource(ctx, method, raw, body, limit)
	return data, err
}

func (c *HTTPClient) requestResource(ctx context.Context, method, raw string, body io.Reader, limit int64) ([]byte, string, error) {
	if err := c.validate(raw); err != nil {
		return nil, "", fmt.Errorf("%w: unsafe player URL: %w", provider.ErrHTTP, err)
	}
	if limit <= 0 || limit > MaxResponseBytes {
		return nil, "", fmt.Errorf("invalid player response limit")
	}
	req, err := http.NewRequestWithContext(ctx, method, raw, body)
	if err != nil {
		return nil, "", fmt.Errorf("%w: create player request", provider.ErrHTTP)
	}
	req.Header.Set("User-Agent", "animego-tv-backend/0.1")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	c.setHeaders(req)
	resp, err := c.client.Do(req)
	if err != nil {
		// net/http includes the full request URL in url.Error; strip that layer
		// before wrapping so signed URLs cannot appear in server logs.
		if urlErr, ok := err.(*url.Error); ok {
			err = urlErr.Err
		}
		return nil, "", &provider.HTTPError{Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", &provider.HTTPError{StatusCode: resp.StatusCode, Err: fmt.Errorf("unexpected player response status")}
	}
	if method == http.MethodHead {
		kind, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if err != nil || kind != "video/mp4" {
			return nil, "", fmt.Errorf("%w: unexpected direct media content type", provider.ErrParsing)
		}
		return nil, "", nil
	}
	if resp.ContentLength > limit {
		return nil, "", &provider.HTTPError{StatusCode: resp.StatusCode, Err: fmt.Errorf("player response exceeds size limit")}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", &provider.HTTPError{StatusCode: resp.StatusCode, Err: fmt.Errorf("read player response: %w", err)}
	}
	if int64(len(data)) > limit {
		return nil, "", &provider.HTTPError{StatusCode: resp.StatusCode, Err: fmt.Errorf("player response exceeds size limit")}
	}
	return data, resp.Request.URL.String(), nil
}

func (c *HTTPClient) setHeaders(req *http.Request) {
	// net/http otherwise synthesizes/forwards Referer during redirects.
	req.Header.Del("Referer")
	if referer := c.referers[strings.ToLower(req.URL.Hostname())]; referer != "" {
		req.Header.Set("Referer", referer)
	}
}

func dialPublic(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid player dial address")
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve player host: %w", err)
	}
	if err := validateAddresses(addresses); err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	var lastError error
	for _, ip := range addresses {
		connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
		if err == nil {
			return connection, nil
		}
		lastError = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, fmt.Errorf("connect to validated player host: %w", lastError)
}

func validateAddresses(addresses []netip.Addr) error {
	if len(addresses) == 0 {
		return fmt.Errorf("player host has no addresses")
	}
	for _, ip := range addresses {
		ip = ip.Unmap()
		if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("nonpublic player address rejected")
		}
		// Special-use, metadata, shared, documentation, and transition ranges.
		for _, raw := range []string{"0.0.0.0/8", "100.64.0.0/10", "168.63.129.16/32", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/32", "2001:db8::/32", "2002::/16"} {
			if netip.MustParsePrefix(raw).Contains(ip) {
				return fmt.Errorf("special-use player address rejected")
			}
		}
	}
	return nil
}
