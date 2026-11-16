// Command bailey-cc-bridge connects whitelisted WhatsApp chats to Claude Code or Codex
// sessions running on this Mac.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"

	"github.com/daffirds/bailey-cc-bridge/internal/app"
)

type command struct {
	name    string
	summary string
	run     func(ctx context.Context, args []string) error
}

var commands = []command{
	{"login", "Link this Mac to your WhatsApp account (QR code, or -phone for a pairing code)", runLogin},
	{"logout", "Unlink this Mac and delete its WhatsApp credentials", runLogout},
	{"version", "Print the version", func(context.Context, []string) error {
		fmt.Println(app.Name, app.Version)
		return nil
	}},
}

func main() {
	if len(os.Args) < 2 || os.Args[1] == "help" || os.Args[1] == "-h" || os.Args[1] == "--help" {
		usage()
		return
	}
	for _, c := range commands {
		if c.name != os.Args[1] {
			continue
		}
		// Ctrl-C and SIGTERM cancel the context so long-running commands can shut down cleanly.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		err := c.run(ctx, os.Args[2:])
		stop()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s %s: %v\n", app.Name, c.name, err)
			os.Exit(1)
		}
		return
	}
	fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
	usage()
	os.Exit(2)
}

func usage() {
	fmt.Fprintf(os.Stderr, "%s bridges whitelisted WhatsApp chats to Claude Code or Codex sessions.\n\n", app.Name)
	fmt.Fprintf(os.Stderr, "Usage: %s <command> [flags]\n\nCommands:\n", app.Name)
	w := tabwriter.NewWriter(os.Stderr, 0, 0, 2, ' ', 0)
	for _, c := range commands {
		fmt.Fprintf(w, "  %s\t%s\n", c.name, c.summary)
	}
	_ = w.Flush()
	fmt.Fprintf(os.Stderr, "\nFiles live in %s (set BAILEY_CC_BRIDGE_HOME to move them).\n", app.Home())
}
