package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sirupsen/logrus"
)

func main() {
	// Setup logger
	log := logrus.New()
	log.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	logEntry := log.WithField("app", "polochon-tui")

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
	apiClient, err := newAPIClient(config.PolochonURL, config.Token, config.RefreshInterval, logEntry)
	if err != nil {
		logEntry.Fatalf("Failed to create API client: %v", err)
	}

	// Initial fetch
	if err := apiClient.fetchLibrary(); err != nil {
		logEntry.Fatalf("Failed to fetch library: %v", err)
	}

	// Start background refresh
	stopCh := make(chan struct{})
	defer close(stopCh)
	go apiClient.startPeriodicRefresh(stopCh)

	// Create and run the TUI
	model := initialModel(apiClient)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		logEntry.Fatalf("Error running TUI: %v", err)
	}
}
