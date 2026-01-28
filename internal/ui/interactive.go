package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/opencode/hivemind-tmuxcoder/internal/protocol"
)

// InteractiveConfig configures the interactive TUI.
type InteractiveConfig struct {
	SocketPath      string
	RefreshInterval time.Duration
}

// RunInteractive starts the interactive TUI.
func RunInteractive(ctx context.Context, config InteractiveConfig) error {
	if config.RefreshInterval <= 0 {
		config.RefreshInterval = 2 * time.Second
	}

	client := NewClient(&Config{
		SocketPath: config.SocketPath,
		Output:     os.Stdout,
	})
	if err := client.Connect(ctx); err != nil {
		return err
	}
	defer client.Close()

	model := newInteractiveModel(ctx, client, config.RefreshInterval)
	program := tea.NewProgram(model, tea.WithContext(ctx))
	_, err := program.Run()
	return err
}

type viewMode int

const (
	viewDashboard viewMode = iota
	viewWorkspace
	viewCreate
)

type workspacesMsg []protocol.WorkspaceInfo
type errMsg struct{ err error }
type statusMsg string
type tickMsg time.Time

type interactiveModel struct {
	ctx             context.Context
	client          *Client
	refreshInterval time.Duration

	workspaces []protocol.WorkspaceInfo
	selected   int
	view       viewMode
	status     string
	lastError  string

	input       textinput.Model
	createLabel textinput.Model
	createView  int

	tmuxSession string
	tmuxPane    string
}

func newInteractiveModel(ctx context.Context, client *Client, refresh time.Duration) interactiveModel {
	input := textinput.New()
	input.Placeholder = "Enter command"
	input.Prompt = "> "
	input.Focus()

	createLabel := textinput.New()
	createLabel.Placeholder = "workspace-name"
	createLabel.Prompt = "Name: "
	createLabel.Focus()

	session, pane := detectTmuxContext(ctx)

	return interactiveModel{
		ctx:             ctx,
		client:          client,
		refreshInterval: refresh,
		view:            viewDashboard,
		input:           input,
		createLabel:     createLabel,
		tmuxSession:     session,
		tmuxPane:        pane,
	}
}

func (m interactiveModel) Init() tea.Cmd {
	return tea.Batch(m.refreshCmd(), m.tickCmd())
}

func (m interactiveModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		return m, tea.Batch(m.refreshCmd(), m.tickCmd())
	case workspacesMsg:
		m.workspaces = filterControllerWorkspaces(msg)
		if m.selected >= len(m.workspaces) {
			m.selected = len(m.workspaces) - 1
		}
		if m.selected < 0 {
			m.selected = 0
		}
		m.lastError = ""
		return m, nil
	case errMsg:
		m.lastError = msg.err.Error()
		return m, nil
	case statusMsg:
		m.status = string(msg)
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		}
	}

	switch m.view {
	case viewDashboard:
		return m.updateDashboard(msg)
	case viewWorkspace:
		return m.updateWorkspace(msg)
	case viewCreate:
		return m.updateCreate(msg)
	default:
		return m, nil
	}
}

func (m interactiveModel) View() string {
	switch m.view {
	case viewWorkspace:
		return m.viewWorkspace()
	case viewCreate:
		return m.viewCreate()
	default:
		return m.viewDashboard()
	}
}

func (m interactiveModel) updateDashboard(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "ctrl+p":
			if m.selected > 0 {
				m.selected--
			}
		case "down", "ctrl+n":
			if m.selected < len(m.workspaces)-1 {
				m.selected++
			}
		case "enter":
			if len(m.workspaces) > 0 {
				m.view = viewWorkspace
				m.input.Focus()
			}
		case "n":
			m.view = viewCreate
			m.createLabel.SetValue("")
			m.createLabel.Focus()
		case "r":
			return m, m.refreshCmd()
		}
	}

	return m, nil
}

func (m interactiveModel) updateWorkspace(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.view = viewDashboard
			m.input.Blur()
			return m, nil
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			m.input.SetValue("")
			return m, m.sendInputCmd(text)
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m interactiveModel) updateCreate(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.view = viewDashboard
			m.createLabel.Blur()
			return m, nil
		case "tab":
			m.createView = (m.createView + 1) % len(layoutOptions)
			return m, nil
		case "enter":
			label := strings.TrimSpace(m.createLabel.Value())
			if label == "" {
				label = fmt.Sprintf("workspace-%s", shortID(m.client.workspaceUID))
			}
			layout := layoutOptions[m.createView]
			if layout != "bridge-only" && m.tmuxPane == "" {
				return m, statusCmd("tmux context required for split/new window")
			}
			m.view = viewDashboard
			m.createLabel.Blur()
			return m, m.createWorkspaceCmd(label, layout)
		}
	}

	var cmd tea.Cmd
	m.createLabel, cmd = m.createLabel.Update(msg)
	return m, cmd
}

