package whatsapp

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/daffirds/baileys-cc-bridge/internal/app"
)

// ErrLoggedOut means WhatsApp unlinked this Mac: the device was removed on the phone, or a logout.
var ErrLoggedOut = errors.New("WhatsApp logged this Mac out (the device was unlinked)")

// Connect starts the sidecar and waits until it is connected to WhatsApp as the linked account.
func Connect(ctx context.Context, o Options) (*Client, *Me, error) {
	if _, ok := Linked(); !ok {
		return nil, nil, errors.New("this Mac is not linked to WhatsApp yet: run `baileys-cc-bridge login`")
	}
	c, err := Start(ctx, o)
	if err != nil {
		return nil, nil, err
	}
	for {
		select {
		case <-ctx.Done():
			_ = c.Close()
			return nil, nil, ctx.Err()
		case <-c.Done():
			return nil, nil, fmt.Errorf("whatsapp sidecar exited: %v", c.Err())
		case ev := <-c.Events():
			switch ev.Type {
			case "ready":
				return c, ev.Me, nil
			case "qr":
				_ = c.Close()
				return nil, nil, errors.New("WhatsApp no longer accepts this Mac's credentials: run `baileys-cc-bridge logout`, then `login`")
			case "closed":
				_ = c.Close()
				err := ClosedError(ev.Reason)
				if errors.Is(err, ErrLoggedOut) {
					err = fmt.Errorf("%w: run `baileys-cc-bridge logout`, then link again", err)
				}
				return nil, nil, err
			}
		}
	}
}

// ClosedError explains why WhatsApp ended the connection for good.
func ClosedError(reason string) error {
	switch reason {
	case "logged_out":
		return ErrLoggedOut
	case "forbidden":
		return errors.New("WhatsApp refused the connection (403): the number may be banned or restricted")
	}
	return fmt.Errorf("WhatsApp closed the connection: %s", reason)
}

// Group is a group this account is in.
type Group struct {
	JID  string `json:"jid"`
	Name string `json:"name"`
	Size int    `json:"size"`
}

// Groups lists the groups this account is in, by name.
func (c *Client) Groups(ctx context.Context) ([]Group, error) {
	var res struct {
		Groups []Group `json:"groups"`
	}
	if err := c.Call(ctx, "chats", nil, &res); err != nil {
		return nil, err
	}
	slices.SortFunc(res.Groups, func(a, b Group) int { return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return res.Groups, nil
}

// People are the people a linked number knows by name (the sidecar's names.json: saved contacts
// and everyone seen in a chat), by name. Only phone-number JIDs: those are what a chat's jid takes.
func People(number string) []Person {
	var stored struct {
		Names map[string]string `json:"names"`
	}
	if b, err := os.ReadFile(filepath.Join(app.AccountDir(number), "names.json")); err == nil {
		_ = json.Unmarshal(b, &stored)
	}
	people := []Person{}
	for jid, name := range stored.Names {
		if strings.HasSuffix(jid, "@s.whatsapp.net") && name != "" && !strings.HasPrefix(jid, number+"@") {
			people = append(people, Person{JID: jid, Name: name})
		}
	}
	slices.SortFunc(people, func(a, b Person) int { return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return people
}
