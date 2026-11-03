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
# A complete distribution carries all supported runtimes for future project init.
source_dir=$(CDPATH= cd -- "$(dirname -- "$binary")" && pwd)
bundle_dir=${3:-"$source_dir"}
if [ ! -f "$bundle_dir/ael-toolchain.lock" ] && [ -f "$source_dir/../dist/ael-toolchain.lock" ]; then bundle_dir="$source_dir/../dist"; fi
if [ -f "$bundle_dir/ael-toolchain.lock" ]; then
 read -r marker bundle_version < "$bundle_dir/ael-toolchain.lock"
 [ "$marker" = version ] || { echo 'Invalid distribution lock' >&2; exit 1; }
 case "$bundle_version" in ''|*[!0-9.]*) echo 'Invalid distribution version' >&2; exit 1 ;; esac
 destination="$prefix/share/ael/distribution/$bundle_version"
 mkdir -p "$prefix/share/ael/distribution"
 bundle_stage=$(mktemp -d "$prefix/share/ael/distribution/.ael-bundle.XXXXXX")
 while read -r platform digest asset; do
  [ "$platform" = version ] && continue
  case "$platform" in darwin-arm64|darwin-amd64|linux-amd64|linux-arm64) [ "$asset" = "ael-$platform" ] || exit 1 ;; notice) [ "$asset" = THIRD_PARTY_NOTICES.txt ] || exit 1 ;; *) exit 1 ;; esac
  actual=$(shasum -a 256 "$bundle_dir/$asset" | awk '{print $1}')
  [ "$actual" = "$digest" ] || { echo "Distribution checksum mismatch: $asset" >&2; exit 1; }
  cp "$bundle_dir/$asset" "$bundle_stage/$asset"
 done < "$bundle_dir/ael-toolchain.lock"
 cp "$bundle_dir/ael-toolchain.lock" "$bundle_stage/ael-toolchain.lock"
 if [ -d "$destination" ]; then
  cmp "$bundle_stage/ael-toolchain.lock" "$destination/ael-toolchain.lock" >/dev/null || { echo 'Existing versioned distribution differs; refusing overwrite' >&2; exit 1; }
  for asset_file in "$bundle_stage"/*; do cmp "$asset_file" "$destination/$(basename "$asset_file")" >/dev/null || { echo 'Existing distribution payload differs; refusing overwrite' >&2; exit 1; }; done
  rm -r "$bundle_stage"
 else mv "$bundle_stage" "$destination"; fi
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
