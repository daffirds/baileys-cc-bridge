package whatsapp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/daffirds/baileys-cc-bridge/internal/app"
	"github.com/daffirds/baileys-cc-bridge/sidecar"
)

// SidecarDir is where the embedded sidecar is installed and run from.
func SidecarDir() string { return filepath.Join(app.Home(), "sidecar") }

// install copies the embedded sidecar into SidecarDir and runs `npm ci` there whenever its
// package-lock.json differs from the one last installed (first run, or after an upgrade).
func install(ctx context.Context, log *slog.Logger) (string, error) {
	dir := SidecarDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	for _, name := range []string{"main.ts", "package.json", "package-lock.json"} {
		b, err := sidecar.Files.ReadFile(name)
		if err != nil {
			return "", err
		}
		path := filepath.Join(dir, name)
		if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, b) {
			continue
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return "", err
		}
	}
	lock, _ := sidecar.Files.ReadFile("package-lock.json")
	sum := sha256.Sum256(lock)
	want := hex.EncodeToString(sum[:])
	marker := filepath.Join(dir, "node_modules", ".baileys-cc-bridge-lock")
	if got, err := os.ReadFile(marker); err == nil && string(got) == want {
		return dir, nil
	}
	npm, err := lookTool("npm")
	if err != nil {
		return "", fmt.Errorf("npm not found: install Node.js 22.18 or newer (brew install node)")
	}
	log.Info("installing the WhatsApp sidecar's npm packages (first run or after an update)", "dir", dir)
	cmd := exec.CommandContext(ctx, npm, "ci", "--omit=dev", "--no-audit", "--no-fund", "--loglevel=error")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("npm ci in %s: %w\n%s", dir, err, out)
	}
	if err := os.WriteFile(marker, []byte(want), 0o600); err != nil {
		return "", err
	}
	return dir, nil
}

// lookTool finds node or npm on PATH, then where Homebrew and the usual version managers put them.
func lookTool(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin", filepath.Join(home, ".volta", "bin")} {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}
