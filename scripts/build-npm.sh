#!/bin/sh
# Builds dist/baileys-cc-bridge, the binary the npm package ships: one macOS executable for Apple
# Silicon and Intel, versioned from package.json. npm runs it before packing and publishing.
set -eu
cd "$(dirname "$0")/.."
version="v$(node -p 'require("./package.json").version')"
rm -rf dist
mkdir dist
for arch in arm64 amd64; do
	GOOS=darwin GOARCH="$arch" CGO_ENABLED=0 go build -trimpath \
		-ldflags "-s -w -X github.com/daffirds/baileys-cc-bridge/internal/app.Version=$version" \
		-o "dist/baileys-cc-bridge-$arch" ./cmd/baileys-cc-bridge
done
lipo -create -output dist/baileys-cc-bridge dist/baileys-cc-bridge-arm64 dist/baileys-cc-bridge-amd64
rm dist/baileys-cc-bridge-arm64 dist/baileys-cc-bridge-amd64
echo "dist/baileys-cc-bridge $version ($(lipo -archs dist/baileys-cc-bridge))"
