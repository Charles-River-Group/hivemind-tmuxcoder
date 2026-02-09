package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	bridgeclient "github.com/opencode/hivemind-tmuxcoder/internal/bridge/client"
	"github.com/opencode/hivemind-tmuxcoder/internal/protocol"

	_ "modernc.org/sqlite"
)

type logEvent struct {
	ts           string
	workspaceUID string
	source       string
	role         string
	sessionID    string
	text         string
}

type sinkWriter struct {
	db   *sql.DB
	stmt *sql.Stmt
}

func main() {
	var (
		socketPath    string
		dbPath        string
		includeGlobal bool
		sourcesRaw    string
	)

	flag.StringVar(&socketPath, "socket", "", "Bus server socket path (default: auto-detect)")
	flag.StringVar(&dbPath, "db-path", defaultDBPath(), "SQLite DB path")
	flag.BoolVar(&includeGlobal, "include-global", false, "Include global bus events")
	flag.StringVar(&sourcesRaw, "sources", "codex,claude", "Comma-separated sources to keep (empty = all)")
	flag.Parse()

	sourceFilter := parseFilter(sourcesRaw)

	resolvedDBPath := expandHome(dbPath)
	if err := ensureDir(filepath.Dir(resolvedDBPath)); err != nil {
		log.Fatalf("failed to create db directory: %v", err)
	}

	db, err := sql.Open("sqlite", resolvedDBPath)
	if err != nil {
		log.Fatalf("failed to open sqlite db: %v", err)
	}
	defer db.Close()

	if err := initDB(db); err != nil {
		log.Fatalf("failed to init db: %v", err)
	}

	stmt, err := db.Prepare(`INSERT INTO model_outputs
		(ts, source, role, session_id, workspace_uid, text)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		log.Fatalf("failed to prepare insert: %v", err)
	}
	defer stmt.Close()

	writer := &sinkWriter{
		db:   db,
		stmt: stmt,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	clientCfg := bridgeclient.DefaultConfig()
	if socketPath != "" {
		clientCfg.SocketPath = socketPath
	}
	clientCfg.Label = "tmuxcoder-sink"
	clientCfg.DriveMode = "sink"

	client := bridgeclient.New(clientCfg)
	if err := client.Connect(ctx); err != nil {
		log.Fatalf("failed to connect to bus: %v", err)
	}
	defer client.Close()

	eventCh := make(chan logEvent, 256)
	var wg sync.WaitGroup
	var stopped atomic.Bool

	wg.Add(1)
	go func() {
		defer wg.Done()
		writer.run(ctx, eventCh)
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

	if err := client.Subscribe(nil, []string{protocol.TypeUILogAppend}, includeGlobal); err != nil {
		log.Fatalf("subscribe failed: %v", err)
	}

	<-ctx.Done()
	stopped.Store(true)
	wg.Wait()
}

func (w *sinkWriter) run(ctx context.Context, ch <-chan logEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			w.insertRow(ev)
		}
	}
}

func (w *sinkWriter) insertRow(ev logEvent) {
	_, err := w.stmt.Exec(ev.ts, ev.source, ev.role, nullIfEmpty(ev.sessionID), ev.workspaceUID, ev.text)
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

func parseFilter(raw string) map[string]bool {
	result := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
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

func initDB(db *sql.DB) error {
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

	if _, err := db.Exec(`DROP TABLE IF EXISTS turns;`); err != nil {
		return err
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS model_outputs;`); err != nil {
		return err
	}

	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS model_outputs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts TEXT NOT NULL,
		source TEXT NOT NULL,
		role TEXT NOT NULL,
		session_id TEXT,
		workspace_uid TEXT,
		text TEXT NOT NULL
	);`)
	if err != nil {
		return err
	}

	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_model_outputs_ts ON model_outputs(ts);",
		"CREATE INDEX IF NOT EXISTS idx_model_outputs_source ON model_outputs(source);",
		"CREATE INDEX IF NOT EXISTS idx_model_outputs_session ON model_outputs(session_id);",
	}
	for _, stmt := range indexes {
		if _, err := db.Exec(stmt); err != nil {
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

func nullIfEmpty(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}
