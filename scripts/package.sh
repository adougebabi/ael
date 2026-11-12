#!/bin/sh
set -eu
export GOCACHE=${GOCACHE:-/tmp/ael-go-cache}
(cd web && npm ci --ignore-scripts && npm run build)
mkdir -p dist
build_id=$(git rev-parse --short HEAD 2>/dev/null || printf dev)
source_digest=$(git ls-files -co --exclude-standard -- internal cmd assets web/src web/package.json web/package-lock.json web/vite.config.ts scripts go.mod go.sum | LC_ALL=C sort -u | while IFS= read -r source_file; do if [ -f "$source_file" ]; then shasum -a 256 "$source_file"; fi; done | shasum -a 256 | cut -c 1-12)
build_id="$build_id-$source_digest"
version=$(sed -n 's/^const Version = "\(.*\)"/\1/p' internal/bootstrap/bootstrap.go)
[ -n "$version" ] || { echo "Missing source version" >&2; exit 1; }
printf 'version %s\n' "$version" > dist/ael-toolchain.lock
for target in darwin-arm64 darwin-amd64 linux-amd64 linux-arm64; do
 os=${target%-*}
 arch=${target#*-}
 CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "-s -w -X main.build=$build_id" -o "dist/ael-$target" ./cmd/ael
 digest=$(shasum -a 256 "dist/ael-$target" | awk '{print $1}')
 printf '%s %s ael-%s\n' "$target" "$digest" "$target" >> dist/ael-toolchain.lock
done
native_platform="$(go env GOOS)-$(go env GOARCH)"
"./dist/ael-$native_platform" version --json | rg -q "\"version\":\"$version\"" || { echo 'Binary/lock version mismatch' >&2; exit 1; }
cp THIRD_PARTY_NOTICES.txt dist/THIRD_PARTY_NOTICES.txt
digest=$(shasum -a 256 dist/THIRD_PARTY_NOTICES.txt | awk '{print $1}')
printf 'notice %s THIRD_PARTY_NOTICES.txt\n' "$digest" >> dist/ael-toolchain.lock
tar -czf "dist/ael-$version-unix.tar.gz" -C dist ael-darwin-arm64 ael-darwin-amd64 ael-linux-amd64 ael-linux-arm64 ael-toolchain.lock THIRD_PARTY_NOTICES.txt -C .. README.md docs scripts/install.sh scripts/uninstall.sh scripts/epic_tree_demo.py scripts/plan_proposal_demo.py scripts/sequential_work_demo.py
(cd dist && shasum -a 256 ael-darwin-arm64 ael-darwin-amd64 ael-linux-amd64 ael-linux-arm64 "ael-$version-unix.tar.gz" > SHA256SUMS)

python3 scripts/release_package.py --binaries dist --out dist/release --platform all --publication "${RELEASE_PUBLICATION:-local_unpublished}"
"./dist/ael-$native_platform" release verify --manifest dist/release/manifest.json --json
