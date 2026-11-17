// Package sidecar embeds the Node program that holds the WhatsApp connection (Baileys). The bridge
// copies it to ~/.baileys-cc-bridge/sidecar, installs its npm dependencies there and runs it with node,
// which runs TypeScript directly.
package sidecar

import "embed"

//go:embed main.ts package.json package-lock.json
var Files embed.FS