func (m interactiveModel) viewDashboard() string {
	var b strings.Builder
	b.WriteString("TmuxCoder Dashboard\n")
	b.WriteString(strings.Repeat("-", 24))
	b.WriteString("\n")
	if len(m.workspaces) == 0 {
		b.WriteString("No workspaces yet.\n")
	} else {
		for i, ws := range m.workspaces {
			cursor := " "
			if i == m.selected {
				cursor = ">"
			}
			fmt.Fprintf(&b, "%s %s (%s)\n", cursor, ws.Label, ws.WorkspaceUID)
		}
	}
	b.WriteString("\n")
	b.WriteString("[Enter] Select  [N] New  [R] Refresh  [Ctrl+C] Quit\n")
	if m.status != "" {
		b.WriteString("\n")
		b.WriteString(m.status)
	}
	if m.lastError != "" {
		b.WriteString("\n")
		b.WriteString("Error: " + m.lastError)
	}
	return b.String()
}

func (m interactiveModel) viewWorkspace() string {
	var b strings.Builder
	ws := m.selectedWorkspace()
	if ws != nil {
		fmt.Fprintf(&b, "Workspace: %s (%s)\n", ws.Label, ws.WorkspaceUID)
	} else {
		b.WriteString("Workspace: (none)\n")
	}
	b.WriteString(strings.Repeat("-", 24))
	b.WriteString("\n")
	b.WriteString(m.input.View())
	b.WriteString("\n")
	b.WriteString("[Enter] Send  [Esc] Back\n")
	if m.status != "" {
		b.WriteString("\n")
		b.WriteString(m.status)
	}
	return b.String()
}

func (m interactiveModel) viewCreate() string {
	var b strings.Builder
	b.WriteString("Create Workspace\n")
	b.WriteString(strings.Repeat("-", 24))
	b.WriteString("\n")
	b.WriteString(m.createLabel.View())
	b.WriteString("\n")
	fmt.Fprintf(&b, "Layout: %s (Tab to change)\n", layoutOptions[m.createView])
	b.WriteString("[Enter] Create  [Esc] Cancel\n")
	if m.status != "" {
		b.WriteString("\n")
		b.WriteString(m.status)
	}
	return b.String()
}

func (m interactiveModel) selectedWorkspace() *protocol.WorkspaceInfo {
	if m.selected < 0 || m.selected >= len(m.workspaces) {
		return nil
	}
	return &m.workspaces[m.selected]
}

func (m interactiveModel) refreshCmd() tea.Cmd {
	return func() tea.Msg {
		workspaces, err := m.client.ListWorkspaces(m.ctx)
		if err != nil {
			return errMsg{err: err}
		}
		return workspacesMsg(workspaces)
	}
}

func (m interactiveModel) tickCmd() tea.Cmd {
	return tea.Tick(m.refreshInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m interactiveModel) sendInputCmd(text string) tea.Cmd {
	ws := m.selectedWorkspace()
	if ws == nil {
		return statusCmd("no workspace selected")
	}
	return func() tea.Msg {
		if err := m.client.SendInput(m.ctx, ws.WorkspaceUID, text, false); err != nil {
			return errMsg{err: err}
		}
		return statusMsg("sent input to " + ws.Label)
	}
}

func (m interactiveModel) createWorkspaceCmd(label, layout string) tea.Cmd {
	return func() tea.Msg {
		payload := protocol.CreateWorkspaceRequestPayload{
			Label:       label,
			Command:     defaultShell(),
			WorkDir:     defaultWorkDir(),
			Layout:      layout,
			TmuxSession: m.tmuxSession,
			TmuxPane:    m.tmuxPane,
		}
		if err := m.client.RequestCreateWorkspace(m.ctx, payload); err != nil {
			return errMsg{err: err}
		}
		return statusMsg("create request sent for " + label)
	}
}

func statusCmd(text string) tea.Cmd {
	return func() tea.Msg {
		return statusMsg(text)
	}
}

func filterControllerWorkspaces(items []protocol.WorkspaceInfo) []protocol.WorkspaceInfo {
	filtered := make([]protocol.WorkspaceInfo, 0, len(items))
	for _, ws := range items {
		if ws.Label == "tmuxcoder-controller" {
			continue
		}
		if ws.Label == "tmuxcoder-ui" {
			continue
		}
		if strings.HasPrefix(ws.WorkspaceID, "ui:") {
			continue
		}
		filtered = append(filtered, ws)
	}
	return filtered
}

var layoutOptions = []string{"bridge-only", "split-pane", "new-window"}

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

func detectTmuxContext(ctx context.Context) (string, string) {
	if os.Getenv("TMUX") == "" {
		return "", ""
	}
	pane := os.Getenv("TMUX_PANE")
	session, err := tmuxDisplay(ctx, "#S")
	if err != nil {
		return "", pane
	}
	return session, pane
}
