package sink

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	bridgeclient "github.com/opencode/hivemind-tmuxcoder/internal/bridge/client"
	"github.com/opencode/hivemind-tmuxcoder/internal/protocol"

	_ "modernc.org/sqlite"
)

type Config struct {
	SocketPath    string
	DBPath        string
	IncludeGlobal bool
	Sources       []string
	Rebuild       bool
	TmuxSessionID string
}

type logEvent struct {
	ts           string
	workspaceUID string
	source       string
	role         string
	sessionID    string
	text         string
}

type writer struct {
	stmt          *sql.Stmt
	tmuxSessionID string
}

func Run(ctx context.Context, cfg Config) error {
	dbPath := cfg.DBPath
	if dbPath == "" {
		dbPath = defaultDBPath()
	}
	resolvedDBPath := expandHome(dbPath)
	if err := ensureDir(filepath.Dir(resolvedDBPath)); err != nil {
		return fmt.Errorf("failed to create db directory: %w", err)
	}

	db, err := sql.Open("sqlite", resolvedDBPath)
	if err != nil {
		return fmt.Errorf("failed to open sqlite db: %w", err)
	}
	defer db.Close()

	if err := initDB(db, cfg.Rebuild); err != nil {
		return fmt.Errorf("failed to init db: %w", err)
	}

	stmt, err := db.Prepare(`INSERT OR IGNORE INTO model_outputs
		(ts, source, role, session_id, tmux_session_id, workspace_uid, text)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("failed to prepare insert: %w", err)
	}
	defer stmt.Close()

	sourceFilter := parseFilter(cfg.Sources)

	clientCfg := bridgeclient.DefaultConfig()
	if cfg.SocketPath != "" {
		clientCfg.SocketPath = cfg.SocketPath
	}
	clientCfg.Label = "tmuxcoder-sink"
	clientCfg.DriveMode = "sink"

	client := bridgeclient.New(clientCfg)
	if err := client.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to bus: %w", err)
	}
	defer client.Close()

	eventCh := make(chan logEvent, 256)
	var wg sync.WaitGroup
	var stopped atomic.Bool

	tmuxSessionID, err := resolveTmuxSessionID(cfg.TmuxSessionID)
	if err != nil {
		return err
	}

	if err := registerTmuxSession(db, tmuxSessionID); err != nil {
		return err
	}

	w := &writer{
		stmt:          stmt,
		tmuxSessionID: tmuxSessionID,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-eventCh:
				if !ok {
					return
				}
				w.insertRow(ev)
			}
		}
	}()

	client.OnEvent(protocol.TypeBusSubscribeResponse, func(*protocol.EventEnvelope) {})
	client.OnEvent(protocol.TypeUILogAppend, func(event *protocol.EventEnvelope) {
		if stopped.Load() {
			return
		}
		var payload protocol.UILogAppendPayload
		if err := protocol.UnmarshalPayload(event.Payload, &payload); err != nil {
			return
		}
		source, role, text, ok := parseLogText(payload.Text)
		if !ok {
			return
		}
		if !isRoleAllowed(role) {
			return
		}
		if !isSourceAllowed(source, sourceFilter) {
			return
		}

		eventCh <- logEvent{
			ts:           event.Timestamp,
			workspaceUID: event.WorkspaceUID,
			source:       source,
			role:         role,
			sessionID:    payload.SessionID,
			text:         text,
		}
	})

	if err := client.Subscribe(nil, []string{protocol.TypeUILogAppend}, cfg.IncludeGlobal); err != nil {
		return fmt.Errorf("subscribe failed: %w", err)
	}

	<-ctx.Done()
	stopped.Store(true)
	wg.Wait()
	return nil
}

func (w *writer) insertRow(ev logEvent) {
	_, err := w.stmt.Exec(ev.ts, ev.source, ev.role, nullIfEmpty(ev.sessionID), w.tmuxSessionID, ev.workspaceUID, ev.text)
	if err != nil {
		log.Printf("insert failed: %v", err)
	}
}

func parseLogText(text string) (string, string, string, bool) {
	trimmed := strings.TrimLeft(text, "\n")
	if trimmed == "" {
		return "", "", "", false
	}
	if strings.HasPrefix(trimmed, "[ingest:") {
		return "", "", "", false
	}

	parts := strings.SplitN(trimmed, "\n", 2)
	label := strings.TrimSpace(parts[0])
	if !strings.HasPrefix(label, "[") || !strings.HasSuffix(label, "]") {
		return "", "", "", false
	}

	label = strings.TrimSuffix(strings.TrimPrefix(label, "["), "]")
	labelParts := strings.Split(label, ":")
	var source, role string
	if len(labelParts) == 1 {
		role = strings.TrimSpace(labelParts[0])
	} else {
		source = strings.TrimSpace(labelParts[0])
		role = strings.TrimSpace(labelParts[len(labelParts)-1])
	}

	body := ""
	if len(parts) > 1 {
		body = strings.TrimRight(parts[1], "\n")
	}

	return source, role, body, true
}

func isRoleAllowed(role string) bool {
	switch role {
	case "user", "assistant":
		return true
	default:
		return false
	}
}

func parseFilter(sources []string) map[string]bool {
	result := make(map[string]bool)
	for _, part := range sources {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		result[part] = true
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func isSourceAllowed(source string, filter map[string]bool) bool {
	if filter == nil {
		return true
	}
	return filter[source]
}

func initDB(db *sql.DB, rebuild bool) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA temp_store=MEMORY;",
		"PRAGMA busy_timeout=5000;",
	}
	for _, stmt := range pragmas {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}

	if rebuild {
		if _, err := db.Exec(`DROP TABLE IF EXISTS model_outputs;`); err != nil {
			return err
		}
	}

	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS model_outputs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts TEXT NOT NULL,
		source TEXT NOT NULL,
		role TEXT NOT NULL,
		session_id TEXT,
		tmux_session_id TEXT,
		workspace_uid TEXT,
		text TEXT NOT NULL
	);`)
	if err != nil {
		return err
	}

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS tmux_sessions (
		tmux_session_id TEXT PRIMARY KEY,
		created_at TEXT NOT NULL
	);`); err != nil {
		return err
	}

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS context_reads (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts TEXT NOT NULL,
		tmux_session_id TEXT,
		session_id TEXT,
		source TEXT,
		rows INTEGER,
		max_chars INTEGER,
		limit_rows INTEGER,
		raw_rows INTEGER,
		selected_rows INTEGER,
		merged_rows INTEGER,
		first_id INTEGER,
		last_id INTEGER,
		first_ts TEXT,
		last_ts TEXT
	);`); err != nil {
		return err
	}

	if err := ensureContextReadsColumns(db); err != nil {
		return err
	}

	if _, err := db.Exec(`ALTER TABLE model_outputs ADD COLUMN tmux_session_id TEXT;`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}

	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_model_outputs_ts ON model_outputs(ts);",
		"CREATE INDEX IF NOT EXISTS idx_model_outputs_source ON model_outputs(source);",
		"CREATE INDEX IF NOT EXISTS idx_model_outputs_session ON model_outputs(session_id);",
		"CREATE INDEX IF NOT EXISTS idx_model_outputs_tmux_session ON model_outputs(tmux_session_id);",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_model_outputs_dedup ON model_outputs(ts, source, role, session_id, tmux_session_id, text);",
	}
	for _, stmt := range indexes {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func ensureContextReadsColumns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(context_reads);`)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, colType string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notnull, &dflt, &pk); err != nil {
			return err
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	columns := map[string]string{
		"raw_rows":      "INTEGER",
		"selected_rows": "INTEGER",
		"merged_rows":   "INTEGER",
		"first_id":      "INTEGER",
		"last_id":       "INTEGER",
		"first_ts":      "TEXT",
		"last_ts":       "TEXT",
	}

	for name, colType := range columns {
		if existing[name] {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf("ALTER TABLE context_reads ADD COLUMN %s %s;", name, colType)); err != nil {
			return err
		}
	}

	return nil
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

func ensureDir(dir string) error {
	if dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o700)
}

func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "./tmuxcoder-model-outputs.db"
	}
	return filepath.Join(home, ".tmuxcoder", "model_outputs.db")
}

func registerTmuxSession(db *sql.DB, tmuxSessionID string) error {
	if tmuxSessionID == "" {
		return fmt.Errorf("tmux_session_id is required")
	}
	_, err := db.Exec(
		`INSERT OR IGNORE INTO tmux_sessions (tmux_session_id, created_at) VALUES (?, datetime('now'))`,
		tmuxSessionID,
	)
	return err
}

func resolveTmuxSessionID(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if value := strings.TrimSpace(os.Getenv("TMUXCODER_SESSION_ID")); value != "" {
		return value, nil
	}
	if value := readSessionFile(); value != "" {
		return value, nil
	}
	return "", fmt.Errorf("tmux_session_id is required (use --tmux-session-id, TMUXCODER_SESSION_ID, or ~/.tmuxcoder/current_session_id)")
}

func readSessionFile() string {
	path := defaultSessionFile()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func defaultSessionFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".tmuxcoder", "current_session_id")
}

func nullIfEmpty(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}
