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
  tree                               semantic tree as JSON
  tree --compact [--all|--selected]  one node per line with on-screen bounds;
                                     plain wrappers are hidden (--all keeps
                                     them), off-screen subtrees are collapsed,
                                     --selected keeps only the selection paths
  key NAME [MODIFIER...]             key press, e.g. RightArrow, Return, Escape
  click [--occurrence N] LABEL       click a semantic label (1-based occurrence)
  click --id N                       click a semantic node id
  click --xy X Y                     click viewport coordinates
  type TEXT                          insert text into the focused editor
  resize WIDTH HEIGHT                resize the viewport
  size                               viewport size
  screenshot [--crop X Y W H] [--scale F] [--no-wait]
                                     render a PNG and print its absolute path;
                                     crop to a viewport area, shrink by F<1;
                                     first waits up to 8s for the library and
                                     artwork to load (warns if still loading)
  wait [SECONDS]                     wait up to SECONDS (default 8, max 25)
                                     for loading to finish; prints idle
  shutdown                           stop the server
  raw JSON                           send {"method":...,"params":...} as-is

key, click and type print the selected (focused) node(s) afterwards, one per
line, or "no selection". If the action was applied but the selection could
not be read (the UI failed to render, or the server predates this), they
print a warning on stderr instead and exit 3; do not retry the action
blindly, since it already took effect.

