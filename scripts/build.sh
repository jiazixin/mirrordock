#!/bin/sh
set -eu
repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo"
npm --prefix web run build
mkdir -p bin
go build -trimpath -o bin/mirrordock ./cmd/mirrordock
