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

	windowID, err := tmuxNewWindow(ctx, sessionName, config.WindowName, buildLogCommand(config))
	if err != nil {
		return err
	}

	if err := tmuxSplitWindow(ctx, windowID, buildWorkspaceCommand(config)); err != nil {
		return err
	}

	if insideTmux {
		if err := tmuxSelectWindow(ctx, windowID); err != nil {
			return err
		}
		return nil
	}

	return tmuxAttach(ctx, sessionName)
}

func buildLogCommand(config TmuxConfig) []string {
	binPath := tmuxcoderBinary()
	args := []string{binPath, "logs", "tail", "--format"}
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
	quotedBin := shellQuote(binPath)
	script := fmt.Sprintf(`while true; do
  clear
  echo "TmuxCoder Workspaces"
  echo
  %s workspaces list
  echo
  echo "Send input: %s send --workspace <uid> <text>"
  sleep %s
done`, quotedBin, quotedBin, config.RefreshInterval.String())
	return []string{"sh", "-c", script}
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

func tmuxNewWindow(ctx context.Context, session, name string, command []string) (string, error) {
	args := []string{"new-window", "-t", session, "-n", name, "-P", "-F", "#{window_id}"}
	args = append(args, command...)
	return runTmux(ctx, args...)
}

func tmuxSplitWindow(ctx context.Context, target string, command []string) error {
	args := []string{"split-window", "-h", "-t", target, "-p", "30"}
	args = append(args, command...)
	_, err := runTmux(ctx, args...)
	return err
}

func tmuxSelectWindow(ctx context.Context, target string) error {
	_, err := runTmux(ctx, "select-window", "-t", target)
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
