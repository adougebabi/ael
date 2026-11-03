#!/bin/sh
set -eu
export GOCACHE=${GOCACHE:-/tmp/ael-go-cache}
(cd web && npm ci --ignore-scripts && npm run build)
mkdir -p dist
build_id=$(git rev-parse --short HEAD 2>/dev/null || printf dev)
if [ -n "$(git status --porcelain 2>/dev/null)" ]; then build_id="$build_id-dirty"; fi
version=0.1.3
printf 'version %s\n' "$version" > dist/ael-toolchain.lock
for target in darwin-arm64 darwin-amd64 linux-amd64 linux-arm64; do
 os=${target%-*}
 arch=${target#*-}
 CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "-s -w -X main.build=$build_id" -o "dist/ael-$target" ./cmd/ael
 digest=$(shasum -a 256 "dist/ael-$target" | awk '{print $1}')
 printf '%s %s ael-%s\n' "$target" "$digest" "$target" >> dist/ael-toolchain.lock
done
cp THIRD_PARTY_NOTICES.txt dist/THIRD_PARTY_NOTICES.txt
digest=$(shasum -a 256 dist/THIRD_PARTY_NOTICES.txt | awk '{print $1}')
printf 'notice %s THIRD_PARTY_NOTICES.txt\n' "$digest" >> dist/ael-toolchain.lock
tar -czf "dist/ael-$version-unix.tar.gz" -C dist ael-darwin-arm64 ael-darwin-amd64 ael-linux-amd64 ael-linux-arm64 ael-toolchain.lock THIRD_PARTY_NOTICES.txt -C .. README.md docs scripts/install.sh scripts/uninstall.sh
(cd dist && shasum -a 256 ael-darwin-arm64 ael-darwin-amd64 ael-linux-amd64 ael-linux-arm64 "ael-$version-unix.tar.gz" > SHA256SUMS)
