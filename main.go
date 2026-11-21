package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"syscall"
	"time"

	gioapp "gioui.org/app"
	"gioui.org/unit"
	"github.com/gregdel/pepe/internal/app"
	"github.com/gregdel/pepe/internal/install"
)

func main() {
	if handled, status := install.Run(os.Args[1:], os.Stdout, os.Stderr); handled {
		os.Exit(status)
	}
	flag.Usage = func() {
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), "Usage: pepe [flags]\n       pepe install|uninstall [-h]\n\n")
		flag.PrintDefaults()
	}
	configPath := flag.String("config", app.DefaultConfigPath(), "config file path")
	logFilePath := flag.String("logFile", "", "enable logging to a file (disabled by default)")
	fullscreenFlag := flag.Bool("fullscreen", false, "start in fullscreen mode")
	windowedFlag := flag.Bool("windowed", false, "start in windowed mode")
	mpvPath := flag.String("mpv", "", "mpv executable path")
	playerFlags := registerMPVFlags(flag.CommandLine)
	interfaceFlags := registerInterfaceFlags(flag.CommandLine)
	webUIAddr := flag.String("web-ui-addr", "", "web remote listen address (empty disables; no authentication)")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "Unexpected argument: %s\n", flag.Arg(0))
		flag.Usage()
		os.Exit(2)
	}

	source := app.ConfigSource{Path: *configPath, Overrides: configOverrides(flag.CommandLine)}
	config, err := app.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		fmt.Fprintf(os.Stderr, "\nRequired configuration:\n")
		fmt.Fprintf(os.Stderr, "  %s with polochon_url and polochon_token\n", *configPath)
		fmt.Fprintf(os.Stderr, "  or POLOCHON_URL and POLOCHON_TOKEN environment variables\n")
		showSetup(source, err)
	}
	if *mpvPath != "" {
		config.MPVPath = *mpvPath
	}
	if err := playerFlags.apply(config, flag.CommandLine); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := applyWebUIAddr(config, flag.CommandLine, *webUIAddr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := interfaceFlags.apply(config, flag.CommandLine); err != nil {
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

	logs := app.NewLogBuffer(config.Token)
	logFile, err := configureLogging(*logFilePath, logs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open log file '%s': %v\n", *logFilePath, err)
		slog.SetDefault(slog.New(logs.Handler(slog.NewTextHandler(os.Stderr, nil))))
	} else if logFile != nil {
		defer func() { _ = logFile.Close() }()
	}

	log := slog.Default().With("app", "pepe")
	apiClient, err := app.NewAPIClient(config.PolochonURL, config.Token, log)
	if err != nil {
		log.Error("failed to create API client", "error", err)
		fmt.Fprintf(os.Stderr, "Failed to create API client: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sseCh := apiClient.Events(ctx)
	// A server that is down at startup is retried from the window, not fatal.
	loadErr := apiClient.FetchLibrary()
	if loadErr != nil {
		log.Error("failed to fetch library", "error", loadErr)
		fmt.Fprintf(os.Stderr, "Failed to fetch library, retrying in the background: %v\n", loadErr)
	}

	go func() {
		w := new(gioapp.Window)
		w.Option(gioapp.Title("pépé"), gioapp.Size(unit.Dp(1200), unit.Dp(700)))
		if config.Fullscreen {
			w.Option(gioapp.Decorated(false), gioapp.Fullscreen.Option())
		}
		err := app.Run(w, apiClient, sseCh, config, loadErr, logs, source)
		if errors.Is(err, app.ErrRestart) {
			restart(source)
		}
		if err != nil {
			log.Error("error running app", "error", err)
			fmt.Fprintf(os.Stderr, "Error running app: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}()
	gioapp.Main()
}

type interfaceFlags struct {
	controls string
	helpBar  bool
	mpvOSD   string
}

func registerInterfaceFlags(flags *flag.FlagSet) *interfaceFlags {
	f := &interfaceFlags{}
	flags.StringVar(&f.controls, "controls", string(app.ControlsKeyboard), "control profile: keyboard or hdmi-cec")
	flags.BoolVar(&f.helpBar, "help-bar", true, "show contextual help (independent of controls)")
	flags.StringVar(&f.mpvOSD, "mpv-osd", "pepe", "playback overlay: pepe or native")
	return f
}

func (f *interfaceFlags) apply(config *app.Config, flags *flag.FlagSet) error {
	flags.Visit(func(setting *flag.Flag) {
		switch setting.Name {
		case "controls":
			config.Controls = app.ControlProfile(f.controls)
		case "help-bar":
			visible := f.helpBar
			config.HelpBar = &visible
		case "mpv-osd":
			config.MPVOSD = f.mpvOSD
		}
	})
	return config.ValidateInterface()
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

func applyWebUIAddr(config *app.Config, flags *flag.FlagSet, addr string) error {
	flags.Visit(func(setting *flag.Flag) {
		if setting.Name == "web-ui-addr" {
			config.WebUIAddr = strings.TrimSpace(addr)
		}
	})
	return config.ValidateWebUI()
}

// showSetup explains a configuration problem in a window, since desktop
// launches have no visible stderr. It restarts pépé once the settings form
// saved a valid config, and otherwise exits with status 1.
func showSetup(source app.ConfigSource, problem error) {
	path := source.Path
	created := false
	// Environment-only setups keep relying on the environment.
	if os.Getenv("POLOCHON_URL") == "" && os.Getenv("POLOCHON_TOKEN") == "" {
		var err error
		if created, err = app.WriteConfigTemplate(path); err != nil {
			fmt.Fprintf(os.Stderr, "Could not create %s: %v\n", path, err)
		} else if created {
			fmt.Fprintf(os.Stderr, "Created %s from the example; edit it and start pépé again.\n", path)
		}
	}
	go func() {
		w := new(gioapp.Window)
		w.Option(gioapp.Title("pépé"), gioapp.Size(unit.Dp(1200), unit.Dp(700)))
		err := app.RunSetup(w, source, problem, created)
		if errors.Is(err, app.ErrRestart) {
			restart(source)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error running app: %v\n", err)
		}
		os.Exit(1)
	}()
	gioapp.Main()
}

// configureLogging always keeps recent entries in memory for the TV and web
// UIs; a log file is optional and receives the same redacted records.
func configureLogging(path string, logs *app.LogBuffer) (*os.File, error) {
	var file *os.File
	var next slog.Handler
	if path != "" {
		var err error
		file, err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, err
		}
		next = slog.NewTextHandler(file, nil)
	}
	// SetDefault also routes standard-library log calls through this handler.
	slog.SetDefault(slog.New(logs.Handler(next)))
	return file, nil
}

// configOverrides maps config keys to the environment variable or flag that
// replaces their file value, for the settings form. It mirrors how main and
// the apply functions use each flag.
func configOverrides(flags *flag.FlagSet) map[string]string {
	overrides := app.EnvOverrides()
	keys := map[string][]string{
		"mpv":         {"mpv_path"},
		"fullscreen":  {"fullscreen", "player_fullscreen"},
		"windowed":    {"fullscreen"},
		"controls":    {"controls"},
		"help-bar":    {"help_bar"},
		"mpv-osd":     {"mpv_osd"},
		"web-ui-addr": {"web_ui_addr"},
	}
	// flags.Visit is sorted, so -windowed, applied after -fullscreen, wins.
	flags.Visit(func(setting *flag.Flag) {
		switch setting.Name {
		case "mpv":
			// main only applies a non-empty path.
			if setting.Value.String() == "" {
				return
			}
		case "fullscreen", "windowed":
			// main only applies these when true.
			if setting.Value.String() != "true" {
				return
			}
		}
		for _, key := range keys[setting.Name] {
			overrides[key] = "-" + setting.Name
		}
	})
	return overrides
}

// restart replaces the process with a fresh pépé, keeping its arguments and
// environment, so saved settings take effect. It runs on the window goroutine
// while gioapp.Main holds the main thread. Desktop launches have no visible
// stderr, so a failure is shown on the setup screen, where saving retries.
func restart(source app.ConfigSource) {
	exe, err := os.Executable()
	if err == nil {
		err = syscall.Exec(exe, os.Args, os.Environ())
	}
	err = fmt.Errorf("the settings were saved, but pépé could not restart: %w", err)
	slog.Error("restart failed", "error", err)
	fmt.Fprintln(os.Stderr, err)
	w := new(gioapp.Window)
	w.Option(gioapp.Title("pépé"), gioapp.Size(unit.Dp(1200), unit.Dp(700)))
	if errors.Is(app.RunSetup(w, source, err, false), app.ErrRestart) {
		restart(source)
	}
	os.Exit(1)
}
