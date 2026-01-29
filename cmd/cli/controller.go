package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	bridgeclient "github.com/opencode/hivemind-tmuxcoder/internal/bridge/client"
	"github.com/opencode/hivemind-tmuxcoder/internal/protocol"
	uiclient "github.com/opencode/hivemind-tmuxcoder/internal/ui"
	"github.com/opencode/hivemind-tmuxcoder/pkg/util/ulid"
)

func handleController(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("controller", flag.ExitOnError)
	var socketPath string
	fs.StringVar(&socketPath, "socket", "", "Bus server socket path (default: auto-detect)")
	fs.Parse(args)

	config := bridgeclient.DefaultConfig()
	if socketPath != "" {
		config.SocketPath = socketPath
	}
	config.WorkspaceUID = "controller-" + ulid.New()
	config.WorkspaceID = "controller"
	config.Label = "tmuxcoder-controller"
	config.DriveMode = "service"

	client := bridgeclient.New(config)
	if err := client.Connect(ctx); err != nil {
		log.Fatalf("Controller connect failed: %v", err)
	}
	defer client.Close()

	if err := client.Subscribe(nil, []string{protocol.TypeBusCreateWorkspaceRequest}, false); err != nil {
		log.Fatalf("Controller subscribe failed: %v", err)
	}

	client.OnEvent(protocol.TypeBusCreateWorkspaceRequest, func(event *protocol.EventEnvelope) {
		handleCreateWorkspaceRequest(client, config.SocketPath, event)
	})

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-sigCtx.Done()
}

func handleCreateWorkspaceRequest(client *bridgeclient.Client, socketPath string, event *protocol.EventEnvelope) {
	log.Printf("[CONTROLLER] Received create workspace request: event_id=%s", event.EventID)
	var payload protocol.CreateWorkspaceRequestPayload
	if err := protocol.UnmarshalPayload(event.Payload, &payload); err != nil {
		log.Printf("[CONTROLLER] Failed to unmarshal payload: %v", err)
		sendCreateWorkspaceResponse(client, event, "", "", "", protocol.ErrKindProtocol, protocol.ErrCodeBadSchema, err.Error())
		return
	}
	log.Printf("[CONTROLLER] Request: label=%s, layout=%s, tmux_pane=%s, tmux_session=%s", payload.Label, payload.Layout, payload.TmuxPane, payload.TmuxSession)

	wsUID := payload.WorkspaceUID
	if wsUID == "" {
		wsUID = ulid.New()
	}
	label := strings.TrimSpace(payload.Label)
	if label == "" {
		label = "workspace-" + wsUID[:8]
	}
	command := payload.Command
	if command == "" {
		command = defaultShell()
	}
	workDir := payload.WorkDir
	if workDir == "" {
		workDir = defaultWorkDir()
	}

	wsID := fmt.Sprintf("bridge:%s", shortID(wsUID))
	bridgeArgs := buildBridgeArgs(socketPath, wsUID, wsID, label, command, payload.Args, workDir)

	layout := strings.ToLower(strings.TrimSpace(payload.Layout))
	if layout == "" {
		layout = "bridge-only"
	}

	var err error
	switch layout {
	case "bridge-only":
		err = startBridgeHeadless(bridgeArgs)
	case "split-pane":
		if payload.TmuxPane == "" {
			err = fmt.Errorf("tmux pane required for split-pane")
		} else {
			otherPane, paneErr := tmuxOtherPaneInWindow(payload.TmuxPane)
			if paneErr == nil && otherPane != "" {
				err = tmuxRespawnPane(otherPane, bridgeArgs)
			} else {
				err = tmuxSplitWindow(payload.TmuxPane, bridgeArgs)
			}
		}
	case "new-window":
		if payload.TmuxSession == "" {
			err = fmt.Errorf("tmux session required for new-window")
		} else {
			windowName := payload.TmuxWindow
			if windowName == "" {
				windowName = label
			}
			err = tmuxNewWindow(payload.TmuxSession, windowName, bridgeArgs)
		}
	default:
		err = fmt.Errorf("unknown layout: %s", layout)
	}

	if err != nil {
		log.Printf("[CONTROLLER] Failed to create workspace: %v", err)
		sendCreateWorkspaceResponse(client, event, "", "", "", protocol.ErrKindBackend, protocol.ErrCodeUnknownAction, err.Error())
		return
	}

	if err := waitForWorkspaceConnected(socketPath, wsUID, 2*time.Second); err != nil {
		log.Printf("[CONTROLLER] Workspace did not connect: uid=%s err=%v", wsUID, err)
		sendCreateWorkspaceResponse(client, event, "", "", "", protocol.ErrKindTransport, protocol.ErrCodeDestNotConnected, fmt.Sprintf("workspace did not connect: %v", err))
		return
	}

	log.Printf("[CONTROLLER] Successfully created workspace: uid=%s, id=%s, label=%s", wsUID, wsID, label)
	sendCreateWorkspaceResponse(client, event, wsUID, wsID, label, "", "", "")
}

