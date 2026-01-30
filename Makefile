.PHONY: all build clean package help bus controller ui up down

BINDIR ?= dist
BUS_BIN := $(BINDIR)/tmuxcoder-bus
BRIDGE_BIN := $(BINDIR)/tmuxcoder-bridge
CLI_BIN := $(BINDIR)/tmuxcoder
RUN_DIR ?= .run
SOCKET ?=

BUS_FLAGS := $(if $(SOCKET),-socket $(SOCKET),)
CONTROLLER_FLAGS := $(if $(SOCKET),-socket $(SOCKET),)
UI_FLAGS := $(if $(SOCKET),--socket $(SOCKET),)

.PHONY: $(BUS_BIN) $(BRIDGE_BIN) $(CLI_BIN)

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

bus: $(BUS_BIN)
	@set -e; \
	mkdir -p $(RUN_DIR); \
	if [ -f "$(RUN_DIR)/bus.pid" ] && kill -0 $$(cat "$(RUN_DIR)/bus.pid") 2>/dev/null; then \
		echo "bus already running (pid $$(cat "$(RUN_DIR)/bus.pid"))"; \
	else \
		$(BUS_BIN) $(BUS_FLAGS) >$(RUN_DIR)/bus.log 2>&1 & echo $$! >$(RUN_DIR)/bus.pid; \
		echo "bus started (pid $$(cat "$(RUN_DIR)/bus.pid"))"; \
	fi

controller: $(CLI_BIN)
	@set -e; \
	mkdir -p $(RUN_DIR); \
	if [ -f "$(RUN_DIR)/controller.pid" ] && kill -0 $$(cat "$(RUN_DIR)/controller.pid") 2>/dev/null; then \
		echo "controller already running (pid $$(cat "$(RUN_DIR)/controller.pid"))"; \
	else \
		$(CLI_BIN) controller $(CONTROLLER_FLAGS) >$(RUN_DIR)/controller.log 2>&1 & echo $$! >$(RUN_DIR)/controller.pid; \
		echo "controller started (pid $$(cat "$(RUN_DIR)/controller.pid"))"; \
	fi

ui: $(CLI_BIN)
	@$(CLI_BIN) ui $(UI_FLAGS)

up: build bus controller ui

down:
	@set -e; \
	if [ -f "$(RUN_DIR)/controller.pid" ]; then kill $$(cat "$(RUN_DIR)/controller.pid") 2>/dev/null || true; rm -f "$(RUN_DIR)/controller.pid"; fi; \
	if [ -f "$(RUN_DIR)/bus.pid" ]; then kill $$(cat "$(RUN_DIR)/bus.pid") 2>/dev/null || true; rm -f "$(RUN_DIR)/bus.pid"; fi

help:
	@echo "Targets:"
	@echo "  build    Build tmuxcoder binaries into $(BINDIR)"
	@echo "  package  Build and create tarball in $(BINDIR)"
	@echo "  clean    Remove built binaries and packages"
	@echo "  bus      Start bus in background (logs in $(RUN_DIR))"
	@echo "  controller Start controller in background (logs in $(RUN_DIR))"
	@echo "  ui       Start interactive UI (foreground)"
	@echo "  up       Build, start bus/controller, then run UI"
	@echo "  down     Stop background bus/controller"
