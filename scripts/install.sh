#!/bin/sh
set -eu
prefix=${1:-"$HOME/.local"}
binary=${2:-"./bin/ael"}
[ -x "$binary" ] || { echo 'Build or unpack an executable AEL distribution first' >&2; exit 1; }
source_dir=$(CDPATH= cd -- "$(dirname -- "$binary")" && pwd)
bundle_dir=${3:-"$source_dir"}
if [ ! -f "$bundle_dir/ael-toolchain.lock" ] && [ -f "$source_dir/../dist/ael-toolchain.lock" ]; then bundle_dir="$source_dir/../dist"; fi
# Installation and future upgrades use the same checked native implementation.
exec "$binary" upgrade --prefix "$prefix" --from "$bundle_dir"
