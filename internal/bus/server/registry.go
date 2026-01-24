package server

import (
	"fmt"
	"sync"
)

// Registry manages registered workspace connections.
// It enforces workspace_uid uniqueness and provides lookup by various keys.
type Registry struct {
	mu sync.RWMutex

	// byID maps connection ID to connection
	byID map[string]*Connection

	// byWorkspaceUID maps workspace_uid to connection
	byWorkspaceUID map[string]*Connection
}

// NewRegistry creates a new connection registry.
func NewRegistry() *Registry {
	return &Registry{
		byID:           make(map[string]*Connection),
		byWorkspaceUID: make(map[string]*Connection),
	}
}

// Register adds a connection to the registry.
// Returns error if workspace_uid is already registered.
func (r *Registry) Register(conn *Connection) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check workspace_uid uniqueness
	if existing, ok := r.byWorkspaceUID[conn.WorkspaceUID]; ok {
		return fmt.Errorf("workspace %s already registered (connection: %s)",
			conn.WorkspaceUID, existing.ID)
	}

	// Check connection ID uniqueness (should always be unique)
	if _, ok := r.byID[conn.ID]; ok {
		return fmt.Errorf("connection %s already exists", conn.ID)
	}

	r.byID[conn.ID] = conn
	if conn.WorkspaceUID != "" {
		r.byWorkspaceUID[conn.WorkspaceUID] = conn
	}

	return nil
}

// Unregister removes a connection from the registry.
func (r *Registry) Unregister(connectionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	conn, ok := r.byID[connectionID]
	if !ok {
		return
	}

	delete(r.byID, connectionID)
	if conn.WorkspaceUID != "" {
		delete(r.byWorkspaceUID, conn.WorkspaceUID)
	}
}

// Get returns a connection by ID.
func (r *Registry) Get(connectionID string) *Connection {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byID[connectionID]
}

// GetByWorkspace returns a connection by workspace_uid.
func (r *Registry) GetByWorkspace(workspaceUID string) *Connection {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byWorkspaceUID[workspaceUID]
}

// HasWorkspace returns true if a workspace is registered.
func (r *Registry) HasWorkspace(workspaceUID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.byWorkspaceUID[workspaceUID]
	return ok
}

// Count returns the number of registered connections.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byID)
}

// All returns all registered connections.
func (r *Registry) All() []*Connection {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*Connection, 0, len(r.byID))
	for _, conn := range r.byID {
		result = append(result, conn)
	}
	return result
}

// AllWorkspaces returns all workspace connections (excluding non-workspace clients).
func (r *Registry) AllWorkspaces() []*Connection {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*Connection, 0, len(r.byWorkspaceUID))
	for _, conn := range r.byWorkspaceUID {
		result = append(result, conn)
	}
	return result
}

// CloseAll closes all connections.
func (r *Registry) CloseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, conn := range r.byID {
		conn.Close()
	}

	r.byID = make(map[string]*Connection)
	r.byWorkspaceUID = make(map[string]*Connection)
}
