package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr             string
	AnimeGOBaseURL       string
	AnimeGOTimeout       time.Duration
	PlayerTimeout        time.Duration
	CatalogCacheCapacity int
	CatalogDetailsTTL    time.Duration
	CatalogEpisodesTTL   time.Duration
	CatalogConcurrency   int
	PlaybackConcurrency  int
}

func Load() (Config, error) {
	cfg := Config{HTTPAddr: envOrDefault("HTTP_ADDR", ":8080"), AnimeGOBaseURL: envOrDefault("ANIMEGO_BASE_URL", "https://animego.me")}
	_, port, err := net.SplitHostPort(cfg.HTTPAddr)
	if err != nil {
		return Config{}, fmt.Errorf("invalid HTTP_ADDR: %w", err)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return Config{}, fmt.Errorf("HTTP_ADDR port must be between 1 and 65535")
	}
	cfg.AnimeGOTimeout, err = time.ParseDuration(envOrDefault("ANIMEGO_TIMEOUT", "10s"))
	if err != nil {
		return Config{}, fmt.Errorf("parse ANIMEGO_TIMEOUT: %w", err)
	}
	if cfg.AnimeGOTimeout <= 0 {
		return Config{}, fmt.Errorf("ANIMEGO_TIMEOUT must be positive")
	}
	cfg.PlayerTimeout, err = time.ParseDuration(envOrDefault("PLAYER_TIMEOUT", "10s"))
	if err != nil {
		return Config{}, fmt.Errorf("parse PLAYER_TIMEOUT: %w", err)
	}
	if cfg.PlayerTimeout <= 0 {
		return Config{}, fmt.Errorf("PLAYER_TIMEOUT must be positive")
	}
	for _, setting := range []struct {
		name, fallback string
		target         *int
		allowZero      bool
	}{
		{"CATALOG_CACHE_CAPACITY", "128", &cfg.CatalogCacheCapacity, true},
		{"CATALOG_CONCURRENCY", "16", &cfg.CatalogConcurrency, false},
		{"PLAYBACK_CONCURRENCY", "4", &cfg.PlaybackConcurrency, false},
	} {
		minimum := 1
		if setting.allowZero {
			minimum = 0
		}
		value, err := strconv.Atoi(envOrDefault(setting.name, setting.fallback))
		if err != nil || value < minimum || value > 10000 {
			return Config{}, fmt.Errorf("%s must be an integer between %d and 10000", setting.name, minimum)
		}
		*setting.target = value
	}
	for _, setting := range []struct {
		name, fallback string
		target         *time.Duration
	}{
		{"CATALOG_DETAILS_TTL", "2m", &cfg.CatalogDetailsTTL},
		{"CATALOG_EPISODES_TTL", "15s", &cfg.CatalogEpisodesTTL},
	} {
		value, err := time.ParseDuration(envOrDefault(setting.name, setting.fallback))
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("%s must be a positive duration", setting.name)
		}
		*setting.target = value
	}
	return cfg, nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