func waitForWorkspaceConnected(socketPath, wsUID string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client := uiclient.NewClient(&uiclient.Config{SocketPath: socketPath})
	if err := client.Connect(ctx); err != nil {
		return err
	}
	defer client.Close()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		workspaces, err := client.ListWorkspaces(ctx)
		if err == nil {
			for _, ws := range workspaces {
				if ws.WorkspaceUID == wsUID {
					return nil
				}
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func buildBridgeArgs(socketPath, wsUID, wsID, label, command string, args []string, workDir string) []string {
	bin := tmuxcoderSiblingBinary("tmuxcoder-bridge")
	cmdArgs := []string{
		bin,
		"--workspace-uid", wsUID,
		"--workspace-id", wsID,
		"--label", label,
		"--shell", command,
		"--dir", workDir,
	}
	if socketPath != "" {
		cmdArgs = append(cmdArgs, "--socket", socketPath)
	}
	for _, arg := range args {
		if arg == "" {
			continue
		}
		cmdArgs = append(cmdArgs, "--arg", arg)
	}
	return cmdArgs
}

func startBridgeHeadless(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing bridge command")
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func tmuxSplitWindow(target string, command []string) error {
	args := []string{"split-window", "-h", "-t", target, "-p", "30"}
	args = append(args, command...)
	_, err := runTmux(args...)
	return err
}

func tmuxRespawnPane(target string, command []string) error {
	args := []string{"respawn-pane", "-k", "-t", target}
	args = append(args, command...)
	_, err := runTmux(args...)
	return err
}

func tmuxOtherPaneInWindow(targetPane string) (string, error) {
	windowID, err := runTmux("display-message", "-p", "-t", targetPane, "#{window_id}")
	if err != nil {
		return "", err
	}

	panesOut, err := runTmux("list-panes", "-t", windowID, "-F", "#{pane_id}")
	if err != nil {
		return "", err
	}

	for _, line := range strings.Split(panesOut, "\n") {
		pane := strings.TrimSpace(line)
		if pane == "" || pane == targetPane {
			continue
		}
		return pane, nil
	}
	return "", fmt.Errorf("no other pane found in window %s", windowID)
}

func tmuxNewWindow(session, name string, command []string) error {
	args := []string{"new-window", "-t", session + ":", "-n", name, "-P", "-F", "#{window_id}"}
	args = append(args, command...)
	_, err := runTmux(args...)
	return err
}

func runTmux(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
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

func tmuxcoderSiblingBinary(name string) string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return name
	}
	dir := filepath.Dir(exe)
	candidate := filepath.Join(dir, name)
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return name
}

func sendCreateWorkspaceResponse(client *bridgeclient.Client, request *protocol.EventEnvelope, wsUID, wsID, label, errKind, errCode, errMsg string) {
	payload := protocol.CreateWorkspaceResponsePayload{
		Status:       "ok",
		WorkspaceUID: wsUID,
		WorkspaceID:  wsID,
		Label:        label,
	}
	if errKind != "" {
		payload.Status = "error"
		payload.Error = protocol.NewEventError(errKind, errCode, false, errMsg)
	}
	payloadBytes, _ := protocol.MarshalPayload(payload)

	response := protocol.NewEventEnvelope(
		ulid.New(),
		request.WorkspaceUID,
		client.Source(),
		protocol.TypeBusCreateWorkspaceResponse,
		payloadBytes,
	)
	response.InReplyTo = request.EventID
	response.CorrelationID = request.CorrelationID

	if err := client.Send(response); err != nil {
		log.Printf("[CONTROLLER] Failed to send create workspace response: %v", err)
	}
}

func defaultShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}

func defaultWorkDir() string {
	if dir, err := os.Getwd(); err == nil {
		return dir
	}
	if home := os.Getenv("HOME"); home != "" {
		return home
	}
	return "/"
}

func shortID(value string) string {
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}
