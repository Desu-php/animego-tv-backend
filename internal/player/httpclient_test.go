package player

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"animego/backend/internal/provider"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type unreadBody struct {
	t      *testing.T
	closed bool
}

func (b *unreadBody) Read([]byte) (int, error) {
	b.t.Fatal("HEAD downloaded video body")
	return 0, io.EOF
}
func (b *unreadBody) Close() error { b.closed = true; return nil }

func TestSourceFormAndMP4Metadata(t *testing.T) {
	c, err := NewHTTPClient(time.Second, []string{"kodikplayer.com", "other.example"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	calls := 0
	c.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Fatal("bad form request")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		values, err := url.ParseQuery(string(body))
		if err != nil || values.Get("d_sign") != "opaque+signature" || values.Get("ref") != "https://animego.me/?a=b&c=d" {
			t.Fatal("issued parameters changed")
		}
		h := http.Header{}
		status := 200
		if r.URL.Path == "/redirect" {
			h.Set("Location", "https://other.example/lookup")
			status = 307
		}
		return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(`{"links":{}}`)), Request: r}, nil
	})
	values := url.Values{"d_sign": {"opaque+signature"}, "ref": {"https://animego.me/?a=b&c=d"}}
	if _, err := c.PostForm(context.Background(), "https://kodikplayer.com/lookup", values, 128); err != nil {
		t.Fatal(err)
	}
	_, err = c.PostForm(context.Background(), "https://kodikplayer.com/redirect?token=opaque", values, 128)
	if !errors.Is(err, provider.ErrUpstream) || calls != 2 || strings.Contains(err.Error(), "token=") {
		t.Fatalf("form redirect or URL redaction failed: %v, calls=%d", err, calls)
	}
	media, err := NewMediaHTTPClient(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer media.CloseIdleConnections()
	body := &unreadBody{t: t}
	media.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodHead {
			t.Fatal("MP4 metadata downloaded with GET")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"video/mp4"}}, ContentLength: 500 << 20, Body: body, Request: r}, nil
	})
	if err := media.Probe(context.Background(), "https://media.new-cdn.example/file"); err != nil || !body.closed {
		t.Fatalf("MP4 metadata failed: %v", err)
	}
	media.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader("challenge")), Request: r}, nil
	})
	if err := media.Probe(context.Background(), "https://media.new-cdn.example/file"); !errors.Is(err, provider.ErrParsing) {
		t.Fatal("HTML accepted as direct MP4")
	}
}

func TestURLPolicy(t *testing.T) {
	allowed := []string{"aniboom.one"}
	for _, raw := range []string{"https://aniboom.one/embed/id", "https://ANIBOOM.ONE:443/embed/id?token=opaque"} {
		if _, err := ValidateURL(raw, allowed); err != nil {
			t.Errorf("rejected legitimate URL: %v", err)
		}
	}
	for _, raw := range []string{"http://aniboom.one/", "ftp://aniboom.one/", "//aniboom.one/", "https://user:pass@aniboom.one/", "https://aniboom.one/#fragment", "https://aniboom.one:8443/", "https://aniboom.one.evil.example/", "https://sub.aniboom.one/", "https://aniboom.one./", "https://127.0.0.1/", "https://[::1]/", "https://[::ffff:127.0.0.1]/", "https://169.254.169.254/", "https://localhost/", "https://a.localhost/", "https://%zz/"} {
		if _, err := ValidateURL(raw, allowed); err == nil {
			t.Errorf("accepted unsafe URL %q", raw)
		}
	}
}

