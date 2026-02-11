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
	SessionName   string
	WindowName    string
	WorkspaceUID  string
	IncludeGlobal bool
	EventTypes    []string
	SocketPath    string
}

// StatusConfig holds settings for launching the tmux status UI.
type StatusConfig struct {
	SessionName   string
	WindowName    string
	SocketPath    string
	DBPath        string
	ClaimDir      string
	TmuxSessionID string
	Interval      time.Duration
	Clear         bool
}

// LaunchTmuxUI starts a tmux window with a log pane.
func LaunchTmuxUI(ctx context.Context, config TmuxConfig) error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux not found in PATH")
	}

	if config.WindowName == "" {
		config.WindowName = "tmuxcoder"
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

	logPane, err := tmuxNewWindow(ctx, sessionName, config.WindowName)
	if err != nil {
		return err
	}

	if err := tmuxSendCommand(ctx, logPane, buildLogCommand(config)); err != nil {
		return err
	}

	if insideTmux {
		windowID, err := tmuxWindowID(ctx, logPane)
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

// LaunchTmuxStatus starts a tmux window that refreshes tmuxcoder status.
func LaunchTmuxStatus(ctx context.Context, config StatusConfig) error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux not found in PATH")
	}

	if config.WindowName == "" {
		config.WindowName = "tmuxcoder-status"
	}
	if config.Interval <= 0 {
		config.Interval = 2 * time.Second
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

	statusPane, err := tmuxNewWindow(ctx, sessionName, config.WindowName)
	if err != nil {
		return err
	}

	if err := tmuxSendCommand(ctx, statusPane, buildStatusCommand(config)); err != nil {
		return err
	}

	if insideTmux {
		windowID, err := tmuxWindowID(ctx, statusPane)
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

func buildStatusCommand(config StatusConfig) []string {
	binPath := tmuxcoderBinary()
	args := []string{binPath, "status", "--watch", config.Interval.String()}
	if config.Clear {
		args = append(args, "--clear")
	}
	if config.SocketPath != "" {
		args = append(args, "--socket", config.SocketPath)
	}
	if config.DBPath != "" {
		args = append(args, "--db-path", config.DBPath)
	}
	if config.ClaimDir != "" {
		args = append(args, "--claim-dir", config.ClaimDir)
	}
	if config.TmuxSessionID != "" {
		args = append(args, "--tmux-session-id", config.TmuxSessionID)
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
