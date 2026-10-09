FROM node:24-bookworm-slim AS web
WORKDIR /build/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27.2-bookworm AS go
ARG GOPROXY=https://proxy.golang.org,direct
WORKDIR /build
COPY go.mod go.sum ./
RUN GOPROXY="$GOPROXY" go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/embed.go web/embed.go
COPY --from=web /build/web/dist web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /mirrordock ./cmd/mirrordock

FROM rockylinux:9
ARG ROCKY_MIRROR
RUN if [ -n "$ROCKY_MIRROR" ]; then \
      sed -i -e 's|^mirrorlist=|#mirrorlist=|' \
        -e "s|^#baseurl=http://dl.rockylinux.org/\$contentdir|baseurl=$ROCKY_MIRROR|" \
        /etc/yum.repos.d/rocky*.repo; \
    fi \
    && dnf -y upgrade --security \
    && dnf -y install ca-certificates tzdata rsync dnf-plugins-core \
    && dnf clean all
COPY --from=go /mirrordock /usr/local/bin/mirrordock
WORKDIR /etc/mirrordock
EXPOSE 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/mirrordock"]
CMD ["--config", "/etc/mirrordock/config.yaml"]
