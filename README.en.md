<p align="center">
  <img src="web/src/assets/logo.svg" width="88" alt="MirrorDock logo">
</p>

<h1 align="center">MirrorDock</h1>

<p align="center">
  Self-hosted package mirror sync and serving — one binary that syncs rsync / DNF / GitHub Release sources and serves them over HTTP(S).
</p>

<p align="center">
  <a href="https://github.com/jiazixin/mirrordock/actions/workflows/ci.yml"><img src="https://github.com/jiazixin/mirrordock/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI"></a>
  <a href="https://github.com/jiazixin/mirrordock/releases/latest"><img src="https://img.shields.io/github/v/release/jiazixin/mirrordock?sort=semver" alt="Release"></a>
  <img src="https://img.shields.io/badge/Go-1.27.2-00ADD8?logo=go&logoColor=white" alt="Go 1.27.2">
  <img src="https://img.shields.io/badge/platform-Linux%20%7C%20macOS-4c8b3f" alt="Linux | macOS">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue" alt="License: Apache-2.0"></a>
</p>

<p align="center">
  <a href="README.md">简体中文</a> · <b>English</b>
</p>

---

MirrorDock is a single Go binary with an embedded Vue 3 admin console for running a private or public software mirror. It drives the system `rsync`, the optional `dnf reposync`, and a native Go downloader for GitHub Release assets. Each sync is verified in a work tree and then published atomically as an immutable *generation*. Mirrors are served directly at `/<dest>/`, the admin console lives at `/admin/`, and the admin API at `/api/`.

> [!IMPORTANT]
> - **Syncing requires a filesystem with reflink support** (XFS with reflink enabled, or APFS on macOS). On ext4, NFS and similar filesystems syncs are refused; there is no fallback to full copies.
> - **The mirror root is public.** Files under `mirror_root` that are not configured sources are still listed and downloadable. Keep private data out of the mirror root.

## Features

- **Three sync types**: rsync (ordered fallback sources, global or per-source proxy), DNF 4 `reposync`, and GitHub Release (assets only, glob filters, bulk-add repositories).
- **Scheduling and control**: manual or cron triggers, per-type concurrency limits, a cumulative timeout budget, automatic retries with backoff, pause / resume / cancel, and automatic resumption of tasks interrupted by a service restart.
- **Safe publishing**: the new generation goes live only after sync and verification succeed; on failure the previous generation keeps serving. Unchanged blocks are shared between generations via reflink.
- **Fast downloads**: HTTP/1.1 sendfile with Range, HEAD and conditional requests. The download hot path never touches the database, logs per chunk, or compresses on the fly.
- **Admin console**: overview timeline, run history with raw logs, Rsync / Reposync / Release analytics, and environment checks including a live reflink probe and free-space report.
- **Notifications**: WeCom, DingTalk, Telegram, Bark and a generic webhook; credentials are stored in YAML as AES-256-GCM ciphertext.
- **YAML is the source of truth**: saves from the console keep comments, check a revision and write atomically; SQLite stores run history.

> The admin console and public directory UI are currently in Simplified Chinese.

## Requirements

| Item | Requirement |
| --- | --- |
| OS | Linux for production; macOS for development and local testing |
| Filesystem | Reflink support on the mirror root; work and published trees on the same filesystem |
| rsync | 3.1 or later with your distribution's security patches (the macOS system rsync is too old) |
| Reposync (optional) | DNF 4 and `dnf-plugins-core`, plus `rpmkeys` when GPG checks are on; DNF5 is not supported |
| Building from source | Go 1.27.2, Node.js ≥ 22.12, npm |

## Quick start

### 1. Download a release

