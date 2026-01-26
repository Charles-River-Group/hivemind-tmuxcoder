package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/opencode/hivemind-tmuxcoder/internal/protocol"
)

// Renderer formats events for display.
type Renderer struct {
	out  io.Writer
	mode RenderMode
}

// NewRenderer creates a renderer for the given output and mode.
func NewRenderer(out io.Writer, mode RenderMode) *Renderer {
	if out == nil {
		out = io.Discard
	}
	return &Renderer{
		out:  out,
		mode: mode,
	}
}

// Render writes an event to the output.
func (r *Renderer) Render(event *protocol.EventEnvelope) {
	switch r.mode {
	case RenderModeFormatted:
		r.renderFormatted(event)
	default:
		r.renderRaw(event)
	}
}

func (r *Renderer) renderRaw(event *protocol.EventEnvelope) {
	switch event.Type {
	case protocol.TypeUILogAppend:
		var payload protocol.UILogAppendPayload
		if err := protocol.UnmarshalPayload(event.Payload, &payload); err == nil {
			_, _ = io.WriteString(r.out, payload.Text)
			return
		}
	case protocol.TypeUIStatusUpdate:
		var payload protocol.UIStatusUpdatePayload
		if err := protocol.UnmarshalPayload(event.Payload, &payload); err == nil {
			_, _ = fmt.Fprintf(r.out, "status %s %s\n", payload.WorkspaceUID, payload.Status)
			return
		}
	}

	data, err := json.Marshal(event)
	if err != nil {
		_, _ = fmt.Fprintf(r.out, "event %s\n", event.Type)
		return
	}
	_, _ = fmt.Fprintf(r.out, "%s\n", data)
}

func (r *Renderer) renderFormatted(event *protocol.EventEnvelope) {
	switch event.Type {
	case protocol.TypeUILogAppend:
		var payload protocol.UILogAppendPayload
		if err := protocol.UnmarshalPayload(event.Payload, &payload); err == nil {
			prefix := fmt.Sprintf("%s %s %s: ", event.Timestamp, shortID(event.WorkspaceUID), payload.Level)
			writePrefixed(r.out, prefix, payload.Text)
			return
		}
	case protocol.TypeUIStatusUpdate:
		var payload protocol.UIStatusUpdatePayload
		if err := protocol.UnmarshalPayload(event.Payload, &payload); err == nil {
			details := formatDetails(payload.Details)
			_, _ = fmt.Fprintf(r.out, "%s %s status=%s%s\n",
				event.Timestamp,
				shortID(payload.WorkspaceUID),
				payload.Status,
				details,
			)
			return
		}
	}

	data, err := json.Marshal(event)
	if err != nil {
		_, _ = fmt.Fprintf(r.out, "%s %s\n", event.Timestamp, event.Type)
		return
	}
	_, _ = fmt.Fprintf(r.out, "%s %s\n", event.Timestamp, data)
}

func writePrefixed(out io.Writer, prefix string, text string) {
	if text == "" {
		return
	}

	lines := strings.Split(text, "\n")
	last := len(lines) - 1
	for i, line := range lines {
		if i == last && line == "" {
			_, _ = io.WriteString(out, "\n")
			continue
		}
		_, _ = io.WriteString(out, prefix)
		_, _ = io.WriteString(out, line)
		if i < last {
			_, _ = io.WriteString(out, "\n")
		}
	}
}

func formatDetails(details map[string]interface{}) string {
	if len(details) == 0 {
		return ""
	}
	data, err := json.Marshal(details)
	if err != nil {
		return ""
	}
	return " details=" + string(data)
}
