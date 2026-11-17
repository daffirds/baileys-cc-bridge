package bridge

import (
	"context"
	"fmt"
	"html"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/daffirds/baileys-cc-bridge/internal/config"
	"github.com/daffirds/baileys-cc-bridge/internal/whatsapp"
)

// essentials opens every chat's system prompt: where the agent is and how it acts on WhatsApp. The
// bridge relies on it, so it is fixed and the panel doesn't show it. The placeholders are filled in
// for each chat, and the chat's own system prompt follows.
const essentials = `You run unattended on {owner}'s Mac, connected to the WhatsApp chat "{chat}" ({chat_kind}, {chat_jid}) through {owner}'s account (+{number}).

- Nobody sees your text output. Act only through the whatsapp tools (send_message, send_file, send_sticker, react, chat_info); what you send comes from {owner}'s account.
- Every chat message reaches you as a <message> (or <reaction>, <edit>, <deleted>) block, also mid-task. owner="true" marks {owner}; mentions_you="true" means it @mentions this account.
- Attachments are already saved locally: see the file attribute.
- To do something later or on a schedule (a reminder, a check-in, a daily summary), use schedule_job: its task comes back here as a <scheduled_job> block when due.
- Use WhatsApp formatting (*bold* _italic_ ~strike~ ` + "```code```" + `), no Markdown headings or tables.
`

// The system prompts a chat gets unless it sets its own (system_prompt in config.yaml): one for
// groups, one for direct chats, "Message yourself" included.
const (
	DefaultGroupPrompt = `You're a helpful assistant, with a computer to work on when someone asks.

- You're a member of this group: not every message is for you or needs a reply. React freely, and join the discussion when you can help.
- Write like texting: short unless the answer needs more. When a reply covers separate points, lean toward sending each as its own message, e.g. the answer, then a follow-up question.
`
	DefaultDirectPrompt = `You're a helpful assistant, with a computer to work on when someone asks.

- This is a direct chat, so messages are for you unless the person says otherwise. Not every one needs a reply: sometimes a reaction or a sticker is enough.
- Write like texting: short unless the answer needs more. When a reply covers separate points, lean toward sending each as its own message, e.g. the answer, then a follow-up question.
`
)

// instructions is what the chat's agent is told on top of the CLI's own system prompt: the
// essentials, filled in for the chat, then the chat's system prompt or the default for its kind.
func instructions(chat *config.Chat, me *whatsapp.Me, owner string) string {
	kind := "direct chat"
	switch {
	case chat.IsGroup():
		kind = "group"
	case chat.JID == me.ID:
		kind = owner + `'s own "Message yourself" chat`
	}
	prompt := strings.TrimSpace(chat.SystemPrompt)
	if prompt == "" {
		prompt = DefaultDirectPrompt
		if chat.IsGroup() {
			prompt = DefaultGroupPrompt
		}
	}
	base := strings.NewReplacer(
		"{owner}", owner, "{number}", user(me.ID), "{chat}", chat.Name, "{chat_kind}", kind, "{chat_jid}", chat.JID,
	).Replace(essentials)
	return base + "\n" + strings.TrimSpace(prompt) + "\n"
}

// render turns a batch of incoming messages into one prompt.
func (s *session) render(batch []*whatsapp.Message) string {
	var parts []string
	for _, m := range batch {
		if p := s.renderOne(m); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "\n\n")
}

