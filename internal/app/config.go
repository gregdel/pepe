package app

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	yaml "github.com/goccy/go-yaml"
)

const (
	defaultMPVStartupTimeout = 3 * time.Second
	defaultMPVStopTimeout    = 2 * time.Second
)

// Config holds the application configuration.
type Config struct {
	PolochonURL        string        `yaml:"polochon_url"`
	Token              string        `yaml:"polochon_token"`
	MPVPath            string        `yaml:"mpv_path"`
	Fullscreen         bool          `yaml:"fullscreen"`
	PlayerFullscreen   bool          `yaml:"player_fullscreen"`
	MPVManagedLauncher bool          `yaml:"mpv_managed_launcher"`
	MPVStartupTimeout  time.Duration `yaml:"mpv_startup_timeout"`
	MPVStopTimeout     time.Duration `yaml:"mpv_stop_timeout"`
	WebUIAddr          string        `yaml:"web_ui_addr"`
}

// DefaultConfig returns a fresh configuration with playback defaults.
func DefaultConfig() *Config {
	return &Config{
		MPVPath:           "mpv",
		MPVStartupTimeout: defaultMPVStartupTimeout,
		MPVStopTimeout:    defaultMPVStopTimeout,
	}
}

// ValidatePlayer checks playback settings after configuration or flag overrides.
func (c *Config) ValidatePlayer() error {
	if c.MPVStartupTimeout <= 0 {
		return fmt.Errorf("mpv_startup_timeout must be positive")
	}
	if c.MPVStopTimeout <= 0 {
		return fmt.Errorf("mpv_stop_timeout must be positive")
	}
	return nil
}

// ValidateWebUI checks the optional listener without resolving or binding it.
func (c *Config) ValidateWebUI() error {
	addr := strings.TrimSpace(c.WebUIAddr)
	if addr == "" {
		return nil
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("web_ui_addr must be a host:port address (for example :8081)")
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return fmt.Errorf("web_ui_addr must have a numeric port between 0 and 65535")
	}
	return nil
}

func DefaultConfigPath() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "pepe", "config.yaml")
	}
	return filepath.Join(".", "pepe.yaml")
}

func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("read config file %s: %w", path, err)
			}
		} else {
			if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(cfg); err == io.EOF {
				// An empty document clears the destination; restore defaults.
				cfg = DefaultConfig()
			} else if err != nil {
				return nil, fmt.Errorf("parse config file %s: %w", path, err)
			}
		}
	}
	cfg.PolochonURL = strings.TrimSpace(cfg.PolochonURL)
	cfg.Token = strings.TrimSpace(cfg.Token)
	cfg.WebUIAddr = strings.TrimSpace(cfg.WebUIAddr)
	if cfg.MPVPath = strings.TrimSpace(cfg.MPVPath); cfg.MPVPath == "" {
		cfg.MPVPath = "mpv"
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
	if err := cfg.ValidatePlayer(); err != nil {
		return nil, err
	}
	if err := cfg.ValidateWebUI(); err != nil {
		return nil, err
	}
	return cfg, nil
}
