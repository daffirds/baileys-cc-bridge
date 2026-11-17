package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/daffirds/baileys-cc-bridge/internal/webui"
)

// runUI opens the panel of the running bridge.
func runUI(context.Context, []string) error {
	b, err := os.ReadFile(webui.LinkFile())
	if err != nil {
		return errors.New("the bridge is not running: `baileys-cc-bridge install` keeps it running in the background (or `run` in a terminal)")
	}
	link := strings.TrimSpace(string(b))
	fmt.Println(link)
	return exec.Command("open", link).Run()
}
