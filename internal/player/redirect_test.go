package player

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestFetchResourceReturnsFinalURL(t *testing.T) {
	client, err := NewMediaHTTPClient(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	client.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/start" {
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://other-cdn.example/final/master.m3u8"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("manifest")), Request: r}, nil
	})
	data, final, err := client.FetchResource(context.Background(), "https://cdn.example/start", 32)
	if err != nil || string(data) != "manifest" || final != "https://other-cdn.example/final/master.m3u8" {
		t.Fatalf("lost final URL: %q, %v", final, err)
	}
}
