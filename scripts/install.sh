#!/bin/sh
set -eu
prefix=${1:-"$HOME/.local"}
binary=${2:-"./bin/ael"}
target="$prefix/bin/ael"
manifest="$prefix/share/ael/install.sha256"
[ -f "$binary" ] || { echo 'Build bin/ael first' >&2; exit 1; }
if [ -e "$target" ]; then
 [ -f "$manifest" ] || { echo 'Unknown existing binary; refusing overwrite' >&2; exit 1; }
 actual=$(shasum -a 256 "$target" | cut -d ' ' -f 1)
 expected=$(cat "$manifest")
 [ "$actual" = "$expected" ] || { echo 'Existing binary changed; preserve and review manually' >&2; exit 1; }
fi
mkdir -p "$prefix/bin" "$prefix/share/ael"
if [ -f "$target" ]; then cp "$target" "$prefix/share/ael/ael.previous"; fi
stage=$(mktemp "$prefix/bin/.ael-install.XXXXXX")
trap 'rm -f "$stage"' EXIT
cp "$binary" "$stage"
chmod 755 "$stage"
mv "$stage" "$target"
shasum -a 256 "$target" | cut -d ' ' -f 1 > "$manifest"
printf 'Installed %s; project assets require separate ael init/refresh.\n' "$target"