Grab the archive for your platform from [Releases](https://github.com/jiazixin/mirrordock/releases/latest). Builds are provided for linux amd64 / arm64 / ppc64le / s390x / riscv64 / loong64 and darwin amd64 / arm64. Linux targets other than amd64 and arm64 are cross-compiled only and have not been run on real hardware.

```sh
curl -LO https://github.com/jiazixin/mirrordock/releases/download/v1.0.0/mirrordock_1.0.0_linux_amd64.tar.gz
curl -LO https://github.com/jiazixin/mirrordock/releases/download/v1.0.0/checksums.txt
sha256sum --ignore-missing -c checksums.txt
tar -xzf mirrordock_1.0.0_linux_amd64.tar.gz
./mirrordock --version
```

Each archive contains the binary, the LICENSE, both READMEs, the [deployment reference](docs/DEPLOYMENT.md) (Chinese), the annotated [example config](configs/config.example.yaml), and a [systemd unit](deploy/mirrordock.service).

> The macOS builds are ad-hoc signed and not notarized. If Gatekeeper blocks a binary downloaded with a browser, run `xattr -d com.apple.quarantine mirrordock`.

### 2. Write a minimal config and start

Try it first with no sources configured. Relative paths are resolved against the config file's directory.

```sh
cat > config.yaml <<'YAML'
listen: 127.0.0.1
port: 8080
path: ./data            # SQLite, task logs and the notification key
mirror_root: ./mirrors  # mirror root, served publicly
auth:
  username: admin
  password: change-me
sources: []
YAML
mkdir -p mirrors
./mirrordock            # reads ./config.yaml by default; use -f / --config to pick another file
```

Open `http://127.0.0.1:8080/admin/`, sign in with the YAML account, add a source and run it once by hand. The public directory index is at `http://127.0.0.1:8080/`.

> [!WARNING]
> The sources in [config.example.yaml](configs/config.example.yaml) are field examples, including full distribution syncs with cron enabled. Review every upstream and schedule before enabling them; do not test against the full example.

### 3. Build from source

Run from the repository root. The web UI must be built first, because `web/embed.go` embeds `web/dist/` into the binary.

```sh
npm --prefix web ci
sh scripts/build.sh
./bin/mirrordock --version
```

Cross-compile with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 sh scripts/build.sh`.

## Configuration

Every field is documented in [configs/config.example.yaml](configs/config.example.yaml); behavioural details are in [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) (Chinese). The essentials:

- `-f <file>` and `--config <file>` are equivalent. Without either, `config.yaml` in the working directory is read; if it does not exist the program exits instead of creating one.
- Relative paths are resolved against the config file's directory. The database is `<path>/data.sqlite`, and task logs are `<path>/logs/<run-id>/<attempt>.log`.
- Environment variables do not override config fields. Scheduling, the admin console and the public index use the time zone the process starts with; set e.g. `TZ=Asia/Shanghai` in the service environment.
- Changes to `listen`, `port`, `path`, `mirror_root`, `tls`, `auth` and `log` need a restart. Sources, schedules and notifications can be edited in the console and take effect on save.
- The admin password, GitHub token and notification credentials are never returned by read APIs. **Exception:** by the agreed protocol, full GitHub / rsync proxy URLs are echoed back to the signed-in console in plain text; their credentials still never reach run snapshots, audit records or logs.
- Set both `tls.cert` and `tls.key` to serve HTTPS on the same port (HTTP/2 is supported; sendfile is not used over TLS).

## Deployment

### systemd

```sh
sudo useradd --system --home-dir /var/lib/mirrordock --shell /usr/sbin/nologin mirrordock
sudo install -m 0755 mirrordock /usr/local/bin/mirrordock
sudo install -d -o mirrordock -g mirrordock -m 0750 /etc/mirrordock /var/lib/mirrordock /srv/mirrors
# Write /etc/mirrordock/config.yaml with path: /var/lib/mirrordock, mirror_root: /srv/mirrors and a real password
sudo install -m 0644 deploy/mirrordock.service /etc/systemd/system/mirrordock.service
sudo systemctl daemon-reload
sudo systemctl enable --now mirrordock
```

The console writes the YAML back on save, so the config directory must be writable by the service user. Put `/srv/mirrors` on XFS with reflink enabled. Binding ports below 1024 needs extra privileges.

### Docker

```sh
docker build --build-arg VERSION=v1.0.0 -t mirrordock .
docker run --rm --name mirrordock -p 8080:8080 -e TZ=Asia/Shanghai \
  -v /absolute/config-dir:/etc/mirrordock \
  -v /absolute/state-dir:/var/lib/mirrordock \
  -v /absolute/mirror-dir:/srv/mirrors mirrordock
```

The image runs as a non-root user with UID/GID 10001; grant it access to the mounted directories first (e.g. `chown -R 10001:10001 <dir>`). `VERSION` must match the source being built. Build-mirror arguments for networks in mainland China are described in the [deployment reference](docs/DEPLOYMENT.md#容器与-systemd-部署).

### Data and backups

Persistent data is the YAML config, the whole state directory pointed to by `path` (SQLite, `notification.key`, and so on), and the mirror root. Stop the service cleanly, then back up all three together. A backup of only the YAML or only SQLite cannot restore notification credentials. To reset a forgotten password, edit the YAML and restart.

## Development

```sh
npm --prefix web ci
npm --prefix web run format:check
npm --prefix web run build
go vet ./...
go test -race -count=1 ./...
```

E2E and real-rsync tests need a modern rsync and a test binary; see [docs/IMPLEMENTATION.md](docs/IMPLEMENTATION.md#开发验证). On pushes to `main` and on pull requests, [CI](.github/workflows/ci.yml) runs the web build, Go checks and tests, Playwright E2E, and a GoReleaser snapshot build.

## Versioning and releases

- Versions come from Git tags of the form `vMAJOR.MINOR.PATCH` (pre-releases such as `v1.2.0-rc.1`). Go stamps the module version at build time; untagged commits get a pseudo-version, and uncommitted changes add `+dirty`.
- Check the version with `mirrordock --version`. It does not read a config or start the service.
- For source archives without `.git`, inject the matching version explicitly: `go build -trimpath -ldflags='-X main.version=v1.0.0' -o bin/mirrordock ./cmd/mirrordock`.
- Pushing a standard tag triggers the [release workflow](.github/workflows/release.yml). It checks `--version` first, then GoReleaser builds every target and uploads the tar.gz archives and `checksums.txt`.

Details: [docs/IMPLEMENTATION.md](docs/IMPLEMENTATION.md#构建与版本).

## Documentation

The project docs are written in Chinese.

| Document | Contents |
| --- | --- |
| [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) | Deployment and operations reference: config protocol, sync executors, logging/TLS, notifications, backups, downloads and publishing |
| [docs/DESIGN.md](docs/DESIGN.md) | Current architecture and design decisions (including ADR-010 on download performance) |
| [docs/IMPLEMENTATION.md](docs/IMPLEMENTATION.md) | API, source map, build, release, testing and CI |
| [docs/ACCEPTANCE.md](docs/ACCEPTANCE.md) | Acceptance records, review results and unverified items |
| [configs/config.example.yaml](configs/config.example.yaml) | Fully annotated example configuration |

## Project layout

```text
cmd/mirrordock/           CLI entry point
internal/{config,observability,database,sync,runs,mirrors,notifications,app}/
web/                      The only npm/Vite boundary: Vue app, E2E tests, embed.go
configs/                  Annotated example; local instances are ignored
deploy/                   systemd unit
scripts/                  Build, CI test environment, favicon and directory-icon tools
docs/                     Deployment, design, implementation and acceptance docs
.github/workflows/        GitHub Actions CI and tag releases
.goreleaser.yaml          GoReleaser multi-target release config
bin/ data/ .cache/        Ignored local binaries (incl. bin/release/), state and cache
test-results/             Ignored Go/OCR/build reports
```

The directory baseline is maintained only in [目录规范.md](目录规范.md), and contributor rules are in [AGENTS.md](AGENTS.md). The directory exceptions recorded for this project are listed in the [Chinese README](README.md#项目结构).

## Contributing

Issues and pull requests are welcome. Before submitting:

1. Read [AGENTS.md](AGENTS.md) and [目录规范.md](目录规范.md), and place new files according to the directory rules there.
2. Run the checks under “Development” above; for frontend changes also run `npm --prefix web run format:check` and the relevant E2E tests.
3. Test only with temporary directories, isolated ports and controlled upstreams; never run a full sync against real distribution mirrors from a test.

## License

MirrorDock is licensed under the [Apache License 2.0](LICENSE). The Phosphor icons embedded in the public directory page keep their own MIT license (see `internal/mirrors/listing-icons.html`).
