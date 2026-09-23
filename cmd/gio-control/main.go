package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gregdel/pepe/internal/app"
	"github.com/gregdel/pepe/internal/driver"
)

const (
	defaultWidth  = 1024
	defaultHeight = 768
)

func main() {
	if os.Getenv("GIO_CONTROL_MOCK_MPV") == "1" && len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "--include=") {
		// The headless driver invokes its own executable in place of mpv.
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
		defer signal.Stop(stop)
		parent := os.Getppid()
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				if os.Getppid() != parent {
					return
				}
			}
		}
	}
	log.SetOutput(os.Stderr)
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get working directory: %v\n", err)
		os.Exit(1)
	}
	ui, cleanup, err := newUI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "initialize UI: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	drv, err := driver.New(driver.Config{
		Width: defaultWidth, Height: defaultHeight, PxPerDp: 1, PxPerSp: 1,
	}, ui.Layout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "initialize headless driver: %v\n", err)
		os.Exit(1)
	}
	defer drv.Close()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		<-signals
		drv.Close()
		cleanup()
		os.Exit(0)
	}()

	out := os.Stdout
	if err := writeReady(out, drv.Size().X, drv.Size().Y); err != nil {
		fmt.Fprintf(os.Stderr, "write ready event: %v\n", err)
		os.Exit(1)
	}
	if err := Serve(context.Background(), os.Stdin, out, drv, root); err != nil {
		fmt.Fprintf(os.Stderr, "serve protocol: %v\n", err)
		os.Exit(1)
	}
}

func writeReady(w interface{ Write([]byte) (int, error) }, width, height int) error {
	_, err := fmt.Fprintf(w, "{\"event\":\"ready\",\"pid\":%d,\"width\":%d,\"height\":%d}\n", os.Getpid(), width, height)
	return err
}

func newUI() (*app.App, func(), error) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{}))
	if os.Getenv("GIO_CONTROL_MOCK_MPV") == "1" {
		path, err := os.Executable()
		if err != nil {
			return nil, nil, err
		}
		client, err := app.NewHeadlessPlaybackClient(logger)
		if err != nil {
			return nil, nil, err
		}
		return app.New(client, nil, &app.Config{MPVPath: path}, nil, nil), func() {}, nil
	}
	configPath := os.Getenv("GIO_CONTROL_CONFIG")
	if configPath == "" {
		configPath = app.DefaultConfigPath()
	}

	configured := os.Getenv("POLOCHON_URL") != "" || os.Getenv("POLOCHON_TOKEN") != ""
	if _, err := os.Stat(configPath); err == nil {
		configured = true
	} else if !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("stat config %s: %w", configPath, err)
	}

	if !configured {
		return app.New(app.NewEmptyAPIClient(logger), nil, &app.Config{MPVPath: "mpv"}, nil, nil), func() {}, nil
	}

	config, err := app.LoadConfig(configPath)
	if err != nil {
		return nil, nil, err
	}
	client, err := app.NewAPIClient(config.PolochonURL, config.Token, logger)
	if err != nil {
		return nil, nil, err
	}
	if err := client.FetchLibrary(); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	app := app.New(client, client.Events(ctx), config, nil, nil)
	return app, cancel, nil
}
