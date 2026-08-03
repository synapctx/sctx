.PHONY: build test vet fmt lint install install-sctxd clean

BINARY := sctx
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

build:
	cd app && go build $(LDFLAGS) -o ../bin/$(BINARY) ./cmd/sctx

test:
	cd app && go test -race ./...

vet:
	cd app && go vet ./...

fmt:
	cd app && gofmt -w .

lint:
	cd app && golangci-lint run || true

install: build
	install -m 0755 bin/$(BINARY) $(HOME)/.local/bin/$(BINARY)

# install-sctxd builds and installs the workspace-daemon helper BESIDE sctx.
#
# It lives in a separate, PRIVATE repository, so this target only works with that
# repository checked out as a sibling — and that is the whole point: this module
# stays buildable by anyone, and `watch` is delivered by shipping both binaries in
# one release archive. SCTXD_SRC overrides the location.
SCTXD_SRC ?= ../workspace-delta-daemon/app
SCTXD := sctxd

install-sctxd:
	@test -d $(SCTXD_SRC) || { \
		echo "workspace daemon source not found at $(SCTXD_SRC)."; \
		echo "It is a separate repository; sctx itself needs nothing from it."; \
		exit 1; }
	cd $(SCTXD_SRC) && go build -o $(CURDIR)/bin/$(SCTXD) ./cmd/$(SCTXD)
	install -m 0755 bin/$(SCTXD) $(HOME)/.local/bin/$(SCTXD)

clean:
	rm -rf bin
