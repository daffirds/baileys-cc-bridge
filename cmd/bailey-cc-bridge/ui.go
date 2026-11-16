package main

import (
	"context"
	"flag"

	"github.com/daffirds/bailey-cc-bridge/internal/webui"
)

func runUI(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:7468", "listen address (loopback only)")
	noOpen := fs.Bool("no-open", false, "don't open the browser")
	debug := fs.Bool("debug", false, "verbose logs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return webui.Serve(ctx, webui.Options{Addr: *addr, Open: !*noOpen, Log: newLogger(*debug)})
}
