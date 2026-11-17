// Package config loads the bridge's YAML config: the whitelist of chats and how each chat's agent
// runs.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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

// Path is where the config lives.
func Path() string { return filepath.Join(app.Home(), "config.yaml") }

type Config struct {
	Chats []Chat `yaml:"chats" json:"chats"`
}

// Agent says how a chat's agent runs. After Load, Engine and Autonomy are always set.
type Agent struct {
	Engine   string `yaml:"engine,omitempty" json:"engine,omitempty"`     // claude (default) | codex
	Model    string `yaml:"model,omitempty" json:"model,omitempty"`       // "" = the CLI's own default
	Effort   string `yaml:"effort,omitempty" json:"effort,omitempty"`     // reasoning effort; "" = the model's default
	Autonomy string `yaml:"autonomy,omitempty" json:"autonomy,omitempty"` // auto (default) | full | readonly
	// Workdir is where the agent runs. "" = the chat's own folder under ~/.bailey-cc-bridge/chats.
	Workdir string `yaml:"workdir,omitempty" json:"workdir,omitempty"`
	// SystemPrompt replaces the bridge's built-in system prompt for this chat; "" keeps the
	// built-in one. {owner}, {number}, {chat}, {chat_kind} and {chat_jid} are filled in.
	SystemPrompt string `yaml:"system_prompt,omitempty" json:"system_prompt,omitempty"`
}

// Chat is one whitelisted chat: a person (…@s.whatsapp.net), a group (…@g.us) or yourself.
type Chat struct {
	JID   string `yaml:"jid" json:"jid"` // a JID, or a phone number such as "+62 812-3456-7890"
	Name  string `yaml:"name,omitempty" json:"name,omitempty"`
	Agent `yaml:",inline"`
}

// IsGroup reports whether the chat is a group.
func (c *Chat) IsGroup() bool { return strings.HasSuffix(c.JID, "@g.us") }

// Load reads and validates the config at path, filling in each chat's engine and autonomy.
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

// Read parses the config at path as written, for editing: empty fields stay empty. A missing file
// reads as an empty config.
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

// Save validates c and writes it to path as given (empty fields stay empty). Comments in an
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
		if ch.Engine == "" {
			ch.Engine = EngineClaude
		}
		if ch.Autonomy == "" {
			ch.Autonomy = AutonomyAuto
		}
		if err := ch.Agent.validate(); err != nil {
			return fmt.Errorf("chat %s: %w", ch.Name, err)
		}
	}
	return nil
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
