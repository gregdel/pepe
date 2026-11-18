package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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
	os.Exit(run())
}

// run starts the driver and serves the stdio or socket protocol. It returns the
// process exit code, so deferred teardown runs on every return path; a signal
// in stdio mode instead tears down and exits from its handler.
func run() int {
	log.SetOutput(os.Stderr)
	var socketPath string
	serveMode := false
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "call":
			return runCall(os.Args[2:], os.Stdout, os.Stderr)
		case "probe":
			return runProbe(os.Args[2:], os.Stderr)
		case "serve":
			var err error
			if socketPath, err = parseServeArgs(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "gio-control serve: %v\nusage: gio-control serve [--socket PATH]\n", err)
				return 2
			}
			serveMode = true
		default:
			fmt.Fprintf(os.Stderr, "usage: gio-control [serve [--socket PATH] | call ... | probe [--socket PATH]]\nunknown command %q\n", os.Args[1])
			return 2
		}
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get working directory: %v\n", err)
		return 1
	}
	ui, cleanup, err := newUI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "initialize UI: %v\n", err)
		return 1
	}
	defer cleanup()

	drv, err := driver.New(driver.Config{
		Width: defaultWidth, Height: defaultHeight, PxPerDp: 1, PxPerSp: 1,
	}, ui.Layout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "initialize headless driver: %v\n", err)
		return 1
	}
	defer drv.Close()

	if serveMode {
		if err := runSocketServer(socketPath, drv, root); err != nil {
			fmt.Fprintf(os.Stderr, "serve socket: %v\n", err)
			return 1
		}
		return 0
	}

	// A blocking stdin read cannot be interrupted, so the stdio protocol tears
	// down and exits from the signal handler instead of returning from run.
	srv := newServer(drv, root)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		<-signals
		// Restore the default action, so a second signal ends a hung teardown.
		signal.Stop(signals)
		// Wait for the request in progress and keep later ones out: the
		// driver is not safe for concurrent use.
		srv.mu.Lock()
		drv.Close()
		cleanup()
		os.Exit(0)
	}()

	out := os.Stdout
	if err := writeReady(out, drv.Size().X, drv.Size().Y); err != nil {
		fmt.Fprintf(os.Stderr, "write ready event: %v\n", err)
		return 1
	}
	if _, err := srv.serve(context.Background(), os.Stdin, out); err != nil {
		fmt.Fprintf(os.Stderr, "serve protocol: %v\n", err)
		return 1
	}
	return 0
}

// parseServeArgs returns the socket path for "serve [--socket PATH]".
func parseServeArgs(args []string) (string, error) {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	socketPath := flags.String("socket", envOr("GIO_CONTROL_SOCKET", defaultSocketPath), "Unix socket to listen on")
	if err := flags.Parse(args); err != nil {
		return "", err
	}
	if flags.NArg() != 0 {
		return "", fmt.Errorf("unexpected arguments %q", flags.Args())
	}
	if *socketPath == "" {
		return "", errors.New("--socket must not be empty")
	}
	return *socketPath, nil
}

func runSocketServer(socketPath string, drv *driver.Driver, root string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// Restore the default action after the first signal, so a second one ends
	// a hung request or teardown.
	context.AfterFunc(ctx, stop)
	return serveSocket(ctx, socketPath, newServer(drv, root), func() error {
		return writeReady(os.Stdout, drv.Size().X, drv.Size().Y)
	})
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
