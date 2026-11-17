package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daffirds/baileys-cc-bridge/internal/app"
	"github.com/daffirds/baileys-cc-bridge/internal/config"
	"github.com/daffirds/baileys-cc-bridge/internal/whatsapp"
)

// Sender is the part of the WhatsApp client the tools use.
type Sender interface {
	SendText(ctx context.Context, chat, text string, o whatsapp.TextOptions) (string, error)
	SendMedia(ctx context.Context, chat string, m whatsapp.OutMedia) (string, error)
	React(ctx context.Context, id, emoji string) (string, error)
	ChatInfo(ctx context.Context, chat string) (*whatsapp.ChatInfo, error)
}

// Binding ties a tool server to one session's chat.
type Binding struct {
	Chat    string // JID of the session's chat
	Self    string // JID of this account
	Owner   string // the account owner's name
	Workdir string // relative paths resolve here
	WA      Sender
	// Resolve maps a chat name or JID to the JID of a whitelisted chat, or fails.
	Resolve func(chat string) (string, error)
	// LastIncoming returns the id of the latest message received in a chat ("" if none).
	LastIncoming func(chat string) string
	// Sent records what the agent sent, so the bridge never feeds it back as input.
	Sent func(chat, id, text string)
	// Footer goes under every text and caption the agent sends, e.g. "Assisted by Claude"; "" for none.
	Footer string
	// Reminders are the chat's: what set_reminder sets comes back to this chat.
	Reminders Reminders
	Log       *slog.Logger
}

// sign puts the footer under text.
func (b *Binding) sign(text string) string {
	if b.Footer == "" {
		return text
	}
	return text + "\n\n_" + b.Footer + "_"
}

func newServer(b *Binding) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: app.Name, Version: app.Version}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "send_message",
		Description: "Send a text message. WhatsApp formatting: *bold* _italic_ ~strike~ ```code```. " +
			"To @mention someone, list their number in mentions and write @<number> in the text."}, b.sendMessage)
	mcp.AddTool(s, &mcp.Tool{Name: "send_file",
		Description: "Send a local file. JPEG/PNG photos, MP4 videos and audio show inline; anything else goes as a document."}, b.sendFile)
	mcp.AddTool(s, &mcp.Tool{Name: "send_sticker",
		Description: "Send an image (PNG, JPEG, WebP, GIF) as a sticker."}, b.sendSticker)
	mcp.AddTool(s, &mcp.Tool{Name: "react",
		Description: "React to a message with an emoji (default: the latest message in this chat). Empty emoji removes your reaction."}, b.react)
	mcp.AddTool(s, &mcp.Tool{Name: "chat_info",
		Description: "Who is in the chat: a group's name, description and members (names, numbers, admins), or who a direct chat is with.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, b.chatInfo)
	mcp.AddTool(s, &mcp.Tool{Name: "set_reminder",
		Description: "Get a note back in this chat later: at that time it arrives here as a <reminder> block, like a message. " +
			"Give in (from now) or at (local time)."}, b.setReminder)
	mcp.AddTool(s, &mcp.Tool{Name: "list_reminders", Description: "The reminders set in this chat that haven't come due yet.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, b.listReminders)
	mcp.AddTool(s, &mcp.Tool{Name: "cancel_reminder", Description: "Cancel a reminder set in this chat, by id."}, b.cancelReminder)
	return s
}

func text(format string, args ...any) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}}}, nil, nil
}

type messageIn struct {
	Text       string   `json:"text"`
	ReplyTo    string   `json:"reply_to,omitempty" jsonschema:"id of a message to quote"`
	Mentions   []string `json:"mentions,omitempty" jsonschema:"people to @mention, by phone number with country code"`
	MentionAll bool     `json:"mention_all,omitempty" jsonschema:"@mention everyone in the group: all members get notified"`
	Chat       string   `json:"chat,omitempty" jsonschema:"another whitelisted chat (name or JID); default: this chat"`
}

func (b *Binding) sendMessage(ctx context.Context, _ *mcp.CallToolRequest, in messageIn) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(in.Text) == "" {
		return nil, nil, errors.New("text is empty")
	}
	chat, err := b.target(in.Chat)
	if err != nil {
		return nil, nil, err
	}
	if in.MentionAll && !strings.HasSuffix(chat, "@g.us") {
		return nil, nil, errors.New("mention_all only works in groups")
	}
	body, mentions, err := withMentions(in.Text, in.Mentions)
	if err != nil {
		return nil, nil, err
	}
	id, err := b.WA.SendText(ctx, chat, b.sign(body), whatsapp.TextOptions{ReplyTo: in.ReplyTo, Mentions: mentions, MentionAll: in.MentionAll})
	if err != nil {
		return nil, nil, err
	}
	b.Sent(chat, id, body)
	return text("Sent (id %s).", id)
}

// withMentions turns the numbers to mention into JIDs. Someone listed but not written as @<number>
// in the text gets the token appended, so the mention is visible, not only a notification.
func withMentions(text string, numbers []string) (string, []string, error) {
	var jids []string
	for _, n := range numbers {
		jid, err := config.NormalizeJID(n)
		if err != nil {
			return "", nil, fmt.Errorf("mentions: %w", err)
		}
		jids = append(jids, jid)
		if token := "@" + strings.Split(jid, "@")[0]; !strings.Contains(text, token) {
			text += " " + token
		}
	}
	return text, jids, nil
}

