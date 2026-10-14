VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG     := github.com/axuezi/sing-box-web/internal/version
LDFLAGS := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)
BIN     := bin/sing-box-web
TESTFLAGS ?=

.PHONY: all web build test vet check dev-api dev-web clean

all: web build

web/node_modules: web/package-lock.json
	cd web && npm ci
	@touch $@

## web: build the frontend into web/dist (embedded by `build`)
web: web/node_modules
	cd web && npm run build
	@touch web/dist/.gitkeep

## build: compile the binary with the current web/dist embedded
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/sing-box-web

test:
	go test $(TESTFLAGS) ./...

vet:
	go vet ./...
	@test -z "$$(gofmt -l .)" || (gofmt -l . && echo 'run gofmt -w .' && exit 1)

## check: everything CI runs
check: vet test web
	cd web && npm run typecheck

## dev-api / dev-web: run in two terminals; Vite proxies /api to the backend
dev-api:
	go run ./cmd/sing-box-web -listen 127.0.0.1:9091

dev-web: web/node_modules
	cd web && npm run dev

clean:
	rm -rf bin dist web/node_modules/.tmp
	find web/dist -mindepth 1 ! -name .gitkeep -delete
