// Package app holds the program's name, its version and where it keeps its files.
package app

import (
	"os"
	"path/filepath"
)

const Name = "baileys-cc-bridge"

// Version is set at build time: go build -ldflags "-X github.com/daffirds/baileys-cc-bridge/internal/app.Version=v0.1.0".
var Version = "dev"

// Home is the directory for the WhatsApp credentials, the sidecar and each linked number's data:
// $BAILEYS_CC_BRIDGE_HOME, or ~/.baileys-cc-bridge.
func Home() string {
	if dir := os.Getenv("BAILEYS_CC_BRIDGE_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "." + Name
	}
	return filepath.Join(home, "."+Name)
}

// AccountDir holds everything that belongs to one linked number (its config, conversations and
// chat folders), so linking another number never mixes the two: Home()/<number>.
func AccountDir(number string) string { return filepath.Join(Home(), number) }
