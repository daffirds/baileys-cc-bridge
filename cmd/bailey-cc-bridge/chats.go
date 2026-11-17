package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/daffirds/bailey-cc-bridge/internal/config"
	"github.com/daffirds/bailey-cc-bridge/internal/whatsapp"
)

func runChats(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("chats", flag.ContinueOnError)
	debug := fs.Bool("debug", false, "verbose logs")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: bailey-cc-bridge chats [-debug] [name filter]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	filter := strings.ToLower(strings.Join(fs.Args(), " "))
	wa, me, err := whatsapp.Connect(ctx, whatsapp.Options{Debug: *debug, Log: newLogger(*debug)})
	if err != nil {
		return err
	}
	defer wa.Close()
	groups, err := wa.Groups(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("You (your \"Message yourself\" chat):\n  %s\n\nGroups:\n", me.ID)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	shown := 0
	for _, g := range groups {
		if filter != "" && !strings.Contains(strings.ToLower(g.Name), filter) {
			continue
		}
		members := "members"
		if g.Size == 1 {
			members = "member"
		}
		fmt.Fprintf(w, "  %s\t%s\t%d %s\n", g.JID, g.Name, g.Size, members)
		shown++
	}
	_ = w.Flush()
	if shown == 0 {
		fmt.Println("  (none)")
	}
	fmt.Printf("\nPeople: use their phone number with country code as the jid, e.g. \"+62 812-3456-7890\".\n"+
		"`run` also logs the JID of every chat it ignores. Add chats to %s.\n", config.Path(strings.Split(me.ID, "@")[0]))
	return nil
}
