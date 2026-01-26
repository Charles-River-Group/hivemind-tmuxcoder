.PHONY: all build clean package help

BINDIR ?= dist
BUS_BIN := $(BINDIR)/tmuxcoder-bus
BRIDGE_BIN := $(BINDIR)/tmuxcoder-bridge
CLI_BIN := $(BINDIR)/tmuxcoder

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
PACKAGE_NAME := tmuxcoder-$(VERSION)-$(GOOS)-$(GOARCH).tar.gz

all: build

build: $(BUS_BIN) $(BRIDGE_BIN) $(CLI_BIN)

$(BUS_BIN):
	@mkdir -p $(BINDIR)
	go build -o $@ ./cmd/bus

$(BRIDGE_BIN):
	@mkdir -p $(BINDIR)
	go build -o $@ ./cmd/bridge

$(CLI_BIN):
	@mkdir -p $(BINDIR)
	go build -o $@ ./cmd/cli

package: build
	tar -C $(BINDIR) -czf $(BINDIR)/$(PACKAGE_NAME) $(notdir $(BUS_BIN)) $(notdir $(BRIDGE_BIN)) $(notdir $(CLI_BIN))

clean:
	rm -f $(BUS_BIN) $(BRIDGE_BIN) $(CLI_BIN) $(BINDIR)/tmuxcoder-*.tar.gz

help:
	@echo "Targets:"
	@echo "  build    Build tmuxcoder binaries into $(BINDIR)"
	@echo "  package  Build and create tarball in $(BINDIR)"
	@echo "  clean    Remove built binaries and packages"
