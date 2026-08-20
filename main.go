package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	gioapp "gioui.org/app"
	"gioui.org/unit"
	"github.com/gregdel/polochon-gio/internal/app"
)

func main() {
	defaultLogFile := defaultLogFilePath()
	configPath := flag.String("config", app.DefaultConfigPath(), "config file path")
	logFilePath := flag.String("logFile", defaultLogFile, "log file path")
	fullscreenFlag := flag.Bool("fullscreen", false, "start in fullscreen mode")
	windowedFlag := flag.Bool("windowed", false, "start in windowed mode")
	mpvPath := flag.String("mpv", "", "mpv executable path")
	flag.Parse()

	config, err := app.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		fmt.Fprintf(os.Stderr, "\nRequired configuration:\n")
		fmt.Fprintf(os.Stderr, "  %s with polochon_url and polochon_token\n", *configPath)
		fmt.Fprintf(os.Stderr, "  or POLOCHON_URL and POLOCHON_TOKEN environment variables\n")
		os.Exit(1)
	}
	if *mpvPath != "" {
		config.MPVPath = *mpvPath
	}
	if *fullscreenFlag {
		config.Fullscreen = true
		config.PlayerFullscreen = true
	}
	if *windowedFlag {
		config.Fullscreen = false
	}

	logFile, err := os.OpenFile(*logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open log file '%s': %v\n", *logFilePath, err)
		logFile = os.Stderr
	} else {
		defer func() { _ = logFile.Close() }()
	}

	log := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{})).With("app", "polochon-gio")
	apiClient, err := app.NewAPIClient(config.PolochonURL, config.Token, log)
	if err != nil {
		log.Error("failed to create API client", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sseCh := apiClient.Events(ctx)
	if err := apiClient.FetchLibrary(); err != nil {
		log.Error("failed to fetch library", "error", err)
		os.Exit(1)
	}

	go func() {
		w := new(gioapp.Window)
		w.Option(gioapp.Title("Polochon"), gioapp.Size(unit.Dp(1200), unit.Dp(700)))
		if config.Fullscreen {
			w.Option(gioapp.Decorated(false), gioapp.Fullscreen.Option())
		}
		if err := app.Run(w, apiClient, sseCh, config); err != nil {
			log.Error("error running app", "error", err)
		}
		os.Exit(0)
	}()
	gioapp.Main()
}

func defaultLogFilePath() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "polochon-gio.log"
		}
		base = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(base, "polochon-gio")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "polochon-gio.log"
	}
	return filepath.Join(dir, "polochon-gio.log")
}
