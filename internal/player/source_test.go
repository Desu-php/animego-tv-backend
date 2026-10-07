package player

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"animego/backend/internal/domain"
	"animego/backend/internal/provider"
)

const clearPlaylist = "#EXTM3U\n#EXT-X-KEY:METHOD=NONE\n#EXTINF:6,\nsegment.ts\n#EXT-X-ENDLIST\n"

type manifestFixture struct {
	bodies   map[string]string
	final    map[string]string
	requests []string
}

func (f *manifestFixture) Fetch(ctx context.Context, raw string, limit int64) ([]byte, error) {
	data, _, err := f.FetchResource(ctx, raw, limit)
	return data, err
}
func (f *manifestFixture) FetchResource(ctx context.Context, raw string, limit int64) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	f.requests = append(f.requests, raw)
	body, ok := f.bodies[raw]
	if !ok {
		return nil, "", &provider.HTTPError{StatusCode: 404, Err: errors.New("missing fixture")}
	}
	final := raw
	if f.final[raw] != "" {
		final = f.final[raw]
	}
	return []byte(body), final, nil
}

func TestManifestUsesRedirectTargetAndNeverFetchesSegments(t *testing.T) {
	original := "https://cdn.example/start/master.m3u8"
	target := "https://new-cdn.example/final/master.m3u8"
	child := "https://new-cdn.example/final/video.m3u8"
	f := &manifestFixture{bodies: map[string]string{original: "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nvideo.m3u8\n", child: clearPlaylist}, final: map[string]string{original: target}}
	err := CheckManifest(context.Background(), f, domain.VideoSource{Type: domain.VideoHLS, URL: original}, true)
	if err != nil || len(f.requests) != 2 || f.requests[1] != child {
		t.Fatalf("wrong redirect base or segment download: %v, %v", err, f.requests)
	}
}

func TestChildManifestProtectionAndTraversalBounds(t *testing.T) {
	root, child := "https://cdn.example/root.m3u8", "https://cdn.example/child.m3u8"
	for _, tt := range []struct {
		name, body string
		want       error
	}{
		{"encrypted child", "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key\"\n", domain.ErrPlaybackUnavailable},
		{"cycle", "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nroot.m3u8\n", provider.ErrParsing},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &manifestFixture{bodies: map[string]string{root: "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nchild.m3u8\n", child: tt.body}}
			if err := CheckManifest(context.Background(), f, domain.VideoSource{Type: domain.VideoHLS, URL: root}, true); !errors.Is(err, tt.want) {
				t.Fatalf("got %v", err)
			}
		})
	}
	bodies := map[string]string{}
	var master strings.Builder
	master.WriteString("#EXTM3U\n")
	for i := 0; i < MaxManifestRequests; i++ {
		fmt.Fprintf(&master, "#EXT-X-STREAM-INF:BANDWIDTH=1\n%d.m3u8\n", i)
		bodies[fmt.Sprintf("https://cdn.example/%d.m3u8", i)] = clearPlaylist
	}
	bodies[root] = master.String()
	f := &manifestFixture{bodies: bodies}
	if err := CheckManifest(context.Background(), f, domain.VideoSource{Type: domain.VideoHLS, URL: root}, true); !errors.Is(err, provider.ErrParsing) || len(f.requests) != MaxManifestRequests {
		t.Fatalf("unbounded traversal: %v, %d", err, len(f.requests))
	}
}

func TestAvailableSourcesPreservesWorkingQualitiesAndCancellation(t *testing.T) {
	sources := []domain.VideoSource{{Quality: "1080"}, {Quality: "720"}, {Quality: "360"}}
	missing := ResolutionError("check", &provider.HTTPError{StatusCode: 404, Err: errors.New("gone")})
	check := func(_ context.Context, s domain.VideoSource) error {
		if s.Quality == "1080" {
			return missing
		}
		return nil
	}
	got, err := AvailableSources(context.Background(), sources, check)
	if err != nil || len(got) != 2 || got[0].Quality != "720" || got[1].Quality != "360" {
		t.Fatalf("lost working qualities: %v, %v", got, err)
	}
	if _, err = AvailableSources(context.Background(), sources[:1], check); !errors.Is(err, domain.ErrPlaybackUnavailable) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	got, err = AvailableSources(ctx, sources, func(context.Context, domain.VideoSource) error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatal("cancelled operation returned partial playback")
	}
}
