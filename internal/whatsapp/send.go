package whatsapp

import (
	"context"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Outgoing media kinds.
const (
	KindImage    = "image"
	KindVideo    = "video"
	KindAudio    = "audio"
	KindVoice    = "voice" // a voice note (OGG/Opus)
	KindDocument = "document"
	KindSticker  = "sticker"
)

// OutMedia is a local file to send.
type OutMedia struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Mimetype string `json:"mimetype,omitempty"`
	Caption  string `json:"caption,omitempty"`
	FileName string `json:"file_name,omitempty"`
	ReplyTo  string `json:"reply_to,omitempty"`
}

type sent struct {
	ID string `json:"id"`
}

// SendText sends text to chat, quoting the recent message replyTo if set. It returns the new
// message's id.
func (c *Client) SendText(ctx context.Context, chat, text, replyTo string) (string, error) {
	var res sent
	err := c.Call(ctx, "send_text", map[string]string{"chat": chat, "text": text, "reply_to": replyTo}, &res)
	return res.ID, err
}

// SendMedia sends a local file to chat and returns the new message's id.
func (c *Client) SendMedia(ctx context.Context, chat string, m OutMedia) (string, error) {
	var res sent
	err := c.Call(ctx, "send_media", struct {
		Chat string `json:"chat"`
		OutMedia
	}{chat, m}, &res)
	return res.ID, err
}

// React puts emoji on a recent message; "" takes this account's reaction off again. It returns the
// reaction's own message id.
func (c *Client) React(ctx context.Context, id, emoji string) (string, error) {
	var res sent
	err := c.Call(ctx, "react", map[string]string{"id": id, "emoji": emoji}, &res)
	return res.ID, err
}

// Classify picks how to send a file: as a photo, video or audio clip when WhatsApp can show it
// inline, as a document otherwise. It also returns the file's MIME type.
func Classify(path string) (kind, mimetype string, err error) {
	mimetype = mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if mimetype == "" {
		f, err := os.Open(path)
		if err != nil {
			return "", "", err
		}
		head := make([]byte, 512)
		n, _ := f.Read(head)
		f.Close()
		mimetype = http.DetectContentType(head[:n])
	}
	base, _, _ := strings.Cut(mimetype, ";")
	switch base {
	case "image/jpeg", "image/png":
		return KindImage, base, nil
	case "video/mp4", "video/3gpp":
		return KindVideo, base, nil
	}
	if strings.HasPrefix(base, "audio/") {
		return KindAudio, base, nil
	}
	return KindDocument, base, nil
}