Arguments after the command are taken literally, except tree, click and
screenshot options; use
"click -- LABEL" for a label that starts with "--".
`
)

type callRequest struct {
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type outputKind int

const (
	kindJSON outputKind = iota
	kindCompactTree
	kindScreenshotPath
	kindSelection
)

// treeOptions trims a compact tree.
type treeOptions struct {
	// all keeps plain wrapper nodes, which have nothing but bounds.
	all bool
	// selected keeps only the paths to selected nodes and their subtrees.
	selected bool
}

// callOutput says how runCall prints a result.
type callOutput struct {
	kind outputKind
	tree treeOptions
}

var (
	outputJSON           = callOutput{kind: kindJSON}
	outputCompactTree    = callOutput{kind: kindCompactTree}
	outputScreenshotPath = callOutput{kind: kindScreenshotPath}
	outputSelection      = callOutput{kind: kindSelection}
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
	if output.kind == kindCompactTree {
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
	if err := printCallResult(stdout, stderr, output, result, viewport); err != nil {
		_, _ = fmt.Fprintf(stderr, "gio-control call: %v\n", err)
		var applied appliedWarning
		if errors.As(err, &applied) {
			return 3
		}
		return 1
	}
	return 0
}

// appliedWarning reports an action that took effect although its outcome
// could not be observed.
type appliedWarning string

func (w appliedWarning) Error() string { return "warning: " + string(w) }

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

// buildCallRequest turns call arguments into a request. Only tree, click and
// screenshot take options; every other argument is literal, so text, key names and
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
		return buildTreeRequest(args)
	case "key":
		if len(args) == 0 {
			return callRequest{}, callOutput{}, errors.New("usage: key NAME [MODIFIER...]")
		}
		params := keyParams{Name: args[0], Modifiers: append([]string{}, args[1:]...)}
		return callRequest{Method: "key", Params: params}, outputSelection, nil
	case "click":
		return buildClickRequest(args)
	case "type":
		if err := exactly(1, "type TEXT"); err != nil {
			return callRequest{}, callOutput{}, err
		}
		return callRequest{Method: "type", Params: typeParams{Text: args[0]}}, outputSelection, nil
	case "resize":
		if err := exactly(2, "resize WIDTH HEIGHT"); err != nil {
			return callRequest{}, callOutput{}, err
		}
		width, err := strconv.Atoi(args[0])
		if err != nil {
			return callRequest{}, callOutput{}, fmt.Errorf("resize width: %w", err)
		}
		height, err := strconv.Atoi(args[1])
		if err != nil {
			return callRequest{}, callOutput{}, fmt.Errorf("resize height: %w", err)
		}
		return callRequest{Method: "resize", Params: resizeParams{Width: width, Height: height}}, outputJSON, nil
	case "screenshot":
		return buildScreenshotRequest(args)
	case "wait":
		if len(args) > 1 {
			return callRequest{}, callOutput{}, errors.New("usage: wait [SECONDS]")
		}
		req := callRequest{Method: "wait"}
		if len(args) == 1 {
			seconds, err := strconv.ParseFloat(args[0], 64)
			if err != nil {
				return callRequest{}, callOutput{}, fmt.Errorf("wait seconds: %w", err)
			}
			if !(seconds > 0 && seconds <= maxWait.Seconds()) { // also rejects NaN
				return callRequest{}, callOutput{}, fmt.Errorf("wait seconds %g is outside (0, %g]", seconds, maxWait.Seconds())
			}
			req.Params = waitParams{Seconds: seconds}
		}
		return req, outputJSON, nil
	case "shutdown", "size":
		if err := exactly(0, command); err != nil {
			return callRequest{}, callOutput{}, err
		}
		return callRequest{Method: command}, outputJSON, nil
	case "raw":
		if len(args) != 1 {
			return callRequest{}, callOutput{}, errors.New(`usage: raw '{"method":"tree"}'`)
		}
		var raw struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(args[0]), &raw); err != nil {
			return callRequest{}, callOutput{}, fmt.Errorf("raw request: %w", err)
		}
		if raw.Method == "" {
			return callRequest{}, callOutput{}, errors.New("raw request needs a method")
		}
		req := callRequest{Method: raw.Method}
		if len(raw.Params) > 0 {
			req.Params = raw.Params
		}
		return req, outputJSON, nil
	default:
		return callRequest{}, callOutput{}, fmt.Errorf("unknown command %q", command)
	}
}

// isOption reports whether arg is --name or -name.
func isOption(arg, name string) bool {
	return arg == "--"+name || arg == "-"+name
}

// buildTreeRequest parses "[--compact] [--all|--selected]"; --all and
// --selected imply --compact.
func buildTreeRequest(args []string) (callRequest, callOutput, error) {
	usage := errors.New("usage: tree [--compact] [--all|--selected]")
	output := outputJSON
	for _, arg := range args {
		switch {
		case isOption(arg, "compact"):
		case isOption(arg, "all"):
			output.tree.all = true
		case isOption(arg, "selected"):
			output.tree.selected = true
		default:
			return callRequest{}, callOutput{}, usage
		}
		output.kind = kindCompactTree
	}
	if output.tree.all && output.tree.selected {
		return callRequest{}, callOutput{}, usage
	}
	return callRequest{Method: "tree"}, output, nil
}

// buildScreenshotRequest parses "[--crop X Y W H] [--scale F]".
func buildScreenshotRequest(args []string) (callRequest, callOutput, error) {
	usage := errors.New("usage: screenshot [--crop X Y W H] [--scale F] [--no-wait]")
	var params screenshotParams
	for len(args) > 0 {
		switch {
		case isOption(args[0], "crop") && len(args) >= 5 && params.Crop == nil:
			var values [4]int
			for i := range values {
				value, err := strconv.Atoi(args[1+i])
				if err != nil {
					return callRequest{}, callOutput{}, fmt.Errorf("screenshot crop: %w", err)
				}
				values[i] = value
			}
			if values[2] <= 0 || values[3] <= 0 {
				return callRequest{}, callOutput{}, errors.New("screenshot crop needs a positive width and height")
			}
			params.Crop = &cropParams{X: values[0], Y: values[1], Width: values[2], Height: values[3]}
			args = args[5:]
		case isOption(args[0], "scale") && len(args) >= 2 && params.Scale == 0:
			scale, err := strconv.ParseFloat(args[1], 64)
			if err != nil {
				return callRequest{}, callOutput{}, fmt.Errorf("screenshot scale: %w", err)
			}
			if !(scale > 0 && scale <= 1) { // also rejects NaN
				return callRequest{}, callOutput{}, fmt.Errorf("screenshot scale %g is outside (0, 1]", scale)
			}
			params.Scale = scale
			args = args[2:]
		case isOption(args[0], "no-wait") && !params.NoWait:
			params.NoWait = true
			args = args[1:]
		default:
			return callRequest{}, callOutput{}, usage
		}
	}
	req := callRequest{Method: "screenshot"}
	if params.Crop != nil || params.Scale != 0 || params.NoWait {
		req.Params = params
	}
	return req, outputScreenshotPath, nil
}

// buildClickRequest parses "[--occurrence N] [--] LABEL", "--id N" or
// "--xy X Y" by hand, so labels and coordinates may start with "-".
func buildClickRequest(args []string) (callRequest, callOutput, error) {
	usage := errors.New("usage: click [--occurrence N] [--] LABEL | click --id N | click --xy X Y")
	option := func(name string) bool {
		return len(args) > 0 && isOption(args[0], name)
	}
	var params clickParams
	switch {
	case option("xy"):
		if len(args) != 3 {
			return callRequest{}, callOutput{}, usage
		}
		x, err := strconv.ParseFloat(args[1], 32)
		if err != nil {
			return callRequest{}, callOutput{}, fmt.Errorf("click x: %w", err)
		}
		y, err := strconv.ParseFloat(args[2], 32)
		if err != nil {
			return callRequest{}, callOutput{}, fmt.Errorf("click y: %w", err)
		}
		fx, fy := float32(x), float32(y)
		params.X, params.Y = &fx, &fy
	case option("id"):
		if len(args) != 2 {
			return callRequest{}, callOutput{}, usage
		}
		id, err := strconv.ParseUint(args[1], 10, 64)
		if err != nil {
			return callRequest{}, callOutput{}, fmt.Errorf("click id: %w", err)
		}
		params.ID = &id
	default:
		if option("occurrence") {
			if len(args) < 2 {
				return callRequest{}, callOutput{}, usage
			}
			occurrence, err := strconv.Atoi(args[1])
			if err != nil || occurrence < 0 {
				return callRequest{}, callOutput{}, usage
			}
			params.Occurrence = occurrence
			args = args[2:]
		}
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		if len(args) != 1 {
			return callRequest{}, callOutput{}, usage
		}
		params.Label = args[0]
	}
	return callRequest{Method: "click", Params: params}, outputSelection, nil
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

// printCallResult prints result; viewport clips compact tree bounds. Notes
// that need no action go to stderr.
func printCallResult(w, stderr io.Writer, output callOutput, result json.RawMessage, viewport image.Rectangle) error {
	switch output.kind {
	case kindCompactTree:
		var nodes []driver.Node
		if err := json.Unmarshal(result, &nodes); err != nil {
			return fmt.Errorf("decode tree: %w", err)
		}
		_, err := io.WriteString(w, formatCompactTree(nodes, viewport, output.tree))
		return err
	case kindScreenshotPath:
		var shot struct {
			AbsolutePath string `json:"absolutePath"`
			Loading      bool   `json:"loading"`
		}
		if err := json.Unmarshal(result, &shot); err != nil {
			return fmt.Errorf("decode screenshot: %w", err)
		}
		if shot.AbsolutePath == "" {
			return errors.New("screenshot response has no absolutePath")
		}
		if shot.Loading {
			_, _ = fmt.Fprintln(stderr, "gio-control call: note: artwork was still loading; run wait, then screenshot again")
		}
		_, err := fmt.Fprintln(w, shot.AbsolutePath)
		return err
	case kindSelection:
		var action struct {
			Selected *[]driver.Selection `json:"selected"`
			Warning  string              `json:"warning"`
		}
		if err := json.Unmarshal(result, &action); err != nil {
			return fmt.Errorf("decode selection: %w", err)
		}
		if action.Warning != "" {
			return appliedWarning(action.Warning)
		}
		if action.Selected == nil {
			return appliedWarning("the server did not report the selection; restart it (scripts/gio restart)")
		}
		_, err := io.WriteString(w, formatSelection(*action.Selected))
		return err
	default:
		_, err := fmt.Fprintln(w, string(result))
		return err
	}
}

// formatSelection prints one selection per line, "#id "label" @x,y WxH", or
// "no selection".
func formatSelection(selections []driver.Selection) string {
	if len(selections) == 0 {
		return "no selection\n"
	}
	var b strings.Builder
	for _, selection := range selections {
		fmt.Fprintf(&b, "#%d", selection.ID)
		if selection.Label != "" {
			fmt.Fprintf(&b, " %q", selection.Label)
		}
		b.WriteString(formatBounds(selection.Bounds.Rect()))
		b.WriteString("\n")
	}
	return b.String()
}

// formatBounds prints " @x,y WxH", or " [offscreen]" for an empty area.
func formatBounds(area image.Rectangle) string {
	if area.Empty() {
		return " [offscreen]"
	}
	return fmt.Sprintf(" @%d,%d %dx%d", area.Min.X, area.Min.Y, area.Dx(), area.Dy())
}

// formatCompactTree prints one node per line, indented by depth:
// "#id class "label" (description) [selected] [disabled] @x,y WxH".
// Bounds are clipped to the viewport and to every ancestor, like the area a
// semantic click aims at, because raw semantic bounds are unclipped layout
// boxes (text can report a near-infinite height).
//
// To keep the output short, plain wrappers (no class, label, description,
// state or gesture) are left out and their children move up a level, unless
// opts.all; a node with nothing visible is printed as
// "[offscreen, N more]" without its N descendants; opts.selected keeps only
// the paths to selected nodes and their subtrees. An off-screen path is never
// collapsed before it reaches a selected node.
func formatCompactTree(nodes []driver.Node, viewport image.Rectangle, opts treeOptions) string {
	var b strings.Builder
	// containsSelection holds the nodes with a selection below them.
	containsSelection := map[uint64]bool{}
	markSelectionPaths(nodes, containsSelection)
	var walk func(nodes []driver.Node, depth int, clip image.Rectangle, inSelection bool)
	walk = func(nodes []driver.Node, depth int, clip image.Rectangle, inSelection bool) {
		for _, node := range nodes {
			if opts.selected && !inSelection && !node.Selected && !containsSelection[node.ID] {
				continue
			}
			visible := clip.Intersect(node.Bounds.Rect())
			if visible.Empty() && containsSelection[node.ID] {
				// An ancestor of an off-screen selection: keep the path, so
				// the selected node itself is still printed.
				childDepth := depth
				if opts.all || !isPlainWrapper(node) {
					b.WriteString(strings.Repeat("  ", depth))
					writeNodeHeader(&b, node)
					b.WriteString(" [offscreen]\n")
					childDepth++
				}
				walk(node.Children, childDepth, visible, inSelection || node.Selected)
				continue
			}
			if visible.Empty() {
				hidden := countShown(node.Children, opts.all)
				if !opts.all && isPlainWrapper(node) && hidden == 0 {
					continue
				}
				b.WriteString(strings.Repeat("  ", depth))
				writeNodeHeader(&b, node)
				if hidden > 0 {
					fmt.Fprintf(&b, " [offscreen, %d more]\n", hidden)
				} else {
					b.WriteString(" [offscreen]\n")
				}
				continue
			}
			childDepth := depth
			if opts.all || !isPlainWrapper(node) {
				b.WriteString(strings.Repeat("  ", depth))
				writeNodeHeader(&b, node)
				b.WriteString(formatBounds(visible))
				b.WriteString("\n")
				childDepth++
			}
			walk(node.Children, childDepth, visible, inSelection || node.Selected)
		}
	}
	walk(nodes, 0, viewport, false)
	if opts.selected && b.Len() == 0 {
		return "no selection\n"
	}
	return b.String()
}

// writeNodeHeader writes "#id class "label" (description) [selected] [disabled]".
func writeNodeHeader(b *strings.Builder, node driver.Node) {
	fmt.Fprintf(b, "#%d", node.ID)
	if node.HasClass() {
		fmt.Fprintf(b, " %s", node.Class)
	}
	if node.Label != "" {
		fmt.Fprintf(b, " %q", node.Label)
	}
	if node.Description != "" {
		fmt.Fprintf(b, " (%s)", node.Description)
	}
	if node.Selected {
		b.WriteString(" [selected]")
	}
	if node.Disabled {
		b.WriteString(" [disabled]")
	}
}

func isPlainWrapper(node driver.Node) bool {
	return !node.HasClass() && node.Label == "" && node.Description == "" &&
		!node.Selected && !node.Disabled && len(node.Gestures) == 0
}

// markSelectionPaths records in contains the ID of every node with a selected
// descendant, and reports whether nodes are or contain a selection.
func markSelectionPaths(nodes []driver.Node, contains map[uint64]bool) bool {
	found := false
	for _, node := range nodes {
		if markSelectionPaths(node.Children, contains) {
			contains[node.ID] = true
			found = true
		}
		if node.Selected {
			found = true
		}
	}
	return found
}

// countShown counts the nodes under nodes that the compact tree would print:
// every node with all, else all but plain wrappers.
func countShown(nodes []driver.Node, all bool) int {
	count := 0
	for _, node := range nodes {
		if all || !isPlainWrapper(node) {
			count++
		}
		count += countShown(node.Children, all)
	}
	return count
}
