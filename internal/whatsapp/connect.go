package whatsapp

import (
	"context"
	"errors"
	"fmt"
)

// Connect starts the sidecar and waits until it is connected to WhatsApp as the linked account.
func Connect(ctx context.Context, o Options) (*Client, *Me, error) {
	if _, ok := Linked(); !ok {
		return nil, nil, errors.New("this Mac is not linked to WhatsApp yet: run `bailey-cc-bridge login`")
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
				return nil, nil, errors.New("WhatsApp no longer accepts this Mac's credentials: run `bailey-cc-bridge logout`, then `login`")
			case "closed":
				_ = c.Close()
				return nil, nil, ClosedError(ev.Reason)
			}
		}
	}
}

// ClosedError explains why WhatsApp ended the connection for good.
func ClosedError(reason string) error {
	switch reason {
	case "logged_out":
		return errors.New("WhatsApp logged this Mac out (the device was unlinked): run `bailey-cc-bridge logout`, then `login`")
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

// Groups lists the groups this account is in, and remembers them for CachedGroups.
func (c *Client) Groups(ctx context.Context) ([]Group, error) {
	var res struct {
		Groups []Group `json:"groups"`
	}
	if err := c.Call(ctx, "chats", nil, &res); err != nil {
		return nil, err
	}
	saveGroups(res.Groups)
	return res.Groups, nil
}
