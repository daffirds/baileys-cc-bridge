package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeJID(t *testing.T) {
	for in, want := range map[string]string{
		"+62 812-3456-7890":               "6281234567890@s.whatsapp.net",
		"6281234567890:12@s.whatsapp.net": "6281234567890@s.whatsapp.net",
		"120363012345678901@G.US":         "120363012345678901@g.us",
		"123456789012345@lid":             "123456789012345@lid",
	} {
		got, err := NormalizeJID(in)
		if err != nil || got != want {
			t.Errorf("NormalizeJID(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "budi", "@g.us", "12ab34567"} {
		if _, err := NormalizeJID(bad); err == nil {
			t.Errorf("NormalizeJID(%q) accepted", bad)
		}
	}
}

func TestLoadFillsInEngineAndAutonomy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(path, []byte(`
chats:
  - jid: "+62 812 3456 7890"
    name: Me
    autonomy: full
  - jid: 120363012345678901@g.us
    engine: codex
    prompt: only answer questions
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	me, group := c.Chats[0], c.Chats[1]
	if me.JID != "6281234567890@s.whatsapp.net" || me.Engine != EngineClaude || me.Autonomy != AutonomyFull {
		t.Errorf("me = %+v", me)
	}
	if group.Name != "120363012345678901" || group.Engine != EngineCodex || group.Autonomy != AutonomyAuto ||
		group.Prompt != "only answer questions" || !group.IsGroup() {
		t.Errorf("group = %+v", group)
	}
	if c.Chat("x@lid", "120363012345678901@g.us") != &c.Chats[1] {
		t.Error("Chat did not match the second JID form")
	}
}

func TestLoadRejectsUnknownEngine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	_ = os.WriteFile(path, []byte("chats:\n  - jid: 6281234567890\n    engine: gpt\n"), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("accepted engine gpt")
	}
}

func TestSaveKeepsWhatWasWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	in := &Config{
		Chats: []Chat{{JID: "6281234567890@s.whatsapp.net", Name: "Me", Agent: Agent{Engine: EngineCodex, Prompt: "line one\nline two"}}},
	}
	if err := Save(path, in); err != nil {
		t.Fatal(err)
	}
	out, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if c := out.Chats[0]; c.Engine != EngineCodex || c.Prompt != "line one\nline two" || c.Autonomy != "" {
		t.Errorf("chat = %+v", c)
	}
	if err := Save(path, &Config{Chats: []Chat{{JID: "nope"}}}); err == nil {
		t.Error("saved an invalid JID")
	}
}