func (s *session) renderOne(m *whatsapp.Message) string {
	if m.Kind == "scheduled_job" { // not from WhatsApp: deliverJob wrote the block
		return m.Text
	}
	b := s.b
	from, number, owner := b.sender(m)
	attrs := []string{attr("id", m.ID), attr("from", from)}
	if number != "" {
		attrs = append(attrs, attr("number", number))
	}
	if owner {
		attrs = append(attrs, `owner="true"`)
	}
	attrs = append(attrs, attr("time", m.Time().Format("2006-01-02 15:04")))

	switch m.Kind {
	case "reaction":
		r := m.Reaction
		if r == nil || r.Emoji == "" {
			return "" // a reaction taken off is noise
		}
		attrs = append(attrs, attr("emoji", r.Emoji), attr("to", r.ID))
		attrs = append(attrs, s.target(r.ID)...)
		return "<reaction " + strings.Join(attrs, " ") + "/>"
	case "revoke":
		attrs = append(attrs, attr("message", m.Target))
		attrs = append(attrs, s.target(m.Target)...)
		return "<deleted " + strings.Join(attrs, " ") + "/>"
	case "edit":
		attrs = append(attrs, attr("message", m.Target))
		return "<edit " + strings.Join(attrs, " ") + ">\n" + m.Text + "\n</edit>"
	}

	var body strings.Builder
	if q := m.Quoted; q != nil {
		attrs = append(attrs, attr("reply_to", q.ID))
		who := b.personName(whatsapp.Person{JID: q.Sender, Name: q.SenderName})
		if n, ok := b.notesGet(q.ID); ok {
			who = n.from
		}
		fmt.Fprintf(&body, "<quote from=%q>%s</quote>\n", html.EscapeString(who), q.Text)
	}
	if len(m.Mentions) > 0 {
		var names []string
		for _, p := range m.Mentions {
			names = append(names, b.personName(p))
			if p.JID == b.me.ID {
				attrs = append(attrs, `mentions_you="true"`)
			}
		}
		attrs = append(attrs, attr("mentions", strings.Join(names, ", ")))
	}
	switch m.Forwarded {
	case "yes":
		attrs = append(attrs, `forwarded="true"`)
	case "many":
		attrs = append(attrs, `forwarded="many times"`)
	}
	if m.Kind != "text" {
		kind := m.Kind
		if m.Media != nil && m.Media.Voice {
			kind = "voice note"
		}
		attrs = append(attrs, attr("kind", kind))
	}
	if md := m.Media; md != nil {
		if md.Mimetype != "" {
			attrs = append(attrs, attr("mimetype", md.Mimetype))
		}
		if md.Seconds > 0 {
			attrs = append(attrs, attr("seconds", fmt.Sprint(md.Seconds)))
		}
		if path, err := s.download(m); err != nil {
			attrs = append(attrs, attr("file_error", err.Error()))
		} else {
			attrs = append(attrs, attr("file", path))
		}
	}
	body.WriteString(m.Text)
	return "<message " + strings.Join(attrs, " ") + ">\n" + strings.TrimRight(body.String(), "\n") + "\n</message>"
}

// target describes a message being reacted to or deleted: whose it was and what it said.
func (s *session) target(id string) []string {
	n, ok := s.b.notesGet(id)
	if !ok {
		return nil
	}
	out := []string{attr("of", n.from)}
	if n.text != "" {
		out = append(out, attr("excerpt", n.text))
	}
	return out
}

// personName labels someone, naming this account after its owner.
func (b *Bridge) personName(p whatsapp.Person) string {
	if p.JID == b.me.ID {
		p.Name = b.ownerName()
	}
	return p.Label()
}

func (b *Bridge) notesGet(id string) (note, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.notes.get(id)
}

const maxDownload = 100 << 20

// download saves a message's attachment in the media folder.
func (s *session) download(m *whatsapp.Message) (string, error) {
	if m.Media.Size > maxDownload {
		return "", fmt.Errorf("not downloaded: larger than %d MB", maxDownload>>20)
	}
	name := m.ID + extension(m)
	if m.Media.FileName != "" {
		name = m.ID + "-" + safeName(m.Media.FileName)
	}
	dir, err := s.mediaDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
	defer cancel()
	if err := s.b.wa.Download(ctx, m.ID, path); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("download failed: %w", err)
	}
	return path, nil
}

func extension(m *whatsapp.Message) string {
	if m.Kind == "sticker" {
		return ".webp"
	}
	base, _, _ := strings.Cut(m.Media.Mimetype, ";")
	switch strings.TrimSpace(base) {
	case "image/jpeg":
		return ".jpg"
	case "audio/ogg":
		return ".ogg"
	case "audio/mpeg":
		return ".mp3"
	case "video/mp4":
		return ".mp4"
	}
	if exts, _ := mime.ExtensionsByType(base); len(exts) > 0 {
		return exts[0]
	}
	return ".bin"
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func safeName(name string) string {
	name = unsafeChars.ReplaceAllString(name, "_")
	if len(name) > 80 {
		name = name[len(name)-80:]
	}
	return name
}

func attr(k, v string) string { return k + `="` + html.EscapeString(v) + `"` }
