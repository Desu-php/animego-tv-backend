package config

import (
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("ANIMEGO_BASE_URL", "")
	t.Setenv("ANIMEGO_TIMEOUT", "")
	t.Setenv("PLAYER_TIMEOUT", "")
	cfg, err := Load()
	if err != nil || cfg.HTTPAddr != ":8080" || cfg.AnimeGOBaseURL != "https://animego.me" || cfg.AnimeGOTimeout != 10*time.Second || cfg.PlayerTimeout != 10*time.Second {
		t.Fatalf("unexpected defaults: %#v, %v", cfg, err)
	}
	t.Setenv("HTTP_ADDR", "127.0.0.1:9090")
	t.Setenv("ANIMEGO_BASE_URL", "https://example.com")
	t.Setenv("ANIMEGO_TIMEOUT", "2s")
	t.Setenv("PLAYER_TIMEOUT", "3s")
	cfg, err = Load()
	if err != nil || cfg.HTTPAddr != "127.0.0.1:9090" || cfg.AnimeGOTimeout != 2*time.Second || cfg.PlayerTimeout != 3*time.Second || cfg.AnimeGOBaseURL != "https://example.com" {
		t.Fatalf("unexpected overrides: %#v, %v", cfg, err)
	}
}

func TestLoadInvalidConfiguration(t *testing.T) {
	for _, value := range []string{"invalid", ":0", ":99999", ":http"} {
		t.Setenv("HTTP_ADDR", value)
		if _, err := Load(); err == nil {
			t.Errorf("accepted address %q", value)
		}
	}
	t.Setenv("HTTP_ADDR", ":8080")
	for _, value := range []string{"0s", "-1s", "ten"} {
		t.Setenv("ANIMEGO_TIMEOUT", value)
		if _, err := Load(); err == nil {
			t.Errorf("accepted timeout %q", value)
		}
	}
	t.Setenv("ANIMEGO_TIMEOUT", "10s")
	for _, value := range []string{"0s", "-1s", "ten"} {
		t.Setenv("PLAYER_TIMEOUT", value)
		if _, err := Load(); err == nil {
			t.Errorf("accepted player timeout %q", value)
		}
	}
}
