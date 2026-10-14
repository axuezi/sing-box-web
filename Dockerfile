# syntax=docker/dockerfile:1
# Builds sing-box-web and ships it on top of a sing-box image, so the
# `sing-box check` used to validate changes is the same build that runs.
# Use the image your sing-box actually runs:
#   docker build --build-arg SINGBOX_IMAGE=ghcr.io/sagernet/sing-box:v1.14.3 .
ARG SINGBOX_IMAGE=ghcr.io/sagernet/sing-box:v1.14.3

FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.27-alpine AS build
ARG VERSION=dev
ARG COMMIT=none
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/axuezi/sing-box-web/internal/version.Version=${VERSION} -X github.com/axuezi/sing-box-web/internal/version.Commit=${COMMIT} -X github.com/axuezi/sing-box-web/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      -o /out/sing-box-web ./cmd/sing-box-web

FROM ${SINGBOX_IMAGE}
LABEL org.opencontainers.image.source="https://github.com/axuezi/sing-box-web" \
      org.opencontainers.image.licenses="GPL-3.0-only" \
      org.opencontainers.image.description="Web admin UI for sing-box (bundles sing-box, source: https://github.com/SagerNet/sing-box)"
COPY --from=build /out/sing-box-web /usr/local/bin/sing-box-web
ENV SBW_LISTEN=127.0.0.1:9091 \
    SBW_DATA_DIR=/data \
    SBW_SINGBOX_CONFIG=/etc/sing-box/config.json
VOLUME /data
ENTRYPOINT ["/usr/local/bin/sing-box-web"]
