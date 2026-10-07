package animego

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"animego/backend/internal/provider"
)

func TestSearchRequestAndResponse(t *testing.T) {
	query := "Поднятие уровня & solo?"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/search/anime" || r.URL.Query().Get("q") != query {
			t.Errorf("unexpected search request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("User-Agent") != userAgent || r.Header.Get("Accept") != "text/html" {
			t.Error("expected explicit User-Agent and HTML Accept header")
		}
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		_, _ = io.WriteString(w, fixture(t, "search.html"))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, &http.Client{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.Search(context.Background(), query)
	if err != nil || len(got) != 1 || got[0].Year != 2024 {
		t.Fatalf("unexpected result: %#v, %v", got, err)
	}
}

func TestSearchHTTPFailures(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		contentType  string
		body         string
		declaredSize bool
		want         error
	}{
		{name: "unavailable", status: 503, contentType: "text/html", want: provider.ErrHTTP},
		{name: "JSON instead of HTML", status: 200, contentType: "application/json", body: `{}`, want: provider.ErrParsing},
		{name: "challenge", status: 200, contentType: "text/html", body: `<h1>Challenge</h1>`, want: provider.ErrParsing},
		{name: "empty body", status: 200, contentType: "text/html", want: provider.ErrParsing},
		{name: "oversize streamed body", status: 200, contentType: "text/html", body: strings.Repeat("x", maxResponseBytes+1), want: provider.ErrHTTP},
		{name: "oversize content length", status: 200, contentType: "text/html", declaredSize: true, want: provider.ErrHTTP},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				if tc.declaredSize {
					w.Header().Set("Content-Length", fmt.Sprint(maxResponseBytes+1))
				}
				w.WriteHeader(tc.status)
				if !tc.declaredSize {
					w.(http.Flusher).Flush()
				}
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client, err := NewClient(server.URL, &http.Client{Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Search(context.Background(), "test")
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
			if errors.Is(tc.want, provider.ErrHTTP) {
				var httpErr *provider.HTTPError
				if !errors.As(err, &httpErr) || httpErr.StatusCode != tc.status {
					t.Fatalf("missing upstream status: %v", err)
				}
			}
		})
	}
}

func TestSearchCancellationAndTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	client, err := NewClient(server.URL, &http.Client{Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Search(ctx, "test")
	if !errors.Is(err, context.Canceled) || !errors.Is(err, provider.ErrHTTP) {
		t.Fatalf("cancellation not preserved: %v", err)
	}
	_, err = client.Search(context.Background(), "test")
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, provider.ErrHTTP) {
		t.Fatalf("timeout not preserved: %v", err)
	}
}

func TestNewClientValidation(t *testing.T) {
	for _, raw := range []string{"", "animego.me", "ftp://animego.me", "https://user:pass@animego.me", "https://animego.me/path", "https://animego.me?q=1", "https://animego.me/#x"} {
		if _, err := NewClient(raw, &http.Client{Timeout: time.Second}); err == nil {
			t.Errorf("accepted invalid origin %q", raw)
		}
	}
	for _, httpClient := range []*http.Client{nil, {}, {Timeout: -time.Second}} {
		if _, err := NewClient("https://animego.me", httpClient); err == nil {
			t.Error("accepted client without finite timeout")
		}
	}
}
