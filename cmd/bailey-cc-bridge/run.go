package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/daffirds/bailey-cc-bridge/internal/bridge"
	"github.com/daffirds/bailey-cc-bridge/internal/config"
	"github.com/daffirds/bailey-cc-bridge/internal/tools"
	"github.com/daffirds/bailey-cc-bridge/internal/webui"
	"github.com/daffirds/bailey-cc-bridge/internal/whatsapp"
)

func runRun(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	debug := fs.Bool("debug", false, "verbose logs")
	path := fs.String("config", "", "config file (default: the linked number's config.yaml)")
	addr := fs.String("addr", "127.0.0.1:9999", "where the panel listens (loopback only)")
	open := fs.Bool("open", false, "open the panel in the browser (it opens anyway while no number is linked)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	log := newLogger(*debug)

	// The agents' WhatsApp tools: loopback only, one random-token path per session.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	host := tools.NewHost(log)
	srv := &http.Server{Handler: host, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	a := &account{log: log, debug: *debug, host: host, toolsURL: "http://" + ln.Addr().String(), configPath: *path}
	link, err := webui.Start(ctx, webui.Options{Addr: *addr, Account: a, Log: log})
	if err != nil {
		return err
	}
	fmt.Printf("Panel: %s\n", link)
	if _, linked := whatsapp.Linked(); *open || !linked {
		if !linked {
			fmt.Println("No WhatsApp number is linked yet: link one in the panel.")
		}
		_ = exec.Command("open", link).Start()
	}
	return a.run(ctx)
}

// account keeps this Mac's link to WhatsApp and, while a number is linked, that number's bridge.
// After a logout it starts linking again, so the panel can link another number.
type account struct {
	log        *slog.Logger
	debug      bool
	host       *tools.Host
	toolsURL   string
	configPath string // -config, or "" for the linked number's own

	mu         sync.Mutex
	st         webui.Status
	wa         *whatsapp.Client   // while connected
	stop       context.CancelFunc // ends the current connection
	loggingOut bool
}

// errRelink means the number is gone (logged out): start over and link one.
var errRelink = errors.New("link a number again")

func (a *account) run(ctx context.Context) error {
	for {
		err := a.connect(ctx)
		switch {
		case ctx.Err() != nil:
			return nil
		case errors.Is(err, errRelink):
			continue
		case errors.Is(err, whatsapp.ErrBusy):
			return err
		}
		a.log.Error("WhatsApp connection ended; reconnecting in 5 seconds", "err", err)
		a.update(func(st *webui.Status) { *st = webui.Status{State: "starting", Error: err.Error()} })
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
}

// connect runs one connection to WhatsApp: linking a number if there is none, then the number's
// bridge, until ctx ends, the connection fails or the panel logs out.
func (a *account) connect(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	wa, err := whatsapp.Start(ctx, whatsapp.Options{Debug: a.debug, Log: a.log})
	if err != nil {
		return err
	}
	defer wa.Close()
	me, err := a.waitLinked(ctx, wa)
	if errors.Is(err, whatsapp.ErrLoggedOut) {
		_ = wa.Close()
		return a.forget()
	}
	if err != nil {
		return err
	}
	number := strings.Split(me.ID, "@")[0]
	path := cmp.Or(a.configPath, config.Path(number))
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	a.log.Info("connected to WhatsApp", "account", me.ID, "chats", len(cfg.Chats))
	for _, c := range cfg.Chats {
		a.log.Info("bridging", "chat", c.Name, "jid", c.JID, "engine", c.Engine, "autonomy", c.Autonomy)
	}
	a.mu.Lock()
	a.st = webui.Status{State: "connected", Me: me, ConfigPath: path}
	a.wa, a.stop = wa, cancel
	a.mu.Unlock()

	err = bridge.New(cfg, path, wa, me, a.host, a.toolsURL, a.log).Run(ctx)

	a.mu.Lock()
	out := a.loggingOut
	a.wa, a.stop, a.loggingOut = nil, nil, false
	a.mu.Unlock()
	switch {
	case out:
		return a.logout(context.WithoutCancel(ctx), wa)
	case errors.Is(err, whatsapp.ErrLoggedOut):
		_ = wa.Close()
		return a.forget()
	}
	return err
}

// waitLinked waits for the sidecar to be connected as a linked number, showing its QR codes in
// the panel while there is none.
func (a *account) waitLinked(ctx context.Context, wa *whatsapp.Client) (*whatsapp.Me, error) {
	a.update(func(st *webui.Status) { *st = webui.Status{State: "starting", Error: st.Error} })
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-wa.Done():
			return nil, fmt.Errorf("whatsapp sidecar exited: %v", wa.Err())
		case ev := <-wa.Events():
			switch ev.Type {
			case "qr":
				a.update(func(st *webui.Status) { *st = webui.Status{State: "linking", QR: ev.QR} })
			case "ready":
				return ev.Me, nil
			case "closed":
				return nil, whatsapp.ClosedError(ev.Reason)
			}
		}
	}
}

// logout unlinks this Mac from the number, then forgets the credentials. The number's folder
// stays: linking it again brings its chats back.
func (a *account) logout(ctx context.Context, wa *whatsapp.Client) error {
	a.log.Info("logging out of WhatsApp")
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := wa.Call(ctx, "logout", nil, nil); err != nil {
		a.log.Warn("could not unlink cleanly; remove the device in WhatsApp → Linked devices", "err", err)
	}
	_ = wa.Close()
	return a.forget()
}

// forget deletes the credentials of a number that is no longer linked (the sidecar has stopped),
// so the next connection starts linking.
func (a *account) forget() error {
	if err := os.RemoveAll(whatsapp.AuthDir()); err != nil {
		return err
	}
	a.update(func(st *webui.Status) { *st = webui.Status{State: "starting"} })
	return errRelink
}

func (a *account) update(f func(*webui.Status)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f(&a.st)
}

// Status, Groups and Logout serve the panel.

func (a *account) Status() webui.Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.st
}

func (a *account) Groups(ctx context.Context) ([]whatsapp.Group, error) {
	a.mu.Lock()
	wa := a.wa
	a.mu.Unlock()
	if wa == nil {
		return nil, errors.New("not connected to WhatsApp")
	}
	return wa.Groups(ctx)
}

func (a *account) Logout() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stop == nil || a.loggingOut {
		return
	}
	a.loggingOut = true
	a.st.State = "logging_out"
	a.stop()
}
