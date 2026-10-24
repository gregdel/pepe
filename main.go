package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
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

	logFile, err := configureLogging(*logFilePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open log file '%s': %v\n", *logFilePath, err)
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
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
	if err := apiClient.FetchLibrary(); err != nil {
		log.Error("failed to fetch library", "error", err)
		fmt.Fprintf(os.Stderr, "Failed to fetch library: %v\n", err)
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

func configureLogging(path string) (*os.File, error) {
	output := io.Discard
	var file *os.File
	if path != "" {
		var err error
		file, err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, err
		}
		output = file
	}
	// SetDefault also routes standard-library log calls through this handler.
	slog.SetDefault(slog.New(slog.NewTextHandler(output, nil)))
	return file, nil
}
