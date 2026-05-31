package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"gioui.org/app"
	"gioui.org/unit"
)

func main() {
	defaultLogFile := defaultLogFilePath()
	configPath := flag.String("config", defaultConfigPath(), "config file path")
	logFilePath := flag.String("logFile", defaultLogFile, "log file path")
	fullscreenFlag := flag.Bool("fullscreen", false, "start in fullscreen mode")
	windowedFlag := flag.Bool("windowed", false, "start in windowed mode")
	mpvPath := flag.String("mpv", "", "mpv executable path")
	flag.Parse()

	// Load configuration before opening the log so config errors are visible in
	// Steam's launch output even if the log path cannot be created.
	config, err := loadConfig(*configPath)
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
		defer func() {
			if closeErr := logFile.Close(); closeErr != nil {
				fmt.Fprintf(os.Stderr, "Failed to close log file '%s': %v\n", *logFilePath, closeErr)
			}
		}()
	}

	log := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{})).With("app", "polochon-gio")
	log.Info("configuration loaded", "config", *configPath, "fullscreen", config.Fullscreen, "mpv", config.MPVPath)

	// Create API client
	apiClient, err := newAPIClient(config.PolochonURL, config.Token, log)
	if err != nil {
		log.Error("failed to create API client", "error", err)
		os.Exit(1)
	}

	// Subscribe to SSE before the initial fetch so no change event is missed.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sseCh := apiClient.Events(ctx)

	// Initial library load
	if err := apiClient.fetchLibrary(); err != nil {
		log.Error("failed to fetch library", "error", err)
		os.Exit(1)
	}

	// Launch Gio window in a goroutine (app.Main() must run on the main thread)
	go func() {
		w := new(app.Window)
		w.Option(
			app.Title("Polochon"),
			app.Size(unit.Dp(1200), unit.Dp(700)),
		)
		if config.Fullscreen {
			w.Option(app.Decorated(false), app.Fullscreen.Option())
		}
		if err := run(w, apiClient, sseCh, config); err != nil {
			log.Error("error running app", "error", err)
		}
		os.Exit(0)
	}()
	app.Main()
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
