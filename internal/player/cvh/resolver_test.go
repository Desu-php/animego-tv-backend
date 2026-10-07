package cvh

import (
	"context"
	"errors"
	"strings"
	"testing"

	"animego/backend/internal/domain"
	"animego/backend/internal/player"
	"animego/backend/internal/provider"
)

const testWrapper = `<video-player id="pcvh" data-title-id="123" data-publisher-id="747" data-aggregator="mali" episode="11" season="1" priority-voice="Dream Cast"></video-player>`
const testPlaylist = `{"isSerial":true,"items":[{"vkId":"1001","episode":10,"season":1,"voiceStudio":"Dream Cast"},{"vkId":"1002","episode":11,"season":1,"voiceStudio":"Another voice"},{"vkId":"1003","episode":11,"season":1,"voiceStudio":"Dream Cast"},{"vkId":"1004","episode":11,"season":2,"voiceStudio":"Dream Cast"}]}`
const testHLS = `{"sources":{"hlsUrl":"https://media.new-cvh-cdn.example/sample/master.m3u8?token=fixture"}}`

type fixtureFetcher struct {
	t                           *testing.T
	playlist, sources, manifest string
	gets, probes, embeds        int
}

func (f *fixtureFetcher) FetchEmbed(ctx context.Context, raw string) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	f.embeds++
	return []byte(testWrapper), nil
}
func (f *fixtureFetcher) Fetch(ctx context.Context, raw string, limit int64) ([]byte, error) {
	u, err := player.ValidateMediaURL(raw)
	if err != nil {
		return nil, err
	}
	f.gets++
	switch u.Path {
	case "/api/v1/player/sv/playlist":
		q := u.Query()
		if q.Get("id") != "123" || q.Get("pub") != "747" || q.Get("aggr") != "mali" || q.Get("ep") != "11" || q.Get("sn") != "1" {
			f.t.Fatal("wrong CVH playlist parameters")
		}
		return []byte(f.playlist), nil
	case "/api/v1/player/sv/video/1003":
		return []byte(f.sources), nil
	case "/sample/index.m3u8":
		return []byte("#EXTM3U\n#EXTINF:6,\nsegment.ts\n#EXT-X-ENDLIST\n"), nil
	case "/sample/master.m3u8", "/sample/manifest.mpd":
		return []byte(f.manifest), nil
	default:
		f.t.Fatalf("wrong episode/voice or unexpected video request: %s", u.Path)
		return nil, errors.New("fixture")
	}
}
func (f *fixtureFetcher) Probe(ctx context.Context, raw string) error {
	if _, err := player.ValidateMediaURL(raw); err != nil {
		return err
	}
	f.probes++
	return nil
}

func TestResolve(t *testing.T) {
	for _, tt := range []struct {
		name, playlist, sources, manifest string
		kind                              domain.VideoType
		count, probes                     int
		err                               error
	}{
		{"correct episode season and voice", testPlaylist, testHLS, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nindex.m3u8\n", domain.VideoHLS, 1, 0, nil},
		{"DASH without HLS", testPlaylist, `{"sources":{"dashUrl":"https://media.new-cvh-cdn.example/sample/manifest.mpd"}}`, `<MPD><Representation><BaseURL>?quality=720</BaseURL></Representation></MPD>`, domain.VideoDASH, 1, 0, nil},
		{"MP4-only voice", testPlaylist, `{"sources":{"mpegFullHdUrl":"https://media.new-cvh-cdn.example/file?quality=1080","mpegLowUrl":"https://media.new-cvh-cdn.example/file?quality=360"}}`, "", domain.VideoMP4, 2, 2, nil},
		{"no wrong-voice fallback", strings.ReplaceAll(testPlaylist, "Dream Cast", "Other studio"), testHLS, "", "", 0, 0, domain.ErrPlaybackUnavailable},
		{"blocked title", `{"tags":[5],"items":[]}`, testHLS, "", "", 0, 0, domain.ErrPlaybackUnavailable},
		{"missing items", `{}`, testHLS, "", "", 0, 0, provider.ErrParsing},
		{"ambiguous selection", strings.ReplaceAll(testPlaylist, `"season":2`, `"season":1`), testHLS, "", "", 0, 0, provider.ErrParsing},
		{"missing sources", testPlaylist, `{"sources":{}}`, "", "", 0, 0, domain.ErrPlaybackUnavailable},
		{"local CDN", testPlaylist, `{"sources":{"hlsUrl":"https://127.0.0.1/master.m3u8?secret=value"}}`, "", "", 0, 0, provider.ErrParsing},
		{"protected HLS", testPlaylist, testHLS, "#EXTM3U\n#EXT-X-SESSION-KEY:METHOD=SAMPLE-AES,URI=\"key\"\n", "", 0, 0, domain.ErrPlaybackUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &fixtureFetcher{t: t, playlist: tt.playlist, sources: tt.sources, manifest: tt.manifest}
			r, err := NewResolver("https://animego.me", f, f, f)
			if err != nil {
				t.Fatal(err)
			}
			got, err := r.Resolve(context.Background(), domain.PlayerSelection{Player: domain.PlayerReference{Provider: "cvh"}, EmbedURL: "https://animego.me/cdn-iframe/123/Dream%20Cast/11/747"})
			if tt.err != nil {
				if !errors.Is(err, tt.err) {
					t.Fatalf("got %v, want %v", err, tt.err)
				}
				if strings.Contains(err.Error(), "secret=value") {
					t.Fatal("source URL leaked")
				}
				return
			}
			if err != nil || len(got) != tt.count || got[0].Type != tt.kind || f.probes != tt.probes || f.embeds != 1 {
				t.Fatalf("bad CVH sources: %#v, %v", got, err)
			}
			if tt.kind == domain.VideoMP4 && (got[0].Quality != "1080" || got[1].Quality != "360") {
				t.Fatal("MP4 qualities not ordered")
			}
		})
	}
}

func TestRejectEmbedBeforeIO(t *testing.T) {
	r, err := NewResolver("https://animego.me", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"https://animego.me.evil.example/cdn-iframe/id", "https://animego.me/anime/id", "http://animego.me/cdn-iframe/id", "https://user:pass@animego.me/cdn-iframe/id", "https://animego.me/cdn-iframe/../admin"} {
		_, err := r.Resolve(context.Background(), domain.PlayerSelection{Player: domain.PlayerReference{Provider: "cvh"}, EmbedURL: raw})
		if !errors.Is(err, domain.ErrUnsupportedPlayer) {
			t.Fatalf("unexpected %v", err)
		}
	}
}
