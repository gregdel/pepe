package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gregdel/pepe/internal/driver"
)

const (
	defaultSocketPath  = "tmp/gio/control.sock"
	defaultCallTimeout = 30 * time.Second
	callUsage          = `usage: gio-control call [--socket PATH] [--timeout DURATION] <command> [args]

commands:
  tree [--compact]                   semantic tree (JSON, or one node per line
                                     with on-screen bounds)
  key NAME [MODIFIER...]             key press, e.g. RightArrow, Return, Escape
  click [--occurrence N] LABEL       click a semantic label (1-based occurrence)
  click --id N                       click a semantic node id
  click --xy X Y                     click viewport coordinates
  type TEXT                          insert text into the focused editor
  resize WIDTH HEIGHT                resize the viewport
  size                               viewport size
  screenshot                         render a PNG and print its absolute path
  shutdown                           stop the server
  raw JSON                           send {"method":...,"params":...} as-is

Arguments after the command are taken literally, except click options; use
"click -- LABEL" for a label that starts with "--".
`
)

type callRequest struct {
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type callOutput int

const (
	outputJSON callOutput = iota
	outputCompactTree
	outputScreenshotPath
)

// runCall sends one request to a serve-mode gio-control and prints its result.
// It returns the process exit code.
func runCall(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("call", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { _, _ = fmt.Fprint(stderr, callUsage) }
	socketPath := flags.String("socket", envOr("GIO_CONTROL_SOCKET", defaultSocketPath), "server socket path")
	timeout := flags.Duration("timeout", defaultCallTimeout, "give up waiting for the response after this long")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() == 0 {
		flags.Usage()
		return 2
	}
	req, output, err := buildCallRequest(flags.Arg(0), flags.Args()[1:])
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "gio-control call: %v\n", err)
		return 2
	}
	var viewport image.Rectangle
	if output == outputCompactTree {
		if viewport, err = fetchViewport(*socketPath, *timeout); err != nil {
			_, _ = fmt.Fprintf(stderr, "gio-control call: %v\n", err)
			return 1
		}
	}
	result, err := sendCall(*socketPath, *timeout, req)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "gio-control call: %v\n", err)
		return 1
	}
	if err := printCallResult(stdout, output, result, viewport); err != nil {
		_, _ = fmt.Fprintf(stderr, "gio-control call: %v\n", err)
		return 1
	}
	return 0
}

