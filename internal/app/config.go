package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	yaml "github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"

	"github.com/gregdel/pepe/packaging"
)

const (
	defaultMPVStartupTimeout = 3 * time.Second
	defaultMPVStopTimeout    = 2 * time.Second
)

// ControlProfile selects hint presentation and session-local mpv bindings.
type ControlProfile string

const (
	ControlsKeyboard ControlProfile = "keyboard"
	ControlsHDMICEC  ControlProfile = "hdmi-cec"
)

// Config holds the application configuration.
type Config struct {
	PolochonURL        string         `yaml:"polochon_url"`
	Token              string         `yaml:"polochon_token"`
	MPVPath            string         `yaml:"mpv_path"`
	Fullscreen         bool           `yaml:"fullscreen"`
	PlayerFullscreen   bool           `yaml:"player_fullscreen"`
	MPVManagedLauncher bool           `yaml:"mpv_managed_launcher"`
	MPVStartupTimeout  time.Duration  `yaml:"mpv_startup_timeout"`
	MPVStopTimeout     time.Duration  `yaml:"mpv_stop_timeout"`
	WebUIAddr          string         `yaml:"web_ui_addr"`
	Controls           ControlProfile `yaml:"controls"`
	HelpBar            *bool          `yaml:"help_bar"` // nil means visible, including minimal programmatic configs
	MPVOSD             string         `yaml:"mpv_osd"`
}

// DefaultConfig returns a fresh configuration with playback defaults.
func DefaultConfig() *Config {
	return &Config{
		MPVPath:           "mpv",
		Controls:          ControlsKeyboard,
		MPVOSD:            "pepe",
		MPVStartupTimeout: defaultMPVStartupTimeout,
		MPVStopTimeout:    defaultMPVStopTimeout,
	}
}

// ValidateInterface checks controls and OSD settings independently of help visibility.
func (c *Config) ValidateInterface() error {
	if c.Controls != ControlsKeyboard && c.Controls != ControlsHDMICEC {
		return fmt.Errorf("controls must be keyboard or hdmi-cec")
	}
	if c.MPVOSD != "pepe" && c.MPVOSD != "native" {
		return fmt.Errorf("mpv_osd must be pepe or native")
	}
	return nil
}

// ValidatePlayer checks playback settings after configuration or flag overrides.
func (c *Config) ValidatePlayer() error {
	if c.MPVStartupTimeout <= 0 {
		return fmt.Errorf("mpv_startup_timeout must be positive")
	}
	if c.MPVStopTimeout <= 0 {
		return fmt.Errorf("mpv_stop_timeout must be positive")
	}
	return nil
}

// ValidateWebUI checks the optional listener without resolving or binding it.
func (c *Config) ValidateWebUI() error {
	addr := strings.TrimSpace(c.WebUIAddr)
	if addr == "" {
		return nil
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("web_ui_addr must be a host:port address (for example :8081)")
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return fmt.Errorf("web_ui_addr must have a numeric port between 0 and 65535")
	}
	return nil
}

func DefaultConfigPath() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "pepe", "config.yaml")
	}
	return filepath.Join(".", "pepe.yaml")
}

// exampleToken is the placeholder in packaging/linux/config.example.yaml.
const exampleToken = "replace-me"

// WriteConfigTemplate creates a private config file from the example when
// none exists. It never replaces an existing file or symlink.
func WriteConfigTemplate(path string) (created bool, err error) {
	data, err := packaging.Files.ReadFile("linux/config.example.yaml")
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return false, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return false, err
	}
	return true, nil
}

