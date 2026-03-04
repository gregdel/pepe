package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"gioui.org/app"
	"gioui.org/unit"
)

func main() {
	logFilePath := flag.String("logFile", "polochon-gio.log", "log file path")
	flag.Parse()

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

	// Load configuration
	config, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		fmt.Fprintf(os.Stderr, "\nRequired environment variables:\n")
		fmt.Fprintf(os.Stderr, "  POLOCHON_URL   - URL of the polochon API\n")
		fmt.Fprintf(os.Stderr, "  POLOCHON_TOKEN - Authentication token\n")
		os.Exit(1)
	}

	// Create API client
	apiClient, err := newAPIClient(config.PolochonURL, config.Token, config.RefreshInterval, log)
	if err != nil {
		log.Error("failed to create API client", "error", err)
		os.Exit(1)
	}

	// Initial fetch
	if err := apiClient.fetchLibrary(); err != nil {
		log.Error("failed to fetch library", "error", err)
		os.Exit(1)
	}

	// Start background refresh
	stopCh := make(chan struct{})
	defer close(stopCh)
	go apiClient.startPeriodicRefresh(stopCh)

	// Launch Gio window in a goroutine (app.Main() must run on the main thread)
	go func() {
		w := new(app.Window)
		w.Option(
			app.Title("Polochon"),
			app.Size(unit.Dp(1200), unit.Dp(700)),
		)
		if err := run(w, apiClient); err != nil {
			log.Error("error running app", "error", err)
		}
		os.Exit(0)
	}()
	app.Main()
}