// runProbe exits 0 when a server accepts connections on the socket. It only
// connects, so a server busy with another client still counts as running.
func runProbe(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("probe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	socketPath := flags.String("socket", envOr("GIO_CONTROL_SOCKET", defaultSocketPath), "server socket path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "usage: gio-control probe [--socket PATH]\n")
		return 2
	}
	if !socketAccepts(*socketPath) {
		return 1
	}
	return 0
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// buildCallRequest turns call arguments into a request. Only tree and click
// take options; every other argument is literal, so text, key names and
// numbers may start with "-".
func buildCallRequest(command string, args []string) (callRequest, callOutput, error) {
	exactly := func(n int, usage string) error {
		if len(args) != n {
			return fmt.Errorf("usage: %s", usage)
		}
		return nil
	}
	switch command {
	case "tree":
		switch {
		case len(args) == 0:
			return callRequest{Method: "tree"}, outputJSON, nil
		case len(args) == 1 && (args[0] == "--compact" || args[0] == "-compact"):
			return callRequest{Method: "tree"}, outputCompactTree, nil
		default:
			return callRequest{}, 0, errors.New("usage: tree [--compact]")
		}
	case "key":
		if len(args) == 0 {
			return callRequest{}, 0, errors.New("usage: key NAME [MODIFIER...]")
		}
		params := keyParams{Name: args[0], Modifiers: append([]string{}, args[1:]...)}
		return callRequest{Method: "key", Params: params}, outputJSON, nil
	case "click":
		return buildClickRequest(args)
	case "type":
		if err := exactly(1, "type TEXT"); err != nil {
			return callRequest{}, 0, err
		}
		return callRequest{Method: "type", Params: typeParams{Text: args[0]}}, outputJSON, nil
	case "resize":
		if err := exactly(2, "resize WIDTH HEIGHT"); err != nil {
			return callRequest{}, 0, err
		}
		width, err := strconv.Atoi(args[0])
		if err != nil {
			return callRequest{}, 0, fmt.Errorf("resize width: %w", err)
		}
		height, err := strconv.Atoi(args[1])
		if err != nil {
			return callRequest{}, 0, fmt.Errorf("resize height: %w", err)
		}
		return callRequest{Method: "resize", Params: resizeParams{Width: width, Height: height}}, outputJSON, nil
	case "screenshot", "shutdown", "size":
		if err := exactly(0, command); err != nil {
			return callRequest{}, 0, err
		}
		if command == "screenshot" {
			return callRequest{Method: command}, outputScreenshotPath, nil
		}
		return callRequest{Method: command}, outputJSON, nil
	case "raw":
		if len(args) != 1 {
			return callRequest{}, 0, errors.New(`usage: raw '{"method":"tree"}'`)
		}
		var raw struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(args[0]), &raw); err != nil {
			return callRequest{}, 0, fmt.Errorf("raw request: %w", err)
		}
		if raw.Method == "" {
			return callRequest{}, 0, errors.New("raw request needs a method")
		}
		req := callRequest{Method: raw.Method}
		if len(raw.Params) > 0 {
			req.Params = raw.Params
		}
		return req, outputJSON, nil
	default:
		return callRequest{}, 0, fmt.Errorf("unknown command %q", command)
	}
}

// buildClickRequest parses "[--occurrence N] [--] LABEL", "--id N" or
// "--xy X Y" by hand, so labels and coordinates may start with "-".
func buildClickRequest(args []string) (callRequest, callOutput, error) {
	usage := errors.New("usage: click [--occurrence N] [--] LABEL | click --id N | click --xy X Y")
	option := func(name string) bool {
		return len(args) > 0 && (args[0] == "--"+name || args[0] == "-"+name)
	}
	var params clickParams
	switch {
	case option("xy"):
		if len(args) != 3 {
			return callRequest{}, 0, usage
		}
		x, err := strconv.ParseFloat(args[1], 32)
		if err != nil {
			return callRequest{}, 0, fmt.Errorf("click x: %w", err)
		}
		y, err := strconv.ParseFloat(args[2], 32)
		if err != nil {
			return callRequest{}, 0, fmt.Errorf("click y: %w", err)
		}
		fx, fy := float32(x), float32(y)
		params.X, params.Y = &fx, &fy
	case option("id"):
		if len(args) != 2 {
			return callRequest{}, 0, usage
		}
		id, err := strconv.ParseUint(args[1], 10, 64)
		if err != nil {
			return callRequest{}, 0, fmt.Errorf("click id: %w", err)
		}
		params.ID = &id
	default:
		if option("occurrence") {
			if len(args) < 2 {
				return callRequest{}, 0, usage
			}
			occurrence, err := strconv.Atoi(args[1])
			if err != nil || occurrence < 0 {
				return callRequest{}, 0, usage
			}
			params.Occurrence = occurrence
			args = args[2:]
		}
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		if len(args) != 1 {
			return callRequest{}, 0, usage
		}
		params.Label = args[0]
	}
	return callRequest{Method: "click", Params: params}, outputJSON, nil
}