type fileIn struct {
	Path       string `json:"path" jsonschema:"local file; relative paths start at your working directory"`
	Caption    string `json:"caption,omitempty"`
	ReplyTo    string `json:"reply_to,omitempty" jsonschema:"id of a message to quote"`
	AsDocument bool   `json:"as_document,omitempty" jsonschema:"send as a document even if it could show inline"`
	Voice      bool   `json:"voice,omitempty" jsonschema:"send an OGG/Opus file as a voice note"`
	Chat       string `json:"chat,omitempty" jsonschema:"another whitelisted chat (name or JID); default: this chat"`
}

func (b *Binding) sendFile(ctx context.Context, _ *mcp.CallToolRequest, in fileIn) (*mcp.CallToolResult, any, error) {
	chat, err := b.target(in.Chat)
	if err != nil {
		return nil, nil, err
	}
	path, err := b.file(in.Path)
	if err != nil {
		return nil, nil, err
	}
	kind, mimetype, err := whatsapp.Classify(path)
	if err != nil {
		return nil, nil, err
	}
	switch {
	case in.Voice:
		kind = whatsapp.KindVoice
	case in.AsDocument:
		kind = whatsapp.KindDocument
	}
	caption := in.Caption
	if strings.TrimSpace(caption) != "" {
		caption = b.sign(caption)
	}
	id, err := b.WA.SendMedia(ctx, chat, whatsapp.OutMedia{
		Path: path, Kind: kind, Mimetype: mimetype, Caption: caption, FileName: filepath.Base(path), ReplyTo: in.ReplyTo,
	})
	if err != nil {
		return nil, nil, err
	}
	b.Sent(chat, id, strings.TrimSpace("["+kind+"] "+in.Caption))
	return text("Sent %s as %s (id %s).", filepath.Base(path), kind, id)
}

type stickerIn struct {
	Path string `json:"path" jsonschema:"local image; relative paths start at your working directory"`
	Chat string `json:"chat,omitempty" jsonschema:"another whitelisted chat (name or JID); default: this chat"`
}

func (b *Binding) sendSticker(ctx context.Context, _ *mcp.CallToolRequest, in stickerIn) (*mcp.CallToolResult, any, error) {
	chat, err := b.target(in.Chat)
	if err != nil {
		return nil, nil, err
	}
	path, err := b.file(in.Path)
	if err != nil {
		return nil, nil, err
	}
	id, err := b.WA.SendMedia(ctx, chat, whatsapp.OutMedia{Path: path, Kind: whatsapp.KindSticker})
	if err != nil {
		return nil, nil, err
	}
	b.Sent(chat, id, "[sticker]")
	return text("Sent sticker (id %s).", id)
}

type reactIn struct {
	Emoji     string `json:"emoji" jsonschema:"one emoji, e.g. 👍; empty removes your reaction"`
	MessageID string `json:"message_id,omitempty" jsonschema:"default: the latest message in this chat"`
}

func (b *Binding) react(ctx context.Context, _ *mcp.CallToolRequest, in reactIn) (*mcp.CallToolResult, any, error) {
	id := in.MessageID
	if id == "" {
		if id = b.LastIncoming(b.Chat); id == "" {
			return nil, nil, errors.New("no message to react to yet")
		}
	}
	rid, err := b.WA.React(ctx, id, strings.TrimSpace(in.Emoji))
	if err != nil {
		return nil, nil, err
	}
	b.Sent(b.Chat, rid, "")
	return text("Reacted to %s.", id)
}

type chatInfoIn struct {
	Chat string `json:"chat,omitempty" jsonschema:"another whitelisted chat (name or JID); default: this chat"`
}

func (b *Binding) chatInfo(ctx context.Context, _ *mcp.CallToolRequest, in chatInfoIn) (*mcp.CallToolResult, any, error) {
	chat, err := b.target(in.Chat)
	if err != nil {
		return nil, nil, err
	}
	info, err := b.WA.ChatInfo(ctx, chat)
	if err != nil {
		return nil, nil, err
	}
	if !strings.HasSuffix(chat, "@g.us") {
		if chat == b.Self {
			return text("%s's own \"Message yourself\" chat (%s).", b.Owner, chat)
		}
		return text("Direct chat with %s (%s).", whatsapp.Person{JID: info.JID, Name: info.Name}.Label(), chat)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s (group, %s)\n", info.Name, chat)
	if d := strings.TrimSpace(info.Description); d != "" {
		fmt.Fprintf(&sb, "Description: %s\n", d)
	}
	fmt.Fprintf(&sb, "%d members:\n", len(info.Members))
	for _, m := range info.Members {
		p := m.Person
		var tags []string
		if p.JID == b.Self {
			p.Name = b.Owner
			tags = append(tags, "this account")
		}
		if m.Admin != "" {
			tags = append(tags, m.Admin)
		}
		line := "- " + p.Label()
		if len(tags) > 0 {
			line += " (" + strings.Join(tags, ", ") + ")"
		}
		sb.WriteString(line + "\n")
	}
	return text("%s", strings.TrimSpace(sb.String()))
}

func (b *Binding) target(chat string) (string, error) {
	if chat == "" {
		return b.Chat, nil
	}
	return b.Resolve(chat)
}

func (b *Binding) file(p string) (string, error) {
	if p == "" {
		return "", errors.New("path is empty")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(b.Workdir, p)
	}
	st, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		return "", fmt.Errorf("%s is a directory", p)
	}
	return p, nil
}
