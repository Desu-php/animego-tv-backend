package config

import (
	"testing"
	"time"
)

func TestCacheAndConcurrencyConfiguration(t *testing.T) {
	for _, name := range []string{"HTTP_ADDR", "ANIMEGO_TIMEOUT", "PLAYER_TIMEOUT", "CATALOG_CACHE_CAPACITY", "CATALOG_DETAILS_TTL", "CATALOG_EPISODES_TTL", "CATALOG_CONCURRENCY", "PLAYBACK_CONCURRENCY"} {
		t.Setenv(name, "")
	}
	cfg, err := Load()
	if err != nil || cfg.CatalogCacheCapacity != 128 || cfg.CatalogDetailsTTL != 2*time.Minute || cfg.CatalogEpisodesTTL != 15*time.Second || cfg.CatalogConcurrency != 16 || cfg.PlaybackConcurrency != 4 {
		t.Fatalf("bad defaults: %+v, %v", cfg, err)
	}
	t.Setenv("CATALOG_CACHE_CAPACITY", "0")
	if cfg, err = Load(); err != nil || cfg.CatalogCacheCapacity != 0 {
		t.Fatal("cannot disable cache")
	}
	for _, tt := range []struct{ name, value string }{
		{"CATALOG_CACHE_CAPACITY", "-1"}, {"CATALOG_CACHE_CAPACITY", "10001"}, {"CATALOG_DETAILS_TTL", "0s"}, {"CATALOG_EPISODES_TTL", "bad"},
		{"CATALOG_CONCURRENCY", "0"}, {"PLAYBACK_CONCURRENCY", "-1"}, {"PLAYBACK_CONCURRENCY", "bad"},
	} {
		t.Run(tt.name+tt.value, func(t *testing.T) {
			t.Setenv(tt.name, tt.value)
			if _, err := Load(); err == nil {
				t.Fatal("invalid resource bounds accepted")
			}
		})
	}
}
