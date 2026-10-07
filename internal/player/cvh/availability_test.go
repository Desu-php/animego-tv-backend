package cvh

import (
	"context"
	"errors"
	"strings"
	"testing"

	"animego/backend/internal/domain"
	"animego/backend/internal/provider"
)

type partialMedia struct{ *fixtureFetcher }

func (f partialMedia) Probe(ctx context.Context, raw string) error {
	if strings.Contains(raw, "quality=1080") {
		return &provider.HTTPError{StatusCode: 404, Err: errors.New("missing quality")}
	}
	return f.fixtureFetcher.Probe(ctx, raw)
}
func TestMissingMP4QualityKeepsWorkingQuality(t *testing.T) {
	f := &fixtureFetcher{t: t, playlist: testPlaylist, sources: `{"sources":{"mpegFullHdUrl":"https://cdn.example/file?quality=1080","mpegLowUrl":"https://cdn.example/file?quality=360"}}`}
	r, err := NewResolver("https://animego.me", f, f, partialMedia{f})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Resolve(context.Background(), domain.PlayerSelection{Player: domain.PlayerReference{Provider: "cvh"}, EmbedURL: "https://animego.me/cdn-iframe/123/Dream%20Cast/11/747"})
	if err != nil || len(got) != 1 || got[0].Quality != "360" {
		t.Fatalf("working MP4 quality lost: %v, %v", got, err)
	}
}
