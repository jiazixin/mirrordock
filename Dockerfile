# Base images are pinned by digest. To update, resolve the new digest of the
# same tag from the official registry and review the change, for example:
#   docker buildx imagetools inspect node:24-bookworm-slim
FROM node:24-bookworm-slim@sha256:a5f4366e834695d5f363b920c104953a535d3a9864d1bdf74566a0ebe01daee5 AS web
WORKDIR /build/web
COPY web/package.json web/package-lock.json ./
# The lockfile pins every package by integrity hash; no install script runs.
RUN npm ci --ignore-scripts
COPY web/ ./
RUN npm run build

FROM golang:1.27.2-bookworm@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61 AS go
ARG VERSION=dev
ARG GOPROXY=https://proxy.golang.org,direct
# Modules are verified against go.sum and the public checksum database even
# when GOPROXY points at a mirror.
ENV GOSUMDB=sum.golang.org GOFLAGS=-mod=readonly
WORKDIR /build
COPY go.mod go.sum ./
RUN GOPROXY="$GOPROXY" go mod download && go mod verify
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/embed.go web/embed.go
COPY --from=web /build/web/dist web/dist
RUN case "$VERSION" in *\"*) echo 'VERSION contains an unsupported double quote' >&2; exit 1 ;; esac \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X \"main.version=${VERSION}\"" -o /mirrordock ./cmd/mirrordock

# rockylinux/rockylinux is published by the Rocky Enterprise Software
# Foundation; the Docker Library rockylinux image is no longer updated.
FROM rockylinux/rockylinux:9@sha256:8101994123cf3d0a8fee517bee7f39e555c7d92bd2d9eb3303cc988a0eeed00f
ARG ROCKY_MIRROR
ARG MIRRORDOCK_UID=10001
RUN if [ -n "$ROCKY_MIRROR" ]; then \
      case "$ROCKY_MIRROR" in \
        https://*) ;; \
        *) echo 'ROCKY_MIRROR must be an https:// URL' >&2; exit 1 ;; \
      esac; \
      case "$ROCKY_MIRROR" in \
        *[!A-Za-z0-9._~:/%+-]*) echo 'ROCKY_MIRROR contains unsupported characters' >&2; exit 1 ;; \
      esac; \
      sed -i -e 's|^mirrorlist=|#mirrorlist=|' \
        -e "s|^#baseurl=http://dl.rockylinux.org/\$contentdir|baseurl=$ROCKY_MIRROR|" \
        /etc/yum.repos.d/rocky*.repo; \
    fi \
    && dnf -y upgrade --security \
    && dnf -y install ca-certificates tzdata rsync dnf-plugins-core shadow-utils \
    && dnf clean all \
    && useradd --system --uid "$MIRRORDOCK_UID" --user-group --home-dir /var/lib/mirrordock --no-create-home mirrordock \
    && install -d -o mirrordock -g mirrordock -m 0750 /etc/mirrordock /var/lib/mirrordock /srv/mirrors
COPY --from=go /mirrordock /usr/local/bin/mirrordock
USER mirrordock
WORKDIR /etc/mirrordock
EXPOSE 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/mirrordock"]
CMD ["--config", "/etc/mirrordock/config.yaml"]
