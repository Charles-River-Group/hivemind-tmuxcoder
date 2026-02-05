// Package ingest provides JSONL parsing for Codex and Claude session files.
package ingest

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ParsedMessage represents a parsed message from agent JSONL.
type ParsedMessage struct {
	Role      string // "user" or "assistant"
	Text      string // The extracted message text
	SessionID string // Session ID if available
	Source    string // "codex" or "claude"
}

// Parser parses JSONL lines from different agent formats.
type Parser struct{}

// NewParser creates a new parser.
func NewParser() *Parser {
	return &Parser{}
}

// ParseLine parses a single JSONL line and returns a message if relevant.
// Returns nil if the line should be skipped (not a user/assistant message).
func (p *Parser) ParseLine(line []byte, source string) (*ParsedMessage, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	switch strings.ToLower(source) {
	case "codex":
		return p.parseCodex(raw)
	case "claude":
		return p.parseClaude(raw)
	default:
		// Auto-detect based on content
		if msg := p.tryParseCodex(raw); msg != nil {
			return msg, nil
		}
		if msg := p.tryParseClaude(raw); msg != nil {
			return msg, nil
		}
		return nil, nil // Skip non-matching lines
	}
}

// parseCodex parses Codex JSONL format.
// Codex format: type == "response_item" && payload.role in {"user", "assistant"}
// Text is in payload.content (string or []block with type=="input_text"/"output_text")
func (p *Parser) parseCodex(raw map[string]interface{}) (*ParsedMessage, error) {
	return p.tryParseCodex(raw), nil
}

func (p *Parser) tryParseCodex(raw map[string]interface{}) *ParsedMessage {
	// Check type == "response_item"
	typ, _ := raw["type"].(string)
	if typ != "response_item" {
		return nil
	}

	// Get payload
	payload, ok := raw["payload"].(map[string]interface{})
	if !ok {
		return nil
	}

	// Check role
	role, _ := payload["role"].(string)
	if role != "user" && role != "assistant" {
		return nil
	}

	// Extract text from content
	text := p.extractCodexContent(payload["content"])
	if text == "" {
		return nil
	}

	msg := &ParsedMessage{
		Role:   role,
		Text:   text,
		Source: "codex",
	}

	// Extract session ID if available
	if sessionID, ok := raw["session_id"].(string); ok {
		msg.SessionID = sessionID
	}

	return msg
}

// extractCodexContent extracts text from Codex content field.
func (p *Parser) extractCodexContent(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, block := range v {
			if blockMap, ok := block.(map[string]interface{}); ok {
				blockType, _ := blockMap["type"].(string)
				if blockType == "input_text" || blockType == "output_text" {
					if text, ok := blockMap["text"].(string); ok {
						parts = append(parts, text)
					}
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// parseClaude parses Claude JSONL format.
// Claude format: type in {"user", "assistant"}
// Text is in message.content (string or []block with type=="text")
func (p *Parser) parseClaude(raw map[string]interface{}) (*ParsedMessage, error) {
	return p.tryParseClaude(raw), nil
}

func (p *Parser) tryParseClaude(raw map[string]interface{}) *ParsedMessage {
	// Check type is user or assistant
	typ, _ := raw["type"].(string)
	if typ != "user" && typ != "assistant" {
		return nil
	}

	// Get message
	message, ok := raw["message"].(map[string]interface{})
	if !ok {
		// Some Claude formats have content directly on the object
		text := p.extractClaudeContent(raw["content"])
		if text == "" {
			return nil
		}
		msg := &ParsedMessage{
			Role:   typ,
			Text:   text,
			Source: "claude",
		}
		if sessionID, ok := raw["session_id"].(string); ok {
			msg.SessionID = sessionID
		}
		return msg
	}

	// Extract text from message.content
	text := p.extractClaudeContent(message["content"])
	if text == "" {
		return nil
	}

	msg := &ParsedMessage{
		Role:   typ,
		Text:   text,
		Source: "claude",
	}

	// Extract session ID if available
	if sessionID, ok := raw["session_id"].(string); ok {
		msg.SessionID = sessionID
	} else if sessionID, ok := message["session_id"].(string); ok {
		msg.SessionID = sessionID
	}

	return msg
}

// extractClaudeContent extracts text from Claude content field.
func (p *Parser) extractClaudeContent(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, block := range v {
			if blockMap, ok := block.(map[string]interface{}); ok {
				blockType, _ := blockMap["type"].(string)
				if blockType == "text" {
					if text, ok := blockMap["text"].(string); ok {
						parts = append(parts, text)
					}
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// DetectSource tries to detect the source (codex/claude) from content.
func (p *Parser) DetectSource(raw map[string]interface{}) string {
	// Codex has type="response_item" with payload
	if typ, _ := raw["type"].(string); typ == "response_item" {
		if _, ok := raw["payload"].(map[string]interface{}); ok {
			return "codex"
		}
	}
	// Claude has type="user" or "assistant" with message
	if typ, _ := raw["type"].(string); typ == "user" || typ == "assistant" {
		return "claude"
	}
	return ""
}