// sendCall sends one request and waits at most timeout for its response,
// including any time spent queued behind another client.
func sendCall(socketPath string, timeout time.Duration, req callRequest) (json.RawMessage, error) {
	if err := validateSocketPath(socketPath); err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout("unix", socketPath, socketProbeTimeout)
	if err != nil {
		return nil, fmt.Errorf("no gio-control server at %s (start one with scripts/gio start): %w", socketPath, err)
	}
	defer func() { _ = conn.Close() }()
	if timeout > 0 {
		if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
			return nil, fmt.Errorf("set deadline: %w", err)
		}
	}
	timedOut := func(err error) error {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return fmt.Errorf("no response from gio-control within %s (server busy or hung?)", timeout)
		}
		return err
	}

	wire := struct {
		ID uint64 `json:"id"`
		callRequest
	}{ID: 1, callRequest: req}
	if err := json.NewEncoder(conn).Encode(wire); err != nil {
		return nil, fmt.Errorf("send request: %w", timedOut(err))
	}
	reader := bufio.NewReader(conn)
	// The server ends every response with a newline, so any error, even after
	// a partial line, means the response is incomplete.
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", timedOut(err))
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	return resp.Result, nil
}

// fetchViewport asks the server for its viewport, which clips compact trees.
func fetchViewport(socketPath string, timeout time.Duration) (image.Rectangle, error) {
	result, err := sendCall(socketPath, timeout, callRequest{Method: "size"})
	if err != nil {
		return image.Rectangle{}, err
	}
	var size struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	if err := json.Unmarshal(result, &size); err != nil {
		return image.Rectangle{}, fmt.Errorf("decode size: %w", err)
	}
	return image.Rect(0, 0, size.Width, size.Height), nil
}

// printCallResult prints result; viewport clips compact tree bounds.
func printCallResult(w io.Writer, output callOutput, result json.RawMessage, viewport image.Rectangle) error {
	switch output {
	case outputCompactTree:
		var nodes []driver.Node
		if err := json.Unmarshal(result, &nodes); err != nil {
			return fmt.Errorf("decode tree: %w", err)
		}
		_, err := io.WriteString(w, formatCompactTree(nodes, viewport))
		return err
	case outputScreenshotPath:
		var shot struct {
			AbsolutePath string `json:"absolutePath"`
		}
		if err := json.Unmarshal(result, &shot); err != nil {
			return fmt.Errorf("decode screenshot: %w", err)
		}
		if shot.AbsolutePath == "" {
			return errors.New("screenshot response has no absolutePath")
		}
		_, err := fmt.Fprintln(w, shot.AbsolutePath)
		return err
	default:
		_, err := fmt.Fprintln(w, string(result))
		return err
	}
}

// formatCompactTree prints one node per line, indented by depth:
// "#id class "label" (description) [selected] [disabled] @x,y WxH".
// Bounds are clipped to the viewport and to every ancestor, like the area a
// semantic click aims at, because raw semantic bounds are unclipped layout
// boxes (text can report a near-infinite height). A node with nothing left is
// marked [offscreen] instead.
func formatCompactTree(nodes []driver.Node, viewport image.Rectangle) string {
	var b strings.Builder
	var walk func(nodes []driver.Node, depth int, clip image.Rectangle)
	walk = func(nodes []driver.Node, depth int, clip image.Rectangle) {
		for _, node := range nodes {
			b.WriteString(strings.Repeat("  ", depth))
			fmt.Fprintf(&b, "#%d", node.ID)
			if node.HasClass() {
				fmt.Fprintf(&b, " %s", node.Class)
			}
			if node.Label != "" {
				fmt.Fprintf(&b, " %q", node.Label)
			}
			if node.Description != "" {
				fmt.Fprintf(&b, " (%s)", node.Description)
			}
			if node.Selected {
				b.WriteString(" [selected]")
			}
			if node.Disabled {
				b.WriteString(" [disabled]")
			}
			bounds := node.Bounds
			visible := clip.Intersect(image.Rectangle{
				Min: image.Pt(bounds.MinX, bounds.MinY),
				Max: image.Pt(bounds.MaxX, bounds.MaxY),
			})
			if visible.Empty() {
				b.WriteString(" [offscreen]\n")
			} else {
				fmt.Fprintf(&b, " @%d,%d %dx%d\n", visible.Min.X, visible.Min.Y, visible.Dx(), visible.Dy())
			}
			walk(node.Children, depth+1, visible)
		}
	}
	walk(nodes, 0, viewport)
	return b.String()
}
