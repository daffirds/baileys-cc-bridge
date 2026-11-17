package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/daffirds/bailey-cc-bridge/internal/bridge"
	"github.com/daffirds/bailey-cc-bridge/internal/config"
	"github.com/daffirds/bailey-cc-bridge/internal/tools"
	"github.com/daffirds/bailey-cc-bridge/internal/whatsapp"
)

func runRun(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	debug := fs.Bool("debug", false, "verbose logs")
	path := fs.String("config", "", "config file (default: the linked number's config.yaml)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	log := newLogger(*debug)
	number, ok := whatsapp.Linked()
	if !ok {
		return errors.New("this Mac is not linked to WhatsApp yet: run `bailey-cc-bridge login`")
	}
	if *path == "" {
		*path = config.Path(number)
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	if len(cfg.Chats) == 0 {
		return fmt.Errorf("no chats in %s: add some with `bailey-cc-bridge ui`", *path)
	}

	// The agents' WhatsApp tools: loopback only, one random-token path per session.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	host := tools.NewHost(log)
	srv := &http.Server{Handler: host, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	wa, me, err := whatsapp.Connect(ctx, whatsapp.Options{Debug: *debug, Log: log})
	if err != nil {
		return err
	}
	defer wa.Close()
	log.Info("connected to WhatsApp", "account", me.ID)
	// Refresh the group list the config UI offers: it can't connect itself while this runs.
	go func() {
		if _, err := wa.Groups(ctx); err != nil {
			log.Debug("could not list groups", "err", err)
		}
	}()
	for _, c := range cfg.Chats {
		log.Info("bridging", "chat", c.Name, "jid", c.JID, "engine", c.Engine, "autonomy", c.Autonomy)
	}
	return bridge.New(cfg, *path, wa, me, host, "http://"+ln.Addr().String(), log).Run(ctx)
}
