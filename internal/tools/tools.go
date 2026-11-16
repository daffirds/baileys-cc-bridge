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

	"github.com/daffirds/bailey-cc-bridge/internal/app"
	"github.com/daffirds/bailey-cc-bridge/internal/whatsapp"
)

// Sender is the part of the WhatsApp client the tools use.
type Sender interface {
	SendText(ctx context.Context, chat, text, replyTo string) (string, error)
	SendMedia(ctx context.Context, chat string, m whatsapp.OutMedia) (string, error)
	React(ctx context.Context, id, emoji string) (string, error)
}

// Binding ties a tool server to one session's chat.
type Binding struct {
	Chat    string // JID of the session's chat
	Workdir string // relative paths resolve here
	WA      Sender
	// Resolve maps a chat name or JID to the JID of a whitelisted chat, or fails.
	Resolve func(chat string) (string, error)
	// LastIncoming returns the id of the latest message received in a chat ("" if none).
	LastIncoming func(chat string) string
	// Sent records what the agent sent, so the bridge never feeds it back as input.
	Sent func(chat, id, text string)
	Log  *slog.Logger
}

func newServer(b *Binding) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: app.Name, Version: app.Version}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "send_message",
		Description: "Send a text message. WhatsApp formatting: *bold* _italic_ ~strike~ ```code```."}, b.sendMessage)
	mcp.AddTool(s, &mcp.Tool{Name: "send_file",
		Description: "Send a local file. JPEG/PNG photos, MP4 videos and audio show inline; anything else goes as a document."}, b.sendFile)
	mcp.AddTool(s, &mcp.Tool{Name: "send_sticker",
		Description: "Send an image (PNG, JPEG, WebP, GIF) as a sticker."}, b.sendSticker)
	mcp.AddTool(s, &mcp.Tool{Name: "react",
		Description: "React to a message with an emoji (default: the latest message in this chat). Empty emoji removes your reaction."}, b.react)
	return s
}

func text(format string, args ...any) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}}}, nil, nil
}

type messageIn struct {
	Text    string `json:"text"`
	ReplyTo string `json:"reply_to,omitempty" jsonschema:"id of a message to quote"`
	Chat    string `json:"chat,omitempty" jsonschema:"another whitelisted chat (name or JID); default: this chat"`
}

func (b *Binding) sendMessage(ctx context.Context, _ *mcp.CallToolRequest, in messageIn) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(in.Text) == "" {
		return nil, nil, errors.New("text is empty")
	}
	chat, err := b.target(in.Chat)
	if err != nil {
		return nil, nil, err
	}
	id, err := b.WA.SendText(ctx, chat, in.Text, in.ReplyTo)
	if err != nil {
		return nil, nil, err
	}
	b.Sent(chat, id, in.Text)
	return text("Sent (id %s).", id)
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
	id, err := b.WA.SendMedia(ctx, chat, whatsapp.OutMedia{
		Path: path, Kind: kind, Mimetype: mimetype, Caption: in.Caption, FileName: filepath.Base(path), ReplyTo: in.ReplyTo,
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
