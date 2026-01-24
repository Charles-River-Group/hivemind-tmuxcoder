// Package pty provides PTY (pseudo-terminal) proxy functionality for the Workspace Bridge.
// It wraps child processes with a PTY and provides read/write interfaces for I/O.
package pty

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

// Proxy manages a pseudo-terminal wrapper for a child process.
type Proxy struct {
	cmd  *exec.Cmd
	pty  *os.File
	size *pty.Winsize

	mu      sync.RWMutex
	started bool
	done    chan struct{}
	err     error
}

// Config holds PTY Proxy configuration.
type Config struct {
	// Command is the command to execute.
	Command string

	// Args are the command arguments.
	Args []string

	// Dir is the working directory.
	Dir string

	// Env is the environment variables.
	Env []string

	// Rows is the initial terminal rows.
	Rows uint16

	// Cols is the initial terminal columns.
	Cols uint16
}

// DefaultConfig returns the default PTY configuration.
func DefaultConfig() *Config {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	return &Config{
		Command: shell,
		Rows:    24,
		Cols:    80,
	}
}

// New creates a new PTY Proxy with the given configuration.
func New(config *Config) (*Proxy, error) {
	if config == nil {
		config = DefaultConfig()
	}
	if config.Command == "" {
		config.Command = DefaultConfig().Command
	}
	if config.Rows == 0 {
		config.Rows = 24
	}
	if config.Cols == 0 {
		config.Cols = 80
	}

	cmd := exec.Command(config.Command, config.Args...)
	if config.Dir != "" {
		cmd.Dir = config.Dir
	}
	if len(config.Env) > 0 {
		cmd.Env = config.Env
	} else {
		cmd.Env = os.Environ()
	}

	return &Proxy{
		cmd: cmd,
		size: &pty.Winsize{
			Rows: config.Rows,
			Cols: config.Cols,
		},
		done: make(chan struct{}),
	}, nil
}

// Start starts the child process with a PTY attached.
func (p *Proxy) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.started {
		return fmt.Errorf("proxy already started")
	}

	// Start the command with a pty
	ptmx, err := pty.StartWithSize(p.cmd, p.size)
	if err != nil {
		return fmt.Errorf("failed to start pty: %w", err)
	}
	p.pty = ptmx
	p.started = true

	// Monitor process exit in background
	go p.waitForExit()

	return nil
}

// waitForExit waits for the child process to exit.
func (p *Proxy) waitForExit() {
	err := p.cmd.Wait()
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
	close(p.done)
}

// Read reads from the PTY (child's stdout/stderr).
func (p *Proxy) Read(b []byte) (int, error) {
	p.mu.RLock()
	if !p.started || p.pty == nil {
		p.mu.RUnlock()
		return 0, fmt.Errorf("proxy not started")
	}
	ptmx := p.pty
	p.mu.RUnlock()

	return ptmx.Read(b)
}

// Write writes to the PTY (child's stdin).
func (p *Proxy) Write(b []byte) (int, error) {
	p.mu.RLock()
	if !p.started || p.pty == nil {
		p.mu.RUnlock()
		return 0, fmt.Errorf("proxy not started")
	}
	ptmx := p.pty
	p.mu.RUnlock()

	return ptmx.Write(b)
}

// WriteString writes a string to the PTY (child's stdin).
func (p *Proxy) WriteString(s string) (int, error) {
	return p.Write([]byte(s))
}

// Resize resizes the PTY.
func (p *Proxy) Resize(rows, cols uint16) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.started || p.pty == nil {
		return fmt.Errorf("proxy not started")
	}

	p.size.Rows = rows
	p.size.Cols = cols
	return pty.Setsize(p.pty, p.size)
}

// Signal sends a signal to the child process.
func (p *Proxy) Signal(sig os.Signal) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if !p.started || p.cmd.Process == nil {
		return fmt.Errorf("proxy not started")
	}

	return p.cmd.Process.Signal(sig)
}

// Interrupt sends SIGINT to the child process.
func (p *Proxy) Interrupt() error {
	return p.Signal(syscall.SIGINT)
}

// Kill sends SIGKILL to the child process.
func (p *Proxy) Kill() error {
	return p.Signal(syscall.SIGKILL)
}

// Wait waits for the child process to exit.
func (p *Proxy) Wait() error {
	<-p.done
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.err
}

// Done returns a channel that is closed when the process exits.
func (p *Proxy) Done() <-chan struct{} {
	return p.done
}

// Close closes the PTY and terminates the child process.
func (p *Proxy) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	var errs []error

	// Close the PTY
	if p.pty != nil {
		if err := p.pty.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close pty: %w", err))
		}
		p.pty = nil
	}

	// Kill the process if still running
	if p.cmd.Process != nil {
		select {
		case <-p.done:
			// Already exited
		default:
			// Send SIGTERM first
			_ = p.cmd.Process.Signal(syscall.SIGTERM)
		}
	}

	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// PID returns the process ID of the child process.
func (p *Proxy) PID() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return 0
}

// Running returns true if the child process is still running.
func (p *Proxy) Running() bool {
	select {
	case <-p.done:
		return false
	default:
		return p.started
	}
}

// Fd returns the file descriptor of the PTY master.
func (p *Proxy) Fd() uintptr {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.pty != nil {
		return p.pty.Fd()
	}
	return 0
}

// File returns the PTY master file.
func (p *Proxy) File() *os.File {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pty
}

// Reader returns an io.Reader for the PTY output.
func (p *Proxy) Reader() io.Reader {
	return p
}

// Writer returns an io.Writer for the PTY input.
func (p *Proxy) Writer() io.Writer {
	return p
}