// LoadConfig reads the config file, applies environment overrides and
// validates the result.
func LoadConfig(path string) (*Config, error) {
	cfg, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	cfg.applyEnv()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// ReadConfigFile returns the normalized file values over the defaults, without
// environment overrides or validation. A missing file yields the defaults.
func ReadConfigFile(path string) (*Config, error) {
	if path == "" {
		return parseConfig(nil)
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read config file %s: %w", path, err)
	}
	cfg, err := parseConfig(data)
	if err != nil {
		return nil, fmt.Errorf("parse config file %s: %w", path, err)
	}
	return cfg, nil
}

// parseConfig returns the normalized values of a config file over the defaults.
func parseConfig(data []byte) (*Config, error) {
	cfg := DefaultConfig()
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(cfg); err == io.EOF {
		// An empty document clears the destination; restore defaults.
		cfg = DefaultConfig()
	} else if err != nil {
		return nil, err
	}
	cfg.normalize()
	return cfg, nil
}

func (c *Config) normalize() {
	c.PolochonURL = strings.TrimSpace(c.PolochonURL)
	c.Token = strings.TrimSpace(c.Token)
	c.WebUIAddr = strings.TrimSpace(c.WebUIAddr)
	if c.MPVPath = strings.TrimSpace(c.MPVPath); c.MPVPath == "" {
		c.MPVPath = "mpv"
	}
}

// configEnv maps config keys to the environment variables overriding them.
// An empty variable counts as unset. A whitespace-only connection variable
// still overrides, so validation reports it; a blank POLOCHON_MPV is ignored.
var configEnv = []struct {
	key, name    string
	field        func(*Config) *string
	blankIgnored bool
}{
	{"polochon_url", "POLOCHON_URL", func(c *Config) *string { return &c.PolochonURL }, false},
	{"polochon_token", "POLOCHON_TOKEN", func(c *Config) *string { return &c.Token }, false},
	{"mpv_path", "POLOCHON_MPV", func(c *Config) *string { return &c.MPVPath }, true},
}

// envValue returns the trimmed override from the variable name, if any.
func envValue(name string, blankIgnored bool) (string, bool) {
	raw := os.Getenv(name)
	value := strings.TrimSpace(raw)
	if blankIgnored {
		return value, value != ""
	}
	return value, raw != ""
}

// EnvOverrides returns the config keys overridden by the environment, mapped
// to the variable name.
func EnvOverrides() map[string]string {
	overrides := make(map[string]string)
	for _, env := range configEnv {
		if _, ok := envValue(env.name, env.blankIgnored); ok {
			overrides[env.key] = env.name
		}
	}
	return overrides
}

func (c *Config) applyEnv() {
	for _, env := range configEnv {
		if value, ok := envValue(env.name, env.blankIgnored); ok {
			*env.field(c) = value
		}
	}
}

// Validate checks the required connection settings and every setting group.
func (c *Config) Validate() error {
	if c.PolochonURL == "" {
		return fmt.Errorf("polochon_url is not set (config file or POLOCHON_URL)")
	}
	// An unusable URL must be refused here: after a settings save restarts
	// pépé, a client that cannot be created would exit before any window.
	if u, err := url.Parse(c.PolochonURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("polochon_url must be an http:// or https:// URL")
	}
	if c.Token == "" {
		return fmt.Errorf("polochon_token is not set (config file or POLOCHON_TOKEN)")
	}
	if c.Token == exampleToken {
		return fmt.Errorf("polochon_token still has the example value")
	}
	if err := c.ValidatePlayer(); err != nil {
		return err
	}
	if err := c.ValidateWebUI(); err != nil {
		return err
	}
	return c.ValidateInterface()
}

// SaveConfigFile writes the settings form's values that differ from the file to
// path, so unchanged defaults are not pinned. Comments, other keys and their
// order are kept. A symlink is followed so its target is updated.
// An existing file that does not parse, or an update that would not read back
// as cfg, is never written.
func SaveConfigFile(path string, cfg *Config) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read config file %s: %w", path, err)
	}
	out, err := updateConfigYAML(data, cfg)
	if err != nil {
		return fmt.Errorf("cannot update %s (fix or remove it manually): %w", path, err)
	}
	return writeFileAtomic(path, out)
}

func updateConfigYAML(data []byte, cfg *Config) ([]byte, error) {
	want := *cfg
	want.normalize()
	current, err := parseConfig(data)
	if err != nil {
		return nil, err
	}
	var changed []settingRow
	for _, row := range settingRows {
		if row.get(current) != row.get(&want) {
			changed = append(changed, row)
		}
	}
	out, err := replaceConfigValues(data, &want, changed)
	if err != nil {
		return nil, err
	}
	// Unusual YAML (duplicate keys, merge keys, complex keys) could make the
	// edited file fail to load or keep other values: check it reads back.
	saved, err := parseConfig(out)
	if err != nil {
		return nil, fmt.Errorf("the updated config does not parse: %w", err)
	}
	for _, row := range settingRows {
		if row.get(saved) != row.get(&want) {
			return nil, fmt.Errorf("%s cannot be updated in place", row.key)
		}
	}
	return out, nil
}