func TestDiscoveredMediaURLPolicy(t *testing.T) {
	for _, raw := range []string{
		"https://new-node.ya-low1.site/master.m3u8",
		"https://media.completely-new-cdn.example:443/master.m3u8?token=opaque",
		"https://MEDIA.NEW-CDN.EXAMPLE/manifest.mpd",
		"https://xn--example-9d0b.example/master.m3u8",
	} {
		if _, err := ValidateMediaURL(raw); err != nil {
			t.Errorf("rejected discovered media URL: %v", err)
		}
	}
	for _, raw := range []string{
		"http://cdn.example/master.m3u8", "//cdn.example/master.m3u8",
		"https://user:secret@cdn.example/master.m3u8", "https://cdn.example/#fragment",
		"https://cdn.example:8443/", "https://127.0.0.1/", "https://[::ffff:127.0.0.1]/",
		"https://169.254.169.254/", "https://localhost/", "https://cdn.localhost/",
		"https://cdn.local/", "https://cdn.internal/", "https://cdn.home.arpa/",
		"https://intranet/", "https://2130706433/", "https://0177.0.0.1/",
		"https://0x7f.0.0.1/", "https://cdn.example./", "https://cdn..example/",
		"https://-cdn.example/", "https://cdn-.example/", "https://cdn_name.example/",
		"https://%zz/",
	} {
		if _, err := ValidateMediaURL(raw); err == nil {
			t.Errorf("accepted unsafe media URL %q", raw)
		} else if strings.Contains(err.Error(), "secret") {
			t.Errorf("media URL leaked in error: %v", err)
		}
	}
}

func TestMediaHTTPClientDiscoversHostsAndValidatesRedirects(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Referer") != "" || r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" {
			t.Error("credentials or embed headers reached media host")
		}
		switch r.URL.Path {
		case "/manifest":
			w.Header().Set("Location", "https://another.new-cdn.example/final?token=opaque")
		case "/private":
			w.Header().Set("Location", "https://127.0.0.1/final?token=opaque")
		case "/local":
			w.Header().Set("Location", "https://cdn.internal/final?token=opaque")
		case "/http":
			w.Header().Set("Location", "http://another.new-cdn.example/final?token=opaque")
		case "/final":
			w.Write([]byte("manifest"))
			return
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	media, err := NewMediaHTTPClient(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer media.CloseIdleConnections()
	target, _ := url.Parse(server.URL)
	media.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || (r.URL.Hostname() != "media.new-cdn.example" && r.URL.Hostname() != "another.new-cdn.example") {
			t.Fatalf("unsafe redirect reached transport: %s", r.URL.Hostname())
		}
		local := r.Clone(r.Context())
		u := *r.URL
		u.Scheme, u.Host = target.Scheme, target.Host
		local.URL = &u
		return server.Client().Transport.RoundTrip(local)
	})
	b, err := media.Fetch(context.Background(), "https://media.new-cdn.example/manifest", 32)
	if err != nil || string(b) != "manifest" || requests != 2 {
		t.Fatalf("new CDN/redirect failed: %q, %v, requests=%d", b, err, requests)
	}
	for _, p := range []string{"/private", "/local", "/http"} {
		before := requests
		_, err := media.Fetch(context.Background(), "https://media.new-cdn.example"+p+"?token=opaque", 32)
		if !errors.Is(err, provider.ErrUpstream) || requests != before+1 {
			t.Fatalf("unsafe redirect was not blocked: %v, requests=%d", err, requests-before)
		}
		if strings.Contains(err.Error(), "opaque") {
			t.Fatalf("redirect URL leaked: %v", err)
		}
	}
	embed, err := NewHTTPClient(time.Second, []string{"aniboom.one"})
	if err != nil {
		t.Fatal(err)
	}
	defer embed.CloseIdleConnections()
	if err := embed.validate("https://media.new-cdn.example/manifest"); err == nil {
		t.Fatal("media discovery loosened the embed host policy")
	}
	if _, err := NewMediaHTTPClient(0); err == nil {
		t.Fatal("unbounded media timeout accepted")
	}
}

func TestDNSAddressPolicy(t *testing.T) {
	public := netip.MustParseAddr("8.8.8.8")
	if err := validateAddresses([]netip.Addr{public, netip.MustParseAddr("2606:4700:4700::1111")}); err != nil {
		t.Fatal(err)
	}
	if err := validateAddresses(nil); err == nil {
		t.Fatal("accepted empty DNS")
	}
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.100.100.200", "168.63.129.16", "0.0.0.0", "192.0.2.1", "198.18.0.1", "224.0.0.1", "::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "2001:db8::1", "64:ff9b::a9fe:a9fe", "2002:7f00:1::"} {
		if err := validateAddresses([]netip.Addr{public, netip.MustParseAddr(raw)}); err == nil {
			t.Errorf("accepted mixed public/private DNS containing %s", raw)
		}
	}
}

