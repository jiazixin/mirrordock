#!/bin/sh
set -eu
repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo"
version=$(sh scripts/version.sh)
# Quote the complete -X argument for the Go linker's flag parser (tags may contain apostrophes).
case "$version" in
  *\"*) printf '%s\n' 'version contains an unsupported double quote' >&2; exit 1 ;;
esac
npm --prefix web run build
mkdir -p bin
go build -trimpath -ldflags "-X \"main.version=$version\"" -o bin/mirrordock ./cmd/mirrordock
