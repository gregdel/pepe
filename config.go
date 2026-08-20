package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	yaml "github.com/goccy/go-yaml"
)

// Config holds the application configuration.
type Config struct {
	PolochonURL string
	Token       string
	MPVPath     string

	Fullscreen       bool
	PlayerFullscreen bool
}

type fileConfig struct {
	PolochonURL      string `yaml:"polochon_url"`
	Token            string `yaml:"polochon_token"`
	MPVPath          string `yaml:"mpv_path"`
	Fullscreen       bool   `yaml:"fullscreen"`
	PlayerFullscreen bool   `yaml:"player_fullscreen"`
}

func defaultConfigPath() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "polochon-gio", "config.yaml")
	}
	return filepath.Join(".", "polochon-gio.yaml")
}

func loadConfig(path string) (*Config, error) {
	cfg := &Config{MPVPath: "mpv"}
	if path != "" {
		bytes, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("read config file %s: %w", path, err)
			}
		} else {
			var file fileConfig
			if err := yaml.Unmarshal(bytes, &file); err != nil {
				return nil, fmt.Errorf("parse config file %s: %w", path, err)
			}
			cfg.PolochonURL = strings.TrimSpace(file.PolochonURL)
			cfg.Token = strings.TrimSpace(file.Token)
			if value := strings.TrimSpace(file.MPVPath); value != "" {
				cfg.MPVPath = value
			}
			cfg.Fullscreen = file.Fullscreen
			cfg.PlayerFullscreen = file.PlayerFullscreen
		}
	}
	if value := os.Getenv("POLOCHON_URL"); value != "" {
		cfg.PolochonURL = strings.TrimSpace(value)
	}
	if cfg.PolochonURL == "" {
		return nil, fmt.Errorf("POLOCHON_URL environment variable is required")
	}
	if value := os.Getenv("POLOCHON_TOKEN"); value != "" {
		cfg.Token = strings.TrimSpace(value)
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("POLOCHON_TOKEN environment variable is required")
	}
	if value := strings.TrimSpace(os.Getenv("POLOCHON_MPV")); value != "" {
		cfg.MPVPath = value
	}
	return cfg, nil
}
