package player

import (
	"context"
	"errors"
	"strings"
	"testing"

	"animego/backend/internal/domain"
	"animego/backend/internal/provider"
)

func TestHLSStructure(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		master     bool
		want       error
	}{
		{"master", "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000,CODECS=\"avc1,mp4a\"\nvideo.m3u8\n", true, nil},
		{"media", clearPlaylist, false, nil},
		{"segment tags", "#EXTM3U\n#EXTINF:6,title\n#EXT-X-BYTERANGE:100@0\nvideo.mp4\n", false, nil},
		{"CRLF and BOM", "\ufeff#EXTM3U\r\n#EXTINF:6,\r\nsegment.ts\r\n", false, nil},
		{"orphan before stream tag", "#EXTM3U\nvideo.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=1000\n", true, provider.ErrParsing},
		{"missing variant URI", "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000\n", true, provider.ErrParsing},
		{"duplicate stream tag", "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000\n#EXT-X-STREAM-INF:BANDWIDTH=2000\nvideo.m3u8\n", true, provider.ErrParsing},
		{"missing segment URI", "#EXTM3U\n#EXTINF:6,\n", false, provider.ErrParsing},
		{"duplicate segment tag", "#EXTM3U\n#EXTINF:6,\n#EXTINF:6,\nsegment.ts\n", false, provider.ErrParsing},
		{"orphan segment", "#EXTM3U\n#EXTINF:6,\nsegment.ts\nother.ts\n", false, provider.ErrParsing},
		{"mixed playlists", "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000\nvideo.m3u8\n#EXTINF:6,\nsegment.ts\n", false, provider.ErrParsing},
		{"invalid duration", "#EXTM3U\n#EXTINF:NaN,\nsegment.ts\n", false, provider.ErrParsing},
		{"missing bandwidth", "#EXTM3U\n#EXT-X-STREAM-INF:CODECS=\"avc1\"\nvideo.m3u8\n", true, provider.ErrParsing},
		{"duplicate attribute", "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1,BANDWIDTH=2\nvideo.m3u8\n", true, provider.ErrParsing},
		{"unterminated attribute", "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,URI=\"audio.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=1\nvideo.m3u8\n", true, provider.ErrParsing},
		{"unquoted URI", "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,URI=audio.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=1\nvideo.m3u8\n", true, provider.ErrParsing},
		{"unsafe map", "#EXTM3U\n#EXT-X-MAP:URI=\"https://localhost/init.mp4\"\n#EXTINF:6,\nsegment.ts\n", false, provider.ErrParsing},
		{"unsafe part", "#EXTM3U\n#EXT-X-PART:DURATION=0.5,URI=\"http://internal.example/part\"\n#EXTINF:6,\nsegment.ts\n", false, provider.ErrParsing},
		{"encrypted", "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key\"\n", false, domain.ErrPlaybackUnavailable},
		{"media instead of master", clearPlaylist, true, provider.ErrParsing},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := domain.VideoSource{Type: domain.VideoHLS, URL: "https://cdn.example/master.m3u8"}
			err := ValidateManifest([]byte(tt.body), &source, tt.master)
			if tt.want == nil && err != nil || tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestHLSAttributeReferencesAreFetchedButSegmentsAreNot(t *testing.T) {
	root := "https://cdn.example/master.m3u8"
	audio := "https://audio.example/audio.m3u8?token=a,b"
	iframe := "https://cdn.example/iframe.m3u8"
	video := "https://cdn.example/video.m3u8"
	body := "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO, URI=\"" + audio + "\"\n#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=1,URI=\"iframe.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=1\nvideo.m3u8\n"
	f := &manifestFixture{bodies: map[string]string{root: body, audio: clearPlaylist, iframe: clearPlaylist, video: clearPlaylist}}
	if err := CheckManifest(context.Background(), f, domain.VideoSource{Type: domain.VideoHLS, URL: root}, true); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 4 || f.requests[1] != audio {
		t.Fatalf("incorrect playlist traversal: %v", f.requests)
	}
}

func FuzzValidateManifest(f *testing.F) {
	f.Add([]byte(clearPlaylist), false, false)
	f.Add([]byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nvideo.m3u8\n"), true, false)
	f.Add([]byte("#EXTM3U\nvideo.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=1\n"), true, false)
	f.Add([]byte("<MPD><Representation/></MPD>"), false, true)
	f.Fuzz(func(t *testing.T, data []byte, master, dash bool) {
		source := domain.VideoSource{Type: domain.VideoHLS, URL: "https://cdn.example/manifest"}
		if dash {
			source.Type = domain.VideoDASH
		}
		err := ValidateManifest(data, &source, master)
		if err != nil && !errors.Is(err, provider.ErrParsing) && !errors.Is(err, domain.ErrPlaybackUnavailable) {
			t.Fatalf("unclassified parser error: %v", err)
		}
		if err != nil && strings.Contains(err.Error(), source.URL) {
			t.Fatal("manifest URL leaked into diagnostics")
		}
	})
}
