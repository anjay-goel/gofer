#!/bin/sh
set -eu

repo="anjay-goel/gofer"
base="${GOFER_DOWNLOAD_URL:-https://github.com/$repo/releases/latest/download}"
dir="${GOFER_INSTALL_DIR:-$HOME/.local/bin}"

fail() {
	echo "gofer: $1" >&2
	exit 1
}

case "$(uname -s)" in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) fail "unsupported system $(uname -s)" ;;
esac

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) fail "unsupported CPU $(uname -m)" ;;
esac

command -v curl >/dev/null 2>&1 || fail "curl is needed to install gofer"

file="gofer-$os-$arch"
start="$PWD"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

curl -fsSL "$base/$file" -o "$tmp/$file" || fail "couldn't download $file"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt" || fail "couldn't download checksums.txt"

cd "$tmp"
grep " $file\$" checksums.txt >expected || fail "no checksum for $file"
if command -v sha256sum >/dev/null 2>&1; then
	sha256sum -c expected >/dev/null || fail "checksum mismatch for $file"
else
	shasum -a 256 -c expected >/dev/null || fail "checksum mismatch for $file"
fi

mkdir -p "$dir"
chmod 755 "$file"
mv -f "$file" "$dir/gofer"

echo "Installed $("$dir/gofer" version) to $dir/gofer"
case ":$PATH:" in
*":$dir:"*) ;;
*) echo "Add $dir to your PATH" ;;
esac
cd "$start"

if ! { : </dev/tty; } 2>/dev/null; then
	echo "Next: gofer init"
	exit 0
fi

printf "Set up a dev box now? [Y/n] " >/dev/tty
read -r answer </dev/tty || answer=n
case "$answer" in
"" | y | Y | yes) "$dir/gofer" init </dev/tty >/dev/tty 2>&1 ;;
*) echo "Next: gofer init" ;;
esac
