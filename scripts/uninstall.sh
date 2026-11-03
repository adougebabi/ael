#!/bin/sh
set -eu
prefix=${1:-"$HOME/.local"}
target="$prefix/bin/ael"
manifest="$prefix/share/ael/install.sha256"
[ -f "$target" ] && [ -f "$manifest" ] || { echo 'Managed installation not found' >&2; exit 1; }
actual=$(shasum -a 256 "$target" | cut -d ' ' -f 1)
expected=$(cat "$manifest")
[ "$actual" = "$expected" ] || { echo 'Binary changed; refusing removal' >&2; exit 1; }
rm "$target" "$manifest"
printf 'Uninstalled executable only. Project files, runtime data and backups retained.\n'
