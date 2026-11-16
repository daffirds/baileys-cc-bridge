package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"

	"github.com/daffirds/bailey-cc-bridge/internal/app"
	"github.com/daffirds/bailey-cc-bridge/internal/whatsapp"
)

// maxQRCodes bounds login to about five minutes: WhatsApp shows each code for ~20 seconds.
const maxQRCodes = 15

func runLogin(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	phone := fs.String("phone", "", "link with a pairing code for this phone number (with country code) instead of a QR code")
	debug := fs.Bool("debug", false, "verbose logs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if number, ok := whatsapp.Linked(); ok {
		fmt.Printf("Already linked to +%s. Run `%s logout` first to link another account.\n", number, app.Name)
		return nil
	}
	wa, err := whatsapp.Start(ctx, whatsapp.Options{Phone: *phone, Debug: *debug, Log: newLogger(*debug)})
	if err != nil {
		return err
	}
	defer wa.Close()
	qrDir := filepath.Join(app.Home(), "qr")
	defer os.RemoveAll(qrDir)
	shown := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wa.Done():
			return fmt.Errorf("whatsapp sidecar exited: %v", wa.Err())
		case ev := <-wa.Events():
			switch ev.Type {
			case "qr":
				if shown++; shown > maxQRCodes {
					return errors.New("nobody scanned the QR code; run login again when your phone is at hand")
				}
				if err := showQR(ev.QR, qrDir, shown); err != nil {
					return err
				}
			case "pairing_code":
				fmt.Printf("On your phone: WhatsApp → Settings → Linked devices → Link a device → Link with phone number instead, and enter\n\n    %s\n\n", ev.Code)
			case "ready":
				// Right after pairing, Baileys does not know the account's name yet.
				fmt.Printf("Linked to %s. Finishing the first sync…\n", ev.Me.ID)
				// The phone shows the new device as syncing until the first sync lands; leave it time.
				select {
				case <-ctx.Done():
				case <-time.After(20 * time.Second):
				}
				fmt.Println("Done.")
				return nil
			case "closed":
				return fmt.Errorf("WhatsApp closed the connection: %s", ev.Reason)
			}
		}
	}
}

// showQR prints the QR code in the terminal and opens it as an image in Preview. WhatsApp rotates
// the code every ~20 seconds; every new one gets its own file so Preview always shows a fresh one.
func showQR(code, dir string, n int) error {
	q, err := qrcode.New(code, qrcode.Low)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "\nScan with your phone: WhatsApp → Settings → Linked devices → Link a device")
	fmt.Fprint(os.Stderr, terminalQR(q.Bitmap()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, fmt.Sprintf("qr-%d.png", n))
	if err := q.WriteFile(512, path); err != nil {
		return err
	}
	if err := exec.Command("open", path).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "(could not open %s: %v)\n", path, err)
	}
	return nil
}

// terminalQR draws two rows of modules per line with "▀" in explicit black and white, so the code
// scans whatever the terminal's colours are. The bitmap already includes the quiet zone.
func terminalQR(bits [][]bool) string {
	fg := map[bool]int{true: 30, false: 97}  // dark: black, light: bright white
	bg := map[bool]int{true: 40, false: 107} // the same colours as backgrounds
	var b strings.Builder
	for y := 0; y < len(bits); y += 2 {
		for x := range bits[y] {
			bottom := y+1 < len(bits) && bits[y+1][x]
			fmt.Fprintf(&b, "\x1b[%d;%dm▀", fg[bits[y][x]], bg[bottom])
		}
		b.WriteString("\x1b[0m\n")
	}
	return b.String()
}

func runLogout(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	debug := fs.Bool("debug", false, "verbose logs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, ok := whatsapp.Linked(); ok {
		if err := unlink(ctx, *debug); err != nil {
			fmt.Fprintf(os.Stderr, "Could not unlink cleanly (%v); remove the device in WhatsApp → Linked devices.\n", err)
		}
	}
	if err := os.RemoveAll(whatsapp.AuthDir()); err != nil {
		return err
	}
	fmt.Println("Logged out and removed the WhatsApp credentials.")
	return nil
}

// unlink connects and asks WhatsApp to remove this device from the account.
func unlink(ctx context.Context, debug bool) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	wa, _, err := whatsapp.Connect(ctx, whatsapp.Options{Debug: debug, Log: newLogger(debug)})
	if err != nil {
		return err
	}
	defer wa.Close()
	return wa.Call(ctx, "logout", nil, nil)
}