func TestRefererIsScopedAndResetOnRedirect(t *testing.T) {
	var requests []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		if r.URL.Path == "/embed" {
			if r.Header.Get("Referer") != "https://animego.me/" {
				t.Error("verified embed Referer missing")
			}
			w.Header().Set("Location", "https://cdn.example/manifest")
			w.WriteHeader(302)
			return
		}
		if r.Header.Get("Referer") != "" || r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" {
			t.Error("embed headers/credentials forwarded to media host")
		}
		w.Write([]byte("manifest"))
	}))
	defer server.Close()
	c, err := NewHTTPClient(time.Second, []string{"aniboom.one", "cdn.example"}, WithReferer("aniboom.one", "https://animego.me"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	target, _ := url.Parse(server.URL)
	c.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		local := r.Clone(r.Context())
		u := *r.URL
		u.Scheme, u.Host = target.Scheme, target.Host
		local.URL = &u
		return server.Client().Transport.RoundTrip(local)
	})
	b, err := c.Fetch(context.Background(), "https://aniboom.one/embed", 32)
	if err != nil || string(b) != "manifest" || len(requests) != 2 {
		t.Fatalf("bad redirect flow: %q, %v, %#v", b, err, requests)
	}
	for _, origin := range []string{"https://user:pass@animego.me/", "https://animego.me/?secret=value", "https://animego.me/#fragment", "https://animego.me/anime/id", "https://animego.me/\r\nCookie: secret"} {
		if _, err := NewHTTPClient(time.Second, []string{"aniboom.one"}, WithReferer("aniboom.one", origin)); err == nil {
			t.Errorf("accepted unsafe Referer %q", origin)
		}
	}
	if _, err := NewHTTPClient(time.Second, []string{"aniboom.one"}, WithReferer("attacker.example", "https://animego.me/")); err == nil {
		t.Error("untrusted header target accepted")
	}
}

func TestHTTPClient(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" || r.Header.Get("Cookie") != "" {
			t.Error("unexpected headers")
		}
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte("fixture"))
		case "/large":
			w.(http.Flusher).Flush()
			w.Write([]byte(strings.Repeat("x", 32)))
		case "/status":
			w.WriteHeader(503)
		case "/redirect":
			w.Header().Set("Location", "https://aniboom.one/ok")
			w.WriteHeader(302)
		case "/unsafe":
			w.Header().Set("Location", "https://127.0.0.1/?secret=opaque")
			w.WriteHeader(302)
		case "/loop":
			w.Header().Set("Location", "/loop")
			w.WriteHeader(302)
		case "/wait":
			<-r.Context().Done()
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client, err := NewHTTPClient(100*time.Millisecond, []string{"aniboom.one"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	target, _ := url.Parse(server.URL)
	client.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() != "aniboom.one" {
			t.Fatalf("unsafe host reached transport: %s", r.URL.Hostname())
		}
		local := r.Clone(r.Context())
		u := *r.URL
		u.Scheme, u.Host = target.Scheme, target.Host
		local.URL = &u
		return server.Client().Transport.RoundTrip(local)
	})
	for _, path := range []string{"/ok", "/redirect"} {
		b, e := client.Fetch(context.Background(), "https://aniboom.one"+path, 16)
		if e != nil || string(b) != "fixture" {
			t.Fatalf("%s: %q, %v", path, b, e)
		}
	}
	for _, path := range []string{"/large", "/status", "/unsafe", "/loop", "/wait"} {
		_, e := client.Fetch(context.Background(), "https://aniboom.one"+path+"?secret=opaque", 16)
		if !errors.Is(e, provider.ErrUpstream) {
			t.Errorf("%s: got %v", path, e)
		}
		if e != nil && strings.Contains(e.Error(), "secret=opaque") {
			t.Errorf("signed URL leaked: %v", e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := client.Fetch(ctx, "https://aniboom.one/ok", 16); !errors.Is(e, context.Canceled) {
		t.Errorf("cancellation lost: %v", e)
	}
	for _, limit := range []int64{0, -1, MaxResponseBytes + 1} {
		if _, e := client.Fetch(context.Background(), "https://aniboom.one/ok", limit); e == nil {
			t.Error("accepted unbounded limit")
		}
	}
}
