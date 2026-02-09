.PHONY: all build clean package help bus ui up down

BINDIR ?= dist
BUS_BIN := $(BINDIR)/tmuxcoder-bus
INGEST_BIN := $(BINDIR)/tmuxcoder-ingest
CLI_BIN := $(BINDIR)/tmuxcoder
PACKAGE_BINS := $(notdir $(BUS_BIN)) $(notdir $(INGEST_BIN)) $(notdir $(CLI_BIN))
RUN_DIR ?= .run
SOCKET ?=

BUS_FLAGS := $(if $(SOCKET),-socket $(SOCKET),)
UI_FLAGS := $(if $(SOCKET),--socket $(SOCKET),)

.PHONY: $(BUS_BIN) $(INGEST_BIN) $(CLI_BIN)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
PACKAGE_NAME := tmuxcoder-$(VERSION)-$(GOOS)-$(GOARCH).tar.gz
INGEST_LDFLAGS := -X main.Version=$(VERSION)

all: build

build: $(BUS_BIN) $(INGEST_BIN) $(CLI_BIN)

$(BUS_BIN):
	@mkdir -p $(BINDIR)
	go build -o $@ ./cmd/bus

$(INGEST_BIN):
	@mkdir -p $(BINDIR)
	go build -ldflags "$(INGEST_LDFLAGS)" -o $@ ./cmd/ingest

$(CLI_BIN):
	@mkdir -p $(BINDIR)
	go build -o $@ ./cmd/cli

package: build
	tar -C $(BINDIR) -czf $(BINDIR)/$(PACKAGE_NAME) $(PACKAGE_BINS)

clean:
	rm -f $(BUS_BIN) $(INGEST_BIN) $(CLI_BIN) $(BINDIR)/tmuxcoder-*.tar.gz

bus: $(BUS_BIN)
	@set -e; \
	mkdir -p $(RUN_DIR); \
	if [ -f "$(RUN_DIR)/bus.pid" ] && kill -0 $$(cat "$(RUN_DIR)/bus.pid") 2>/dev/null; then \
		echo "bus already running (pid $$(cat "$(RUN_DIR)/bus.pid"))"; \
	else \
		$(BUS_BIN) $(BUS_FLAGS) >$(RUN_DIR)/bus.log 2>&1 & echo $$! >$(RUN_DIR)/bus.pid; \
		echo "bus started (pid $$(cat "$(RUN_DIR)/bus.pid"))"; \
	fi

ui: $(CLI_BIN)
	@$(CLI_BIN) ui $(UI_FLAGS)

up: build bus ui

down:
	@set -e; \
	if [ -f "$(RUN_DIR)/bus.pid" ]; then kill $$(cat "$(RUN_DIR)/bus.pid") 2>/dev/null || true; rm -f "$(RUN_DIR)/bus.pid"; fi

help:
	@echo "Targets:"
	@echo "  build    Build tmuxcoder binaries into $(BINDIR)"
	@echo "  package  Build and create tarball in $(BINDIR)"
	@echo "  clean    Remove built binaries and packages"
	@echo "  bus      Start bus in background (logs in $(RUN_DIR))"
	@echo "  ui       Start interactive UI (foreground)"
	@echo "  up       Build, start bus, then run UI"
	@echo "  down     Stop background bus"
