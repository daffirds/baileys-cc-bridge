package whatsapp

import (
	"context"
	"strings"
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
	Forwarded string    `json:"forwarded,omitempty"` // yes | many (WhatsApp's "forwarded many times")
	Mentions  []Person  `json:"mentions,omitempty"`  // @mentioned people; their @tokens in Text use these numbers
	Media     *Media    `json:"media,omitempty"`
	Reaction  *Reaction `json:"reaction,omitempty"`
	Target    string    `json:"target,omitempty"` // edit, revoke: the id of the message changed
}

// Quoted is the message a reply quotes.
type Quoted struct {
	ID         string `json:"id"`
	Sender     string `json:"sender,omitempty"`
	SenderName string `json:"sender_name,omitempty"`
	Text       string `json:"text,omitempty"`
}

// Person is someone on WhatsApp: a phone-number JID when known (an @lid JID otherwise) and the name
// the sidecar has learned for them, if any.
type Person struct {
	JID  string `json:"jid"`
	Name string `json:"name,omitempty"`
}

// Label shows a person as "Name (+number)", "+number", "Name" or, failing those, their JID.
func (p Person) Label() string {
	number := ""
	if user, server, _ := strings.Cut(p.JID, "@"); server == "s.whatsapp.net" {
		number = "+" + user
	}
	switch {
	case p.Name != "" && number != "":
		return p.Name + " (" + number + ")"
	case number != "":
		return number
	case p.Name != "":
		return p.Name
	case p.JID != "":
		return p.JID
	}
	return "someone"
}

// ChatInfo describes a chat: for a group its name, description and members; for a person, who.
type ChatInfo struct {
	JID         string   `json:"jid"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Members     []Member `json:"members,omitempty"`
}

type Member struct {
	Person
	Admin string `json:"admin,omitempty"` // admin | superadmin
}

// ChatInfo fetches a chat's details (a group's member list is always fetched fresh).
func (c *Client) ChatInfo(ctx context.Context, chat string) (*ChatInfo, error) {
	var info ChatInfo
	if err := c.Call(ctx, "chat_info", map[string]string{"chat": chat}, &info); err != nil {
		return nil, err
	}
	return &info, nil
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
