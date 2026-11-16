// Package config loads the bridge's YAML config: defaults for every chat's agent, and the
// whitelist of chats, each of which may override those defaults.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/daffirds/bailey-cc-bridge/internal/app"
)

// Engines.
const (
	EngineClaude = "claude"
	EngineCodex  = "codex"
)

// Autonomy levels, mapped onto each engine's own permission controls.
const (
	AutonomyAuto     = "auto"     // the engine's own reviewer decides (claude auto mode, codex auto_review)
	AutonomyFull     = "full"     // no prompts and no sandbox
	AutonomyReadOnly = "readonly" // read-only; anything that would prompt is denied
)

// DefaultIdleTimeout is how long a chat's CLI process stays up without activity.
const DefaultIdleTimeout = 30 * time.Minute

// Path is where the config lives.
func Path() string { return filepath.Join(app.Home(), "config.yaml") }

type Config struct {
	Defaults Agent  `yaml:"defaults" json:"defaults"`
	Chats    []Chat `yaml:"chats" json:"chats"`
}

// Agent says how a chat's agent runs. After Load, every chat's Agent is complete: fields it
// leaves empty are filled in from the defaults.
type Agent struct {
	Engine   string `yaml:"engine,omitempty" json:"engine,omitempty"`     // claude | codex
	Model    string `yaml:"model,omitempty" json:"model,omitempty"`       // "" = the CLI's own default
	Effort   string `yaml:"effort,omitempty" json:"effort,omitempty"`     // reasoning effort; "" = the model's default
	Autonomy string `yaml:"autonomy,omitempty" json:"autonomy,omitempty"` // auto | full | readonly
	// Workdir is where the agent runs. "" = the chat's own directory under ~/.bailey-cc-bridge/chats.
	Workdir string `yaml:"workdir,omitempty" json:"workdir,omitempty"`
	// Prompt is appended to the bridge's instructions. A chat's prompt replaces the default one.
	Prompt string `yaml:"prompt,omitempty" json:"prompt,omitempty"`
	// IdleTimeout stops the chat's CLI process after this long without activity. The
	// conversation is kept and resumed on the next message.
	IdleTimeout Duration `yaml:"idle_timeout,omitempty" json:"idle_timeout,omitempty"`
}

// Chat is one whitelisted chat: a person (…@s.whatsapp.net), a group (…@g.us) or yourself.
type Chat struct {
	JID   string `yaml:"jid" json:"jid"` // a JID, or a phone number such as "+62 812-3456-7890"
	Name  string `yaml:"name,omitempty" json:"name,omitempty"`
	Agent `yaml:",inline"`
}

// IsGroup reports whether the chat is a group.
func (c *Chat) IsGroup() bool { return strings.HasSuffix(c.JID, "@g.us") }

