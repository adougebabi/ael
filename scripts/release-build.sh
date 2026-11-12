#!/bin/sh
set -eu
export GOCACHE=${GOCACHE:-/tmp/ael-go-cache}
platform=${1:-native}
out=${2:-dist/release}
(cd web && npm ci --ignore-scripts && npm run build)
native="$(go env GOOS)-$(go env GOARCH)"
[ "$platform" != native ] || platform="$native"
if [ "$platform" = all ]; then targets="darwin-arm64 darwin-amd64 linux-arm64 linux-amd64"; else targets="$platform linux-${platform#*-} $native"; fi
build_id=$(git rev-parse --short HEAD)
source_digest=$(git ls-files -co --exclude-standard -- internal cmd assets web/src web/package.json web/package-lock.json web/vite.config.ts scripts go.mod go.sum | LC_ALL=C sort -u | while IFS= read -r source_file; do if [ -f "$source_file" ]; then shasum -a 256 "$source_file"; fi; done | shasum -a 256 | cut -c 1-12)
mkdir -p "$out"
stage=$(mktemp -d "${TMPDIR:-/tmp}/ael-release-build.XXXXXX")
trap 'rm -rf "$stage"' EXIT HUP INT TERM
for target in $(printf '%s\n' $targets | sort -u); do
 case "$target" in darwin-arm64|darwin-amd64|linux-arm64|linux-amd64) ;; *) echo 'Unsupported release platform' >&2; exit 1;; esac
 CGO_ENABLED=0 GOOS="${target%-*}" GOARCH="${target#*-}" go build -trimpath -ldflags "-s -w -X main.build=$build_id-$source_digest" -o "$stage/ael-$target" ./cmd/ael
done
python3 scripts/release_package.py --binaries "$stage" --out "$out" --platform "$platform" --publication "${RELEASE_PUBLICATION:-local_unpublished}"
"$stage/ael-$native" release verify --manifest "$out/manifest.json" --json
