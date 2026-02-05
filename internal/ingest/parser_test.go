package ingest

import (
	"testing"
)

func TestParser_ParseCodex(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name     string
		line     string
		wantRole string
		wantText string
		wantNil  bool
	}{
		{
			name:     "response_item user string content",
			line:     `{"type":"response_item","payload":{"role":"user","content":"Hello world"}}`,
			wantRole: "user",
			wantText: "Hello world",
		},
		{
			name:     "response_item assistant string content",
			line:     `{"type":"response_item","payload":{"role":"assistant","content":"Hi there"}}`,
			wantRole: "assistant",
			wantText: "Hi there",
		},
		{
			name:     "response_item with array content",
			line:     `{"type":"response_item","payload":{"role":"assistant","content":[{"type":"output_text","text":"Part 1"},{"type":"output_text","text":"Part 2"}]}}`,
			wantRole: "assistant",
			wantText: "Part 1\nPart 2",
		},
		{
			name:    "session_meta should be skipped",
			line:    `{"type":"session_meta","session_id":"abc"}`,
			wantNil: true,
		},
		{
			name:    "event_msg should be skipped",
			line:    `{"type":"event_msg","event":"start"}`,
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := parser.ParseLine([]byte(tt.line), "codex")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantNil {
				if msg != nil {
					t.Errorf("expected nil, got %+v", msg)
				}
				return
			}
			if msg == nil {
				t.Fatal("expected message, got nil")
			}
			if msg.Role != tt.wantRole {
				t.Errorf("role = %q, want %q", msg.Role, tt.wantRole)
			}
			if msg.Text != tt.wantText {
				t.Errorf("text = %q, want %q", msg.Text, tt.wantText)
			}
		})
	}
}

func TestParser_ParseClaude(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name     string
		line     string
		wantRole string
		wantText string
		wantNil  bool
	}{
		{
			name:     "user message string content",
			line:     `{"type":"user","message":{"content":"User says hello"}}`,
			wantRole: "user",
			wantText: "User says hello",
		},
		{
			name:     "assistant message string content",
			line:     `{"type":"assistant","message":{"content":"I am Claude"}}`,
			wantRole: "assistant",
			wantText: "I am Claude",
		},
		{
			name:     "assistant with array content",
			line:     `{"type":"assistant","message":{"content":[{"type":"text","text":"First block"},{"type":"text","text":"Second block"}]}}`,
			wantRole: "assistant",
			wantText: "First block\nSecond block",
		},
		{
			name:    "system type should be skipped",
			line:    `{"type":"system","message":{"content":"System info"}}`,
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := parser.ParseLine([]byte(tt.line), "claude")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantNil {
				if msg != nil {
					t.Errorf("expected nil, got %+v", msg)
				}
				return
			}
			if msg == nil {
				t.Fatal("expected message, got nil")
			}
			if msg.Role != tt.wantRole {
				t.Errorf("role = %q, want %q", msg.Role, tt.wantRole)
			}
			if msg.Text != tt.wantText {
				t.Errorf("text = %q, want %q", msg.Text, tt.wantText)
			}
		})
	}
}

func TestParser_AutoDetect(t *testing.T) {
	parser := NewParser()

	// Auto-detect Codex
	msg, err := parser.ParseLine([]byte(`{"type":"response_item","payload":{"role":"user","content":"hello"}}`), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg == nil {
		t.Fatal("expected message for codex format")
	}
	if msg.Source != "codex" {
		t.Errorf("source = %q, want codex", msg.Source)
	}

	// Auto-detect Claude
	msg, err = parser.ParseLine([]byte(`{"type":"user","message":{"content":"hello"}}`), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg == nil {
		t.Fatal("expected message for claude format")
	}
	if msg.Source != "claude" {
		t.Errorf("source = %q, want claude", msg.Source)
	}
}
