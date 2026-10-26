#!/usr/bin/env bash
# Build the static site (WebAssembly engine + the same UI) into ./dist.
#   scripts/build-pages.sh            -> dist/
#   scripts/build-pages.sh /tmp/site  -> /tmp/site/
set -euo pipefail
cd "$(dirname "$0")/.."

out="${1:-dist}"
theme="${SOAPVIZ_THEME:-dim}"   # default theme for first-time visitors

rm -rf "$out"
mkdir -p "$out/static"

GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o "$out/soapviz.wasm" ./cmd/wasm

# wasm_exec.js must come from the toolchain that built the .wasm.
# It lives in misc/wasm up to Go 1.23 and in lib/wasm from Go 1.24.
root="$(go env GOROOT)"
for p in "$root/lib/wasm/wasm_exec.js" "$root/misc/wasm/wasm_exec.js"; do
  if [ -f "$p" ]; then cp "$p" "$out/wasm_exec.js"; break; fi
done
[ -f "$out/wasm_exec.js" ] || { echo "wasm_exec.js not found under $root" >&2; exit 1; }

cp web/static/*.js web/static/*.css "$out/static/"
cp pages/wasm-boot.js "$out/wasm-boot.js"

# index.html is a Go template with one action ({{.Theme}}); fill it in and
# load the WebAssembly runtime before app.js.
sed -e "s/{{\.Theme}}/$theme/" \
    -e 's#<script src="static/app.js"></script>#<script src="wasm_exec.js"></script>\n<script src="wasm-boot.js"></script>\n<script src="static/app.js"></script>#' \
    web/templates/index.html > "$out/index.html"

if grep -q '{{' "$out/index.html"; then
  echo "unrendered template action left in index.html" >&2
  exit 1
fi
touch "$out/.nojekyll"   # serve files as-is

size() { wc -c < "$1" | awk '{printf "%.1f MB", $1/1048576}'; }
echo "built $out  (soapviz.wasm $(size "$out/soapviz.wasm"), $(gzip -9 -c "$out/soapviz.wasm" | wc -c | awk '{printf "%.1f MB", $1/1048576}') gzipped)"
