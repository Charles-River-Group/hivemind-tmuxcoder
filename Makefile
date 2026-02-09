.PHONY: all build clean package help bus ui up down

BINDIR ?= dist
CLI_BIN := $(BINDIR)/tmuxcoder
PACKAGE_BINS := $(notdir $(CLI_BIN))
RUN_DIR ?= .run
SOCKET ?=

BUS_FLAGS := $(if $(SOCKET),--socket $(SOCKET),)
UI_FLAGS := $(if $(SOCKET),--socket $(SOCKET),)

.PHONY: $(CLI_BIN)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
PACKAGE_NAME := tmuxcoder-$(VERSION)-$(GOOS)-$(GOARCH).tar.gz
all: build

build: $(CLI_BIN)

$(CLI_BIN):
	@mkdir -p $(BINDIR)
	go build -o $@ ./cmd/tmuxcoder

package: build
	tar -C $(BINDIR) -czf $(BINDIR)/$(PACKAGE_NAME) $(PACKAGE_BINS)

clean:
	rm -f $(CLI_BIN) $(BINDIR)/tmuxcoder-*.tar.gz

bus: $(CLI_BIN)
	@set -e; \
	mkdir -p $(RUN_DIR); \
	if [ -f "$(RUN_DIR)/bus.pid" ] && kill -0 $$(cat "$(RUN_DIR)/bus.pid") 2>/dev/null; then \
		echo "bus already running (pid $$(cat "$(RUN_DIR)/bus.pid"))"; \
	else \
		$(CLI_BIN) bus $(BUS_FLAGS) >$(RUN_DIR)/bus.log 2>&1 & echo $$! >$(RUN_DIR)/bus.pid; \
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
