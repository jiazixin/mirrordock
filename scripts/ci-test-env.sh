#!/bin/sh
# Prepares a GitHub Actions Linux runner for the Go and E2E suites: the
# mirror fixtures need a reflink-capable temporary directory and a modern
# rsync. Exported values are appended to $GITHUB_ENV for the following steps.
set -eu
: "${GITHUB_ENV:?run this script inside a GitHub Actions job}"

if [ "$(uname -s)" != Linux ]; then
	echo "unsupported runner: $(uname -s)" >&2
	exit 1
fi

# Runner disks are ext4, which cannot clone. Mount a sparse XFS image with
# reflink enabled and point TMPDIR at it.
command -v mkfs.xfs >/dev/null || {
	sudo apt-get update -q
	sudo apt-get install -yq xfsprogs
}
image="${RUNNER_TEMP:?}/reflink.img"
mount_dir=/mnt/mirrordock-reflink
truncate -s 16G "$image"
mkfs.xfs -q -m reflink=1 "$image"
sudo mkdir -p "$mount_dir"
sudo mount -o loop "$image" "$mount_dir"
sudo chown "$(id -u):$(id -g)" "$mount_dir"
echo "TMPDIR=$mount_dir" >>"$GITHUB_ENV"

rsync_bin=$(command -v rsync)
"$rsync_bin" --version | head -n 1
echo "MIRRORDOCK_TEST_RSYNC=$rsync_bin" >>"$GITHUB_ENV"
