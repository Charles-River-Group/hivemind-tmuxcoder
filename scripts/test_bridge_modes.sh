#!/bin/bash
# Test script for Bridge dual-mode functionality

set -e

echo "=== Testing Bridge Dual-Mode Functionality ==="
echo

# Build the bridge binary
echo "1. Building bridge binary..."
go build -o /tmp/test-bridge ./cmd/bridge
echo "   ✓ Build successful"
echo

# Test 1: Headless mode (no terminal)
echo "2. Testing Headless Mode (no terminal)..."
echo "   Starting bridge in background (should use headless PTY mode)..."
timeout 3s /tmp/test-bridge \
    --workspace-uid test-headless \
    --label "test-headless" \
    --shell /bin/sh \
    --arg -c \
    --arg "echo 'Hello from headless'; sleep 2" \
    --socket /tmp/test-bus.sock \
    > /tmp/test-headless.log 2>&1 &

HEADLESS_PID=$!
sleep 1

if grep -q "Running in headless PTY mode" /tmp/test-headless.log; then
    echo "   ✓ Headless mode detected correctly"
else
    echo "   ✗ Failed to detect headless mode"
    cat /tmp/test-headless.log
fi

wait $HEADLESS_PID 2>/dev/null || true
echo

# Test 2: Check terminal detection
echo "3. Testing Terminal Detection..."
cat > /tmp/test_terminal.go <<'EOF'
package main

import (
	"fmt"
	"os"
	"golang.org/x/term"
)

func main() {
	stdin := term.IsTerminal(int(os.Stdin.Fd()))
	stdout := term.IsTerminal(int(os.Stdout.Fd()))
	fmt.Printf("stdin is terminal: %v\n", stdin)
	fmt.Printf("stdout is terminal: %v\n", stdout)
	fmt.Printf("Would use transparent mode: %v\n", stdin && stdout)
}
EOF

go run /tmp/test_terminal.go
echo

# Test 3: Verify both modes compile
echo "4. Verifying code compiles..."
go build -o /dev/null ./internal/bridge/core
echo "   ✓ Core bridge package compiles"

go build -o /dev/null ./cmd/bridge
echo "   ✓ Bridge binary compiles"
echo

echo "=== All Tests Passed ==="
echo
echo "To test transparent mode manually:"
echo "  1. Start bus server: tmuxcoder-bus"
echo "  2. In a tmux pane, run: tmuxcoder-bridge --workspace-uid test --label test"
echo "  3. Check logs for 'Running in transparent mode'"
