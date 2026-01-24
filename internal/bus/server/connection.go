package server

import (
	"net"
	"sync"
	"time"

	"github.com/opencode/hivemind-tmuxcoder/internal/protocol"
)

// Connection represents a client connection to the Bus Server.
type Connection struct {
	// Connection identity
	ID           string
	WorkspaceUID string
	WorkspaceID  string
	Source       protocol.Principal

	// Registration metadata
	PID        int
	ProjectUID string
	DriveMode  string // pty | agent | daemon
	Label      string
	Nonce      string

	// Connection state
	ConnectedAt time.Time
	LastSeen    time.Time
	Status      string // active | draining | closed

	// Network
	netConn net.Conn
	encoder *protocol.StreamEncoder
	decoder *protocol.StreamDecoder

	// Subscriptions
	subscriptions []SubscriptionFilter
	subMu         sync.RWMutex

	// Send queue with backpressure
	sendMu       sync.Mutex
	controlQueue chan *protocol.EventEnvelope
	dataQueue    chan *protocol.EventEnvelope
}

// SubscriptionFilter defines what events a connection wants to receive.
type SubscriptionFilter struct {
	WorkspaceUIDs []string
	Types         []string
	IncludeGlobal bool
	Redaction     string // metadata | full
}

// NewConnection creates a new connection wrapper.
func NewConnection(netConn net.Conn) *Connection {
	return &Connection{
		netConn:      netConn,
		encoder:      protocol.NewStreamEncoder(netConn),
		decoder:      protocol.NewStreamDecoder(netConn),
		Status:       "active",
		controlQueue: make(chan *protocol.EventEnvelope, 128),
		dataQueue:    make(chan *protocol.EventEnvelope, 1024),
	}
}

// Send sends an event to the connection synchronously.
func (c *Connection) Send(event *protocol.EventEnvelope) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	if c.Status == "closed" {
		return net.ErrClosed
	}

	// Set write deadline
	c.netConn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	defer c.netConn.SetWriteDeadline(time.Time{})

	return c.encoder.Encode(event)
}

// Close closes the connection.
func (c *Connection) Close() error {
	c.sendMu.Lock()
	c.Status = "closed"
	c.sendMu.Unlock()

	return c.netConn.Close()
}

// AddSubscription adds a subscription filter.
func (c *Connection) AddSubscription(filter SubscriptionFilter) {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	c.subscriptions = append(c.subscriptions, filter)
}

// ClearSubscriptions removes all subscriptions.
func (c *Connection) ClearSubscriptions() {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	c.subscriptions = nil
}

// GetSubscriptions returns a copy of all subscriptions.
func (c *Connection) GetSubscriptions() []SubscriptionFilter {
	c.subMu.RLock()
	defer c.subMu.RUnlock()
	result := make([]SubscriptionFilter, len(c.subscriptions))
	copy(result, c.subscriptions)
	return result
}

// MatchesSubscription checks if an event matches any of this connection's subscriptions.
func (c *Connection) MatchesSubscription(event *protocol.EventEnvelope) bool {
	c.subMu.RLock()
	defer c.subMu.RUnlock()

	// If no subscriptions, don't match anything
	if len(c.subscriptions) == 0 {
		return false
	}

	for _, filter := range c.subscriptions {
		if filter.Matches(event) {
			return true
		}
	}
	return false
}

// Matches checks if an event matches this subscription filter.
func (f *SubscriptionFilter) Matches(event *protocol.EventEnvelope) bool {
	// Check workspace filter
	if len(f.WorkspaceUIDs) > 0 {
		matched := false
		for _, uid := range f.WorkspaceUIDs {
			if uid == event.WorkspaceUID {
				matched = true
				break
			}
		}
		if !matched {
			// Check global workspace inclusion
			if f.IncludeGlobal && event.WorkspaceUID == protocol.GlobalWorkspaceUID {
				matched = true
			}
		}
		if !matched {
			return false
		}
	}

	// Check type filter
	if len(f.Types) > 0 {
		matched := false
		for _, t := range f.Types {
			if t == event.Type {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	return true
}

// ToWorkspaceInfo converts connection info to WorkspaceInfo for listing.
func (c *Connection) ToWorkspaceInfo() protocol.WorkspaceInfo {
	return protocol.WorkspaceInfo{
		WorkspaceUID:  c.WorkspaceUID,
		WorkspaceID:   c.WorkspaceID,
		Label:         c.Label,
		StatusSummary: c.Status,
	}
}
