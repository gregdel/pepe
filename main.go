package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	gioapp "gioui.org/app"
	"gioui.org/unit"
	"github.com/gregdel/pepe/internal/app"
)

func main() {
	defaultLogFile := defaultLogFilePath()
	configPath := flag.String("config", app.DefaultConfigPath(), "config file path")
	logFilePath := flag.String("logFile", defaultLogFile, "log file path")
	fullscreenFlag := flag.Bool("fullscreen", false, "start in fullscreen mode")
	windowedFlag := flag.Bool("windowed", false, "start in windowed mode")
	mpvPath := flag.String("mpv", "", "mpv executable path")
	playerFlags := registerMPVFlags(flag.CommandLine)
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
	if err := playerFlags.apply(config, flag.CommandLine); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
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

	log := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{})).With("app", "pepe")
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
		w.Option(gioapp.Title("Pépé"), gioapp.Size(unit.Dp(1200), unit.Dp(700)))
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

type mpvFlags struct {
	managedLauncher bool
	startupTimeout  time.Duration
	stopTimeout     time.Duration
}

func registerMPVFlags(flags *flag.FlagSet) *mpvFlags {
	defaults := app.DefaultConfig()
	f := &mpvFlags{}
	flags.BoolVar(&f.managedLauncher, "mpv-managed-launcher", defaults.MPVManagedLauncher, "require external launcher teardown confirmation")
	flags.DurationVar(&f.startupTimeout, "mpv-startup-timeout", defaults.MPVStartupTimeout, "player IPC connection timeout")
	flags.DurationVar(&f.stopTimeout, "mpv-stop-timeout", defaults.MPVStopTimeout, "player cancellation grace period")
	return f
}

func (f *mpvFlags) apply(config *app.Config, flags *flag.FlagSet) error {
	flags.Visit(func(setting *flag.Flag) {
		switch setting.Name {
		case "mpv-managed-launcher":
			config.MPVManagedLauncher = f.managedLauncher
		case "mpv-startup-timeout":
			config.MPVStartupTimeout = f.startupTimeout
		case "mpv-stop-timeout":
			config.MPVStopTimeout = f.stopTimeout
		}
	})
	return config.ValidatePlayer()
}

func defaultLogFilePath() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "pepe.log"
		}
		base = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(base, "pepe")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "pepe.log"
	}
	return filepath.Join(dir, "pepe.log")
}
