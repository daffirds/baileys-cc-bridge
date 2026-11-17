package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/daffirds/baileys-cc-bridge/internal/app"
	"github.com/daffirds/baileys-cc-bridge/internal/webui"
	"github.com/daffirds/baileys-cc-bridge/internal/whatsapp"
)

// The bridge runs in the background as a launchd agent of the logged-in user: it starts at login
// and again if it crashes, with no terminal. Its output goes to Home()/bridge.log.

const serviceLabel = app.Name

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist")
}

func logPath() string { return filepath.Join(app.Home(), "bridge.log") }

func launchDomain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

func runInstall(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return err
	}
	if strings.Contains(exe, "go-build") {
		return errors.New("install from a built binary: go build -o ~/.local/bin/baileys-cc-bridge ./cmd/baileys-cc-bridge")
	}
	if err := os.MkdirAll(app.Home(), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath()), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(plistPath(), []byte(plist(exe)), 0o644); err != nil {
		return err
	}

	// Replace a previous install, then start it.
	started := time.Now()
	_ = exec.Command("launchctl", "bootout", launchDomain()+"/"+serviceLabel).Run()
	if out, err := exec.Command("launchctl", "bootstrap", launchDomain(), plistPath()).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(out)))
	}
	fmt.Printf("The bridge now runs in the background, starts when you log in and restarts if it crashes.\nLogs: %s\n", logPath())

	// The panel's link shows up once it is serving.
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		if st, err := os.Stat(webui.LinkFile()); err == nil && st.ModTime().After(started) {
			if b, err := os.ReadFile(webui.LinkFile()); err == nil {
				link := strings.TrimSpace(string(b))
				fmt.Printf("Panel: %s (bookmark it, or open it with `%s ui`)\n", link, app.Name)
				if _, linked := whatsapp.Linked(); linked { // unlinked, the bridge opens it itself
					return exec.Command("open", link).Run()
				}
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(300 * time.Millisecond):
		}
	}
	fmt.Printf("It hasn't started yet. Is `%s run` still going in a terminal? Stop it and this one takes over. See the log.\n", app.Name)
	return nil
}

func runUninstall(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = exec.Command("launchctl", "bootout", launchDomain()+"/"+serviceLabel).Run() // stops it, if running
	if err := os.Remove(plistPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fmt.Printf("The bridge no longer runs in the background. The WhatsApp link, your chats and their conversations stay in %s; `%s install` brings it back.\n", app.Home(), app.Name)
	return nil
}

// plist is the launchd agent that runs `exe run`. launchd starts programs with a bare environment,
// so it carries this shell's PATH (agents run git, node, brew tools…) and locale.
func plist(exe string) string {
	env := map[string]string{"PATH": cleanPath(os.Getenv("PATH")), "LANG": cmp.Or(os.Getenv("LANG"), "en_US.UTF-8")}
	for _, k := range []string{"SHELL", "BAILEYS_CC_BRIDGE_HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		if v := os.Getenv(k); v != "" {
			env[k] = v
		}
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var vars strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&vars, "\t\t<key>%s</key>\n\t\t<string>%s</string>\n", k, html.EscapeString(env[k]))
	}
	e := html.EscapeString
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + serviceLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + e(exe) + `</string>
		<string>run</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
` + vars.String() + `	</dict>
	<key>RunAtLoad</key>
	<true/>
	<!-- Restart after a crash, not after a clean stop (uninstall, logout). -->
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<!-- Time to stop the agents and close WhatsApp cleanly. -->
	<key>ExitTimeOut</key>
	<integer>30</integer>
	<key>StandardOutPath</key>
	<string>` + e(logPath()) + `</string>
	<key>StandardErrorPath</key>
	<string>` + e(logPath()) + `</string>
</dict>
</plist>
`
}

// cleanPath drops empty and repeated entries.
func cleanPath(p string) string {
	var out []string
	for _, dir := range filepath.SplitList(p) {
		if dir != "" && !slices.Contains(out, dir) {
			out = append(out, dir)
		}
	}
	return strings.Join(out, string(filepath.ListSeparator))
}
