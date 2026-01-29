// Package core provides terminal utility functions for Bridge.
package core

import (
	"io"
	"os"

	"golang.org/x/term"
)

// isTerminal checks if a file descriptor is a terminal.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

// hasRealTerminal checks if we're running in a real terminal environment
// (both stdin and stdout are terminals).
func hasRealTerminal() bool {
	return isTerminal(os.Stdin) && isTerminal(os.Stdout)
}

// getTerminalSize gets the current terminal size.
func getTerminalSize(f *os.File) (rows, cols uint16, err error) {
	w, h, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0, 0, err
	}
	return uint16(h), uint16(w), nil
}

// teeWriter writes to multiple writers and sends output to bus.
type teeWriter struct {
	writers []io.Writer
}

func newTeeWriter(writers ...io.Writer) *teeWriter {
	return &teeWriter{writers: writers}
}

func (t *teeWriter) Write(p []byte) (n int, err error) {
	for _, w := range t.writers {
		n, err = w.Write(p)
		if err != nil {
			return
		}
		if n != len(p) {
			err = io.ErrShortWrite
			return
		}
	}
	return len(p), nil
}
