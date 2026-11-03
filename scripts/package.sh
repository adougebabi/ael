#!/bin/sh
set -eu
export GOCACHE=${GOCACHE:-/tmp/ael-go-cache}
(cd web && npm ci --ignore-scripts && npm run build)
mkdir -p dist
build_id=$(git rev-parse --short HEAD 2>/dev/null || printf dev)
for target in darwin-arm64 linux-amd64; do
 os=${target%-*}
 arch=${target#*-}
 CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "-s -w -X main.build=$build_id" -o "dist/ael-$target" ./cmd/ael
 tar -czf "dist/ael-0.1.0-$target.tar.gz" -C dist "ael-$target" -C .. THIRD_PARTY_NOTICES.txt README.md docs
done
(cd dist && shasum -a 256 ael-darwin-arm64 ael-linux-amd64 *.tar.gz > SHA256SUMS)
