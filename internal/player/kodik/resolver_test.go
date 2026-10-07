package kodik

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"animego/backend/internal/domain"
	"animego/backend/internal/player"
	"animego/backend/internal/provider"
)

const testEmbed = `<script>var urlParams = '{"d":"animego.me","d_sign":"fixture-d-sign","pd":"kodikplayer.com","pd_sign":"fixture-pd-sign","ref":"https%3A%2F%2Fanimego.me%2F","ref_sign":"fixture-ref-sign"}';</script><script src="/assets/js/app.player_single.abc123.js"></script>`
const testSelectionURL = "https://kodikplayer.com/seria/123/0123456789abcdef0123456789abcdef/720p?translations=false"
const testSources = `{"links":{"360":[{"type":"application/x-mpegURL","src":"//media.future-cdn.example/sample/360.m3u8"}],"720":[{"type":"application/x-mpegURL","src":"Tg9bHEZxGA5ulFZ1kuCbG2ZcTuD4GE1ejOCdk2NbkOftThkgUK5bU3C4X3Zdi2DcXEHxmPZ1kuC="}]}}`
const testPlaylist = "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6,\n./720.mp4:hls:seg-1.ts\n#EXT-X-ENDLIST\n"

type fixtureAPI struct {
	t                 *testing.T
	sources, manifest string
	postErr           error
	gets, posts       int
}

func (f *fixtureAPI) Fetch(ctx context.Context, raw string, limit int64) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if limit <= 0 || limit > player.MaxResponseBytes {
		f.t.Fatal("unbounded fetch")
	}
	u, err := player.ValidateMediaURL(raw)
	if err != nil {
		return nil, err
	}
	f.gets++
	switch u.Path {
	case "/seria/123/0123456789abcdef0123456789abcdef/720p":
		return []byte(testEmbed), nil
	case "/assets/js/app.player_single.abc123.js":
		return []byte(`$.ajax({type:"POST",url:atob("L2ZyZXNoLWVuZHBvaW50"),data:e})`), nil
	case "/sample/720.m3u8", "/sample/360.m3u8":
		return []byte(f.manifest), nil
	default:
		f.t.Fatalf("unexpected or video segment request: %s", u.Path)
		return nil, errors.New("fixture")
	}
}
func (f *fixtureAPI) PostForm(ctx context.Context, raw string, values url.Values, limit int64) ([]byte, error) {
	f.posts++
	if raw != "https://kodikplayer.com/fresh-endpoint" || values.Get("d_sign") != "fixture-d-sign" || values.Get("pd_sign") != "fixture-pd-sign" || values.Get("ref_sign") != "fixture-ref-sign" || values.Get("ref") != "https://animego.me/" || values.Get("id") != "123" || values.Get("type") != "seria" || values.Get("hash") != "0123456789abcdef0123456789abcdef" {
		f.t.Fatal("source request did not preserve issued parameters and selected video identity")
	}
	if f.postErr != nil {
		return nil, f.postErr
	}
	return []byte(f.sources), nil
}
func testSelection() domain.PlayerSelection {
	return domain.PlayerSelection{Player: domain.PlayerReference{Provider: "kodik"}, EmbedURL: testSelectionURL}
}

func TestResolveSourceProtocol(t *testing.T) {
	for _, tt := range []struct {
		name, sources, manifest string
		err                     error
		postErr                 error
	}{
		{"encoded and plain rotating CDN URLs", testSources, testPlaylist, nil, nil},
		{"malformed JSON", "broken", testPlaylist, provider.ErrParsing, nil},
		{"empty sources", `{"links":{}}`, testPlaylist, domain.ErrPlaybackUnavailable, nil},
		{"malformed encoding", `{"links":{"720":[{"type":"application/x-mpegURL","src":"%%%"}]}}`, testPlaylist, provider.ErrParsing, nil},
		{"local source", `{"links":{"720":[{"type":"application/x-mpegURL","src":"https://127.0.0.1/master.m3u8?secret=value"}]}}`, testPlaylist, provider.ErrParsing, nil},
		{"protected manifest", testSources, "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key\"\n", domain.ErrPlaybackUnavailable, nil},
		{"denied source lookup", testSources, testPlaylist, domain.ErrPlaybackUnavailable, &provider.HTTPError{StatusCode: 403, Err: errors.New("denied")}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &fixtureAPI{t: t, sources: tt.sources, manifest: tt.manifest, postErr: tt.postErr}
			got, err := NewResolver(f, f).Resolve(context.Background(), testSelection())
			if tt.err != nil {
				if !errors.Is(err, tt.err) {
					t.Fatalf("got %v, want %v", err, tt.err)
				}
				if strings.Contains(err.Error(), "secret=value") {
					t.Fatal("source URL leaked")
				}
				return
			}
			if err != nil || len(got) != 2 || got[0].Quality != "720" || got[1].Quality != "360" || got[0].URL != "https://media.future-cdn.example/sample/720.m3u8?token=fixture" || f.gets != 4 || f.posts != 1 {
				t.Fatalf("bad sources or lookup flow: %#v, %v, gets=%d posts=%d", got, err, f.gets, f.posts)
			}
		})
	}
}

func TestRejectEmbedBeforeIO(t *testing.T) {
	for _, raw := range []string{"https://kodikplayer.com.evil.example/seria/123/hash/720p", "http://kodikplayer.com/seria/123/hash/720p", "https://kodikplayer.com/serial/123/hash/720p", "https://kodikplayer.com/seria/123/../../720p"} {
		s := testSelection()
		s.EmbedURL = raw
		_, err := NewResolver(nil, nil).Resolve(context.Background(), s)
		if !errors.Is(err, domain.ErrUnsupportedPlayer) {
			t.Fatalf("unexpected %v", err)
		}
	}
	u, _ := url.Parse(testSelectionURL)
	for _, script := range []string{`url:atob("aHR0cHM6Ly9hdHRhY2tlci5leGFtcGxlLw==")`, `url:atob("L2Z0b3I="),url:atob("L29lcg==")`, `no endpoint`} {
		if _, err := parseEndpoint([]byte(script), u); !errors.Is(err, provider.ErrParsing) {
			t.Fatal("unsafe/ambiguous endpoint accepted")
		}
	}
}

func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fixtureAPI{t: t}
	_, err := NewResolver(f, f).Resolve(ctx, testSelection())
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
