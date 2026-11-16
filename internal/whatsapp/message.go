package whatsapp

import (
	"context"
	"time"
)

// Message is an incoming message, reaction, edit or deletion.
type Message struct {
	ID        string    `json:"id"`
	Chat      string    `json:"chat"`               // phone-number JID of a 1:1 chat when known; …@g.us for a group
	ChatAlt   string    `json:"chat_alt,omitempty"` // the chat's @lid JID, when WhatsApp addressed it that way
	Sender    string    `json:"sender"`
	FromMe    bool      `json:"from_me"` // sent from this account: typed on the phone or another linked device
	PushName  string    `json:"push_name,omitempty"`
	Timestamp int64     `json:"timestamp"`
	Kind      string    `json:"kind"` // text | image | video | audio | document | sticker | location | contact | poll | reaction | edit | revoke | unsupported
	Text      string    `json:"text,omitempty"`
	Quoted    *Quoted   `json:"quoted,omitempty"`
	Media     *Media    `json:"media,omitempty"`
	Reaction  *Reaction `json:"reaction,omitempty"`
	Target    string    `json:"target,omitempty"` // edit, revoke: the id of the message changed
}

// Quoted is the message a reply quotes.
type Quoted struct {
	ID     string `json:"id"`
	Sender string `json:"sender,omitempty"`
	Text   string `json:"text,omitempty"`
}

// Media describes an attachment; Client.Download fetches it.
type Media struct {
	Mimetype string `json:"mimetype,omitempty"`
	Size     int64  `json:"size,omitempty"`
	FileName string `json:"file_name,omitempty"`
	Seconds  int    `json:"seconds,omitempty"`
	Voice    bool   `json:"voice,omitempty"` // a voice note
}

// Reaction is an emoji put on (or, when Emoji is empty, taken off) the message with ID.
type Reaction struct {
	ID    string `json:"id"`
	Emoji string `json:"emoji"`
}

func (m *Message) Time() time.Time { return time.Unix(m.Timestamp, 0) }

// Download saves the media of a recent message to path.
func (c *Client) Download(ctx context.Context, id, path string) error {
	return c.Call(ctx, "download", map[string]string{"id": id, "path": path}, nil)
}
