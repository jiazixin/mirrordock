FROM node:24-bookworm-slim AS web
WORKDIR /build/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27.1-bookworm AS go
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY migrations/ migrations/
COPY web/embed.go web/embed.go
COPY --from=web /build/web/dist web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /mirrordock ./cmd/mirrordock

FROM rockylinux:9
RUN dnf -y install ca-certificates tzdata rsync dnf-plugins-core && dnf clean all
COPY --from=go /mirrordock /usr/local/bin/mirrordock
WORKDIR /etc/mirrordock
EXPOSE 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/mirrordock"]
CMD ["--config", "/etc/mirrordock/config.yaml"]
