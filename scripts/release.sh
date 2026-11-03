#!/usr/bin/env bash
# Cut a release: tag the current main and publish a GitHub release, so that
# `go install github.com/gatogato999/soapviz/cmd/soapviz@latest` picks it up.
#   scripts/release.sh patch|minor|major [--dry-run]
set -euo pipefail
cd "$(dirname "$0")/.."

bump="${1:-}"
dry="${2:-}"
[[ $bump =~ ^(patch|minor|major)$ ]] || { echo "usage: $0 patch|minor|major [--dry-run]" >&2; exit 2; }

run() { if [[ $dry == --dry-run ]]; then echo "+ $*"; else "$@"; fi; }

[[ -z "$(git status --porcelain)" ]] || { echo "working tree is not clean" >&2; exit 1; }
[[ "$(git rev-parse --abbrev-ref HEAD)" == main ]] || { echo "not on main" >&2; exit 1; }
git fetch -q --tags origin
[[ "$(git rev-parse HEAD)" == "$(git rev-parse origin/main)" ]] || { echo "main differs from origin/main: push or pull first" >&2; exit 1; }

# Highest strict vX.Y.Z tag. Pre-releases like v1.0.0-rc1 are ignored, as `go install @latest` ignores them.
last="$(git tag --list 'v*' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -n1 || true)"
last="${last:-v0.0.0}"
IFS=. read -r maj min pat <<<"${last#v}"
case $bump in
  major) maj=$((maj + 1)); min=0; pat=0 ;;
  minor) min=$((min + 1)); pat=0 ;;
  patch) pat=$((pat + 1)) ;;
esac
next="v$maj.$min.$pat"
echo "$last -> $next"

# Never release something that does not build or pass.
go vet ./...
GOOS=js GOARCH=wasm go vet ./cmd/wasm
go test ./...
go build -o /dev/null ./cmd/soapviz

run gh release create "$next" --target main --title "$next" --generate-notes --latest

if [[ $dry != --dry-run ]]; then
  # Check what a user gets. GOPROXY=direct skips the proxy's cache of "latest".
  tmp="$(mktemp -d)"
  if GOBIN="$tmp" GOPROXY=direct go install "github.com/gatogato999/soapviz/cmd/soapviz@$next"; then
    "$tmp/soapviz" -version
  else
    echo "warning: go install of $next failed; check the release on GitHub" >&2
  fi
  rm -rf "$tmp"
fi
