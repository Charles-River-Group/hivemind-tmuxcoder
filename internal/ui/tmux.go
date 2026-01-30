package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/opencode/hivemind-tmuxcoder/internal/protocol"
)

// TmuxConfig holds settings for launching the tmux UI.
type TmuxConfig struct {
	SessionName     string
	WindowName      string
	WorkspaceUID    string
	IncludeGlobal   bool
	EventTypes      []string
	RefreshInterval time.Duration
	SocketPath      string
}

// LaunchTmuxUI starts a tmux window with log and workspace panes.
func LaunchTmuxUI(ctx context.Context, config TmuxConfig) error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux not found in PATH")
	}

	if config.WindowName == "" {
		config.WindowName = "tmuxcoder"
	}
	if config.RefreshInterval <= 0 {
		config.RefreshInterval = 2 * time.Second
	}
	if len(config.EventTypes) == 0 {
		config.EventTypes = []string{protocol.TypeUILogAppend, protocol.TypeUIStatusUpdate}
	}

	insideTmux := os.Getenv("TMUX") != ""
	sessionName := config.SessionName
	if insideTmux && sessionName == "" {
		name, err := tmuxDisplay(ctx, "#S")
		if err != nil {
			return err
		}
		sessionName = name
	}
	if sessionName == "" {
		sessionName = "tmuxcoder"
	}

	if !insideTmux {
		exists, err := tmuxHasSession(ctx, sessionName)
		if err != nil {
			return err
		}
		if !exists {
			if err := tmuxNewSession(ctx, sessionName); err != nil {
				return err
			}
		}
	}

	leftPane, err := tmuxNewWindow(ctx, sessionName, config.WindowName)
	if err != nil {
		return err
	}

	rightPane, err := tmuxSplitWindow(ctx, leftPane)
	if err != nil {
		return err
	}

	if err := tmuxSendCommand(ctx, leftPane, buildLogCommand(config)); err != nil {
		return err
	}

	if err := tmuxSendCommand(ctx, rightPane, buildWorkspaceCommand(config)); err != nil {
		return err
	}

	if insideTmux {
		windowID, err := tmuxWindowID(ctx, leftPane)
		if err != nil {
			return err
		}
		if err := tmuxSelectWindow(ctx, windowID); err != nil {
			return err
		}
		return nil
	}

	return tmuxAttach(ctx, sessionName)
}

func buildLogCommand(config TmuxConfig) []string {
	binPath := tmuxcoderBinary()
	args := []string{binPath, "logs", "tail"}
	if config.SocketPath != "" {
		args = append(args, "--socket", config.SocketPath)
	}
	if config.WorkspaceUID != "" {
		args = append(args, "--workspace", config.WorkspaceUID)
	}
	if config.IncludeGlobal {
		args = append(args, "--include-global")
	}
	types := cleanTypes(config.EventTypes)
	if len(types) > 0 {
		args = append(args, "--types", strings.Join(types, ","))
	}
	return args
}

func buildWorkspaceCommand(config TmuxConfig) []string {
	binPath := tmuxcoderBinary()
	args := []string{
		binPath,
		"ui",
		"interactive",
	}
	if config.SocketPath != "" {
		args = append(args, "--socket", config.SocketPath)
	}
	if config.RefreshInterval > 0 {
		args = append(args, "--refresh", config.RefreshInterval.String())
	}
	return args
}

func tmuxDisplay(ctx context.Context, format string) (string, error) {
	output, err := runTmux(ctx, "display-message", "-p", format)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

func tmuxHasSession(ctx context.Context, session string) (bool, error) {
	cmd := exec.CommandContext(ctx, "tmux", "has-session", "-t", session)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, nil
	}
	return false, err
}

func tmuxNewSession(ctx context.Context, session string) error {
	_, err := runTmux(ctx, "new-session", "-d", "-s", session)
	return err
}

func tmuxNewWindow(ctx context.Context, session, name string) (string, error) {
	args := []string{"new-window", "-t", session + ":", "-n", name, "-P", "-F", "#{pane_id}"}
	return runTmux(ctx, args...)
}

func tmuxSplitWindow(ctx context.Context, target string) (string, error) {
	args := []string{"split-window", "-h", "-t", target, "-p", "30", "-P", "-F", "#{pane_id}"}
	return runTmux(ctx, args...)
}

func tmuxSelectWindow(ctx context.Context, target string) error {
	_, err := runTmux(ctx, "select-window", "-t", target)
	return err
}

func tmuxWindowID(ctx context.Context, target string) (string, error) {
	output, err := runTmux(ctx, "display-message", "-p", "-t", target, "#{window_id}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

func tmuxSendCommand(ctx context.Context, target string, args []string) error {
	if len(args) == 0 {
		return nil
	}
	cmd := shellJoin(args)
	_, err := runTmux(ctx, "send-keys", "-t", target, cmd, "C-m")
	return err
}

func tmuxAttach(ctx context.Context, session string) error {
	cmd := exec.CommandContext(ctx, "tmux", "attach", "-t", session)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runTmux(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		errText := strings.TrimSpace(string(output))
		if errText != "" {
			return "", fmt.Errorf("tmux %s: %s", strings.Join(args, " "), errText)
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func tmuxcoderBinary() string {
	exe, err := os.Executable()
	if err == nil && exe != "" {
		if abs, err := filepath.Abs(exe); err == nil {
			return abs
		}
		return exe
	}
	return "tmuxcoder"
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func shellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}
