BINARY  := ubt
PKG     := github.com/arif-rachim/ubuntu-tool
PREFIX  ?= /usr/local
BIN_DIR := bin
DIST    := dist

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.Date=$(DATE)

GOFLAGS_BUILD := -trimpath -ldflags "$(LDFLAGS)"
PLATFORMS     := linux/amd64 linux/arm64

.PHONY: all build build-all release run install uninstall test vet fmt fmt-check lint clean

all: fmt-check vet test build

build:
	CGO_ENABLED=0 go build $(GOFLAGS_BUILD) -o $(BIN_DIR)/$(BINARY) ./cmd/ubt

build-all:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		out=$(DIST)/$(BINARY)-$$os-$$arch; \
		echo "build $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build $(GOFLAGS_BUILD) -o $$out ./cmd/ubt || exit 1; \
	done

# make release TAG=v0.1.0 [PUBLISH=1]
release:
	scripts/release.sh $(TAG) $(if $(PUBLISH),--publish,)

run: build
	./$(BIN_DIR)/$(BINARY) $(ARGS)

install: build
	install -d $(DESTDIR)$(PREFIX)/bin
	install -m 0755 $(BIN_DIR)/$(BINARY) $(DESTDIR)$(PREFIX)/bin/$(BINARY)

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/$(BINARY)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -s -w .

fmt-check:
	@out=$$(gofmt -s -l .); if [ -n "$$out" ]; then echo "file belum di-gofmt:"; echo "$$out"; exit 1; fi

lint: fmt-check vet
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run; \
	else echo "golangci-lint tidak terinstall, dilewati (go vet sudah jalan)"; fi

clean:
	rm -rf $(BIN_DIR) $(DIST)