// replaceConfigValues sets the rows' keys to cfg's values in the YAML document data.
func replaceConfigValues(data []byte, cfg *Config, rows []settingRow) ([]byte, error) {
	file, err := parser.ParseBytes(data, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var root *ast.MappingNode
	if len(file.Docs) == 1 {
		switch body := file.Docs[0].Body.(type) {
		case *ast.MappingNode:
			root = body
		case nil, *ast.CommentGroupNode:
		default:
			return nil, fmt.Errorf("the config is not a YAML mapping")
		}
	} else if len(file.Docs) > 1 {
		return nil, fmt.Errorf("the config has several YAML documents")
	}
	if root == nil {
		// Missing, empty or comment-only file: write the values afresh.
		var out bytes.Buffer
		out.Write(data)
		if out.Len() > 0 && !bytes.HasSuffix(data, []byte("\n")) {
			out.WriteByte('\n')
		}
		for _, row := range rows {
			line, err := encodeSetting(row.key, row.get(cfg))
			if err != nil {
				return nil, err
			}
			out.Write(line)
		}
		return out.Bytes(), nil
	}
	for _, row := range rows {
		encoded, err := encodeSetting(row.key, row.get(cfg))
		if err != nil {
			return nil, err
		}
		snippet, err := parser.ParseBytes(encoded, 0)
		if err != nil {
			return nil, err
		}
		entry := snippet.Docs[0].Body.(*ast.MappingNode)
		replaced := false
		for _, existing := range root.Values {
			if yamlKeyName(existing.Key) != row.key {
				continue
			}
			if err := replaceYAMLValue(existing, entry.Values[0].Value); err != nil {
				return nil, err
			}
			replaced = true
		}
		if !replaced {
			root.Merge(entry)
		}
	}
	out := file.String()
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return []byte(out), nil
}

// encodeSetting returns the YAML mapping "key: value". go-yaml leaves strings
// such as .inf unquoted, which read back as floats: a string that would not
// read back is double-quoted instead (Go's escapes are valid in YAML).
func encodeSetting(key string, value any) ([]byte, error) {
	out, err := yaml.Marshal(map[string]any{key: value})
	if err != nil {
		return nil, err
	}
	if s, ok := value.(string); ok {
		var back map[string]any
		if yaml.Unmarshal(out, &back) != nil || back[key] != s {
			out = []byte(key + ": " + strconv.Quote(s) + "\n")
		}
	}
	return out, nil
}

// yamlKeyName returns a plain or quoted mapping key's name, ignoring comments
// and the explicit "? key" form.
func yamlKeyName(key ast.MapKeyNode) string {
	if explicit, ok := key.(*ast.MappingKeyNode); ok {
		if name, ok := explicit.Value.(ast.MapKeyNode); ok {
			return yamlKeyName(name)
		}
		return ""
	}
	if name, ok := key.(*ast.StringNode); ok {
		return name.Value
	}
	return ""
}

// replaceYAMLValue replaces entry's value, keeping its anchor so aliases follow
// the new value, and its comments.
func replaceYAMLValue(entry *ast.MappingValueNode, value ast.Node) error {
	comment := entry.Value.GetComment()
	// A comment between the key and a value on the next line belongs to the
	// key; printed before an inline value it would comment it out.
	if keyComment := entry.Key.GetComment(); keyComment != nil {
		if err := entry.Key.SetComment(nil); err != nil {
			return err
		}
		if comment == nil {
			comment = keyComment
		}
	}
	if anchor, ok := entry.Value.(*ast.AnchorNode); ok {
		if comment == nil {
			comment = anchor.Value.GetComment()
		}
		anchor.Value = value
	} else {
		entry.Value = value
	}
	if comment != nil {
		return value.SetComment(comment)
	}
	return nil
}

// writeFileAtomic replaces path with data via a temporary file in the same
// directory. The replaced file's permissions are kept; a new file is private.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.yaml")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, writeErr := tmp.Write(data)
	if err := errors.Join(writeErr, tmp.Chmod(mode), tmp.Sync(), tmp.Close()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