// Load reads and validates the config at path, filling every chat's settings in from the defaults.
func Load(path string) (*Config, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no config at %s: create it with `%s ui` (or see config.example.yaml)", path, app.Name)
	}
	c, err := Read(path)
	if err != nil {
		return nil, err
	}
	if err := c.normalize(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Read parses the config at path as written, for editing: defaults are not filled in. A missing
// file reads as an empty config.
func Read(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	c, err := parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

func parse(b []byte) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return &c, nil
}

// Save validates c and writes it to path as given (defaults are not filled in). Comments in an
// existing file are not kept.
func Save(path string, c *Config) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return err
	}
	b := buf.Bytes()
	check, err := parse(b)
	if err != nil {
		return err
	}
	if err := check.normalize(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (c *Config) normalize() error {
	d := &c.Defaults
	if d.Engine == "" {
		d.Engine = EngineClaude
	}
	if d.Autonomy == "" {
		d.Autonomy = AutonomyAuto
	}
	if d.IdleTimeout == 0 {
		d.IdleTimeout = Duration(DefaultIdleTimeout)
	}
	if err := d.validate(); err != nil {
		return fmt.Errorf("defaults: %w", err)
	}
	seen := map[string]bool{}
	for i := range c.Chats {
		ch := &c.Chats[i]
		jid, err := NormalizeJID(ch.JID)
		if err != nil {
			return fmt.Errorf("chats[%d]: %w", i, err)
		}
		if seen[jid] {
			return fmt.Errorf("chats[%d]: %s is listed twice", i, jid)
		}
		seen[jid] = true
		ch.JID = jid
		if ch.Name == "" {
			ch.Name, _, _ = strings.Cut(jid, "@")
		}
		ch.Agent = merge(*d, ch.Agent)
		if err := ch.Agent.validate(); err != nil {
			return fmt.Errorf("chat %s: %w", ch.Name, err)
		}
	}
	return nil
}

// merge fills the empty fields of o from d.
func merge(d, o Agent) Agent {
	pick := func(v, def string) string {
		if v != "" {
			return v
		}
		return def
	}
	a := Agent{
		Engine:      pick(o.Engine, d.Engine),
		Model:       pick(o.Model, d.Model),
		Effort:      pick(o.Effort, d.Effort),
		Autonomy:    pick(o.Autonomy, d.Autonomy),
		Workdir:     pick(o.Workdir, d.Workdir),
		Prompt:      pick(o.Prompt, d.Prompt),
		IdleTimeout: o.IdleTimeout,
	}
	if a.IdleTimeout == 0 {
		a.IdleTimeout = d.IdleTimeout
	}
	return a
}

func (a *Agent) validate() error {
	switch a.Engine {
	case EngineClaude, EngineCodex:
	default:
		return fmt.Errorf("unknown engine %q (use claude or codex)", a.Engine)
	}
	switch a.Autonomy {
	case AutonomyAuto, AutonomyFull, AutonomyReadOnly:
	default:
		return fmt.Errorf("unknown autonomy %q (use auto, full or readonly)", a.Autonomy)
	}
	if a.Workdir != "" {
		dir, err := ExpandHome(a.Workdir)
		if err != nil {
			return err
		}
		a.Workdir = dir
	}
	return nil
}

// Chat returns the whitelisted chat with any of these JIDs, or nil. A 1:1 chat can reach the
// bridge under its phone-number JID or its @lid JID, so callers pass both forms they know.
func (c *Config) Chat(jids ...string) *Chat {
	for _, j := range jids {
		if j == "" {
			continue
		}
		for i := range c.Chats {
			if c.Chats[i].JID == j {
				return &c.Chats[i]
			}
		}
	}
	return nil
}

// NormalizeJID accepts a JID or a phone number ("+62 812-3456-7890") and returns the JID without
// a device suffix.
func NormalizeJID(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("jid is empty")
	}
	if user, server, ok := strings.Cut(s, "@"); ok {
		user, _, _ = strings.Cut(user, ":") // 628123:12@s.whatsapp.net → 628123@s.whatsapp.net
		if user == "" || server == "" {
			return "", fmt.Errorf("%q is not a WhatsApp JID", s)
		}
		return user + "@" + strings.ToLower(server), nil
	}
	digits := strings.NewReplacer("+", "", " ", "", "-", "", "(", "", ")", "", ".", "").Replace(s)
	if len(digits) < 7 || len(digits) > 15 || strings.Trim(digits, "0123456789") != "" {
		return "", fmt.Errorf("%q is neither a JID nor a phone number with country code", s)
	}
	return digits + "@s.whatsapp.net", nil
}

// ExpandHome turns a leading ~ into the home directory and makes p absolute.
func ExpandHome(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return filepath.Abs(p)
}

// Duration is a time.Duration written as "30m" or "2h" in YAML and JSON.
type Duration time.Duration

func (d Duration) Std() time.Duration { return time.Duration(d) }

// String drops the zero units Go adds: 30m, 1h, 1h30m, 45s.
func (d Duration) String() string {
	s := time.Duration(d).String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") {
		s = s[:len(s)-2]
	}
	return s
}

func (d *Duration) set(s string) error {
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("bad duration %q (use e.g. 30m or 2h)", s)
	}
	if v < 0 {
		return fmt.Errorf("bad duration %q: negative", s)
	}
	*d = Duration(v)
	return nil
}

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	return d.set(s)
}

func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" {
		*d = 0
		return nil
	}
	return d.set(s)
}

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }
