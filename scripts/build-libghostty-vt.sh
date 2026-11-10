#!/usr/bin/env bash
# Builds libghostty-vt for the `ghostty` build-tagged tests and prints the
# PKG_CONFIG_PATH assignment they need as the last line of stdout.
# Requires zig 0.17.x and git on PATH; the tests also need pkg-config.
set -euo pipefail

# The stable release, not the newer commit the Go binding's CMakeLists pins:
# the C ABI is the 1.0 contract, and that gap holds no API change.
GHOSTTY_REF="vt-1.0.0"

cache_dir="${GPRT_LIBGHOSTTY_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/gprt/libghostty-vt}"
src="$cache_dir/$GHOSTTY_REF"

case "$(zig version 2>/dev/null || true)" in
0.17.*) ;;
*)
	echo "build-libghostty-vt: zig 0.17.x is required on PATH" >&2
	exit 1
	;;
esac

if [ ! -d "$src/.git" ]; then
	mkdir -p "$cache_dir"
	rm -rf "$src" "$src.tmp"
	git clone --quiet --depth 1 --branch "$GHOSTTY_REF" \
		https://github.com/ghostty-org/ghostty "$src.tmp" >&2
	mv "$src.tmp" "$src"
fi

(cd "$src" && zig build -Demit-lib-vt -Doptimize=ReleaseFast >&2)

pc_file="$(find "$src/zig-out" -name 'libghostty-vt-static.pc' -print -quit)"
if [ -z "$pc_file" ]; then
	echo "build-libghostty-vt: libghostty-vt-static.pc not found under $src/zig-out" >&2
	exit 1
fi

echo "PKG_CONFIG_PATH=$(dirname "$pc_file")${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
