#!/bin/sh
set -eu
repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
# Source archives have no Git metadata. Never pick up a parent repository.
root=$(git -C "$repo" rev-parse --show-toplevel 2>/dev/null || true)
if [ "$root" != "$repo" ]; then
  printf '%s\n' dev
  exit 0
fi
version=$(git -C "$repo" describe --tags --exact-match HEAD 2>/dev/null || git -C "$repo" rev-parse --short=12 HEAD 2>/dev/null || printf '%s' dev)
printf '%s\n' "$version"
