package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	bridgeclient "github.com/opencode/hivemind-tmuxcoder/internal/bridge/client"
	"github.com/opencode/hivemind-tmuxcoder/internal/bus/server"
	"github.com/opencode/hivemind-tmuxcoder/internal/ingest/claim"

	_ "modernc.org/sqlite"
)

func parseCSV(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func busSocketPath(socketFlag string) string {
	if socketFlag != "" {
		return socketFlag
	}
	cfg := server.DefaultConfig()
	return cfg.SocketPath
}

func waitForSocket(path string, timeout time.Duration) error {
	if path == "" {
		return fmt.Errorf("socket path is empty")
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("bus socket not ready: %s", filepath.Clean(path))
}

type contextRow struct {
	id   int64
	ts   string
	role string
	text string
}

type contextBlock struct {
	role string
	text string
}

func snapshotDB(path string) (string, func(), error) {
	tempDir, err := os.MkdirTemp("", "tmuxcoder-context-*")
	if err != nil {
		return "", nil, err
	}

	base := filepath.Base(path)
	dstPath := filepath.Join(tempDir, base)

	if err := copyFile(path, dstPath, 0o600); err != nil {
		_ = os.RemoveAll(tempDir)
		return "", nil, err
	}

	if wal := path + "-wal"; fileExists(wal) {
		if err := copyFile(wal, dstPath+"-wal", 0o600); err != nil {
			_ = os.RemoveAll(tempDir)
			return "", nil, err
		}
	}
	if shm := path + "-shm"; fileExists(shm) {
		if err := copyFile(shm, dstPath+"-shm", 0o600); err != nil {
			_ = os.RemoveAll(tempDir)
			return "", nil, err
		}
	}

	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}
	return dstPath, cleanup, nil
}

func copyFile(srcPath, dstPath string, mode os.FileMode) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func readContextRows(path, tmuxSessionID, sessionID, source string) ([]contextRow, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if _, err := db.Exec("PRAGMA busy_timeout=5000;"); err != nil {
		return nil, err
	}

	query := `SELECT id, ts, role, text FROM model_outputs WHERE tmux_session_id = ?`
	args := []interface{}{tmuxSessionID}
	if sessionID != "" {
		query += ` AND session_id = ?`
		args = append(args, sessionID)
	}
	if source != "" {
		query += ` AND source = ?`
		args = append(args, source)
	}
	query += ` ORDER BY ts ASC, id ASC`

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []contextRow
	for rows.Next() {
		var row contextRow
		if err := rows.Scan(&row.id, &row.ts, &row.role, &row.text); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func mergeRoles(rows []contextRow) []contextBlock {
	var merged []contextBlock
	for _, row := range rows {
		if row.text == "" {
			continue
		}
		if len(merged) > 0 && merged[len(merged)-1].role == row.role {
			merged[len(merged)-1].text = merged[len(merged)-1].text + "\n" + row.text
			continue
		}
		merged = append(merged, contextBlock{role: row.role, text: row.text})
	}
	return merged
}

func trimByChars(rows []contextBlock, maxChars int) []contextBlock {
	if maxChars <= 0 {
		return rows
	}
	var out []contextBlock
	total := 0
	for i := len(rows) - 1; i >= 0; i-- {
		block := fmt.Sprintf("[%s]\n%s\n", rows[i].role, rows[i].text)
		if total+len(block) > maxChars && len(out) > 0 {
			break
		}
		out = append(out, rows[i])
		total += len(block)
	}
	for i := 0; i < len(out)/2; i++ {
		out[i], out[len(out)-1-i] = out[len(out)-1-i], out[i]
	}
	return out
}

func writeContextText(w io.Writer, rows []contextBlock) {
	for i, row := range rows {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "[%s]\n%s\n", row.role, row.text)
	}
}

func writeContextJSON(w io.Writer, rows []contextBlock) error {
	type item struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	out := make([]item, 0, len(rows))
	for _, row := range rows {
		out = append(out, item{Role: row.role, Content: row.text})
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}

func writeContextReadAudit(dbPath, tmuxSessionID, sessionID, source string, rawRows, selectedRows, mergedRows int, firstID, lastID int64, firstTS, lastTS string, limit, maxChars int) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	if _, err := db.Exec("PRAGMA busy_timeout=5000;"); err != nil {
		return err
	}

	if err := ensureContextReadsTable(db); err != nil {
		return err
	}

	var sessionVal interface{}
	if sessionID != "" {
		sessionVal = sessionID
	}
	var firstIDVal interface{}
	var lastIDVal interface{}
	var firstTSVal interface{}
	var lastTSVal interface{}
	if firstID != 0 || lastID != 0 {
		firstIDVal = firstID
		lastIDVal = lastID
	}
	if firstTS != "" || lastTS != "" {
		firstTSVal = firstTS
		lastTSVal = lastTS
	}

	_, err = db.Exec(
		`INSERT INTO context_reads (
			ts, tmux_session_id, session_id, source, rows, max_chars, limit_rows,
			raw_rows, selected_rows, merged_rows, first_id, last_id, first_ts, last_ts
		) VALUES (datetime('now'), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tmuxSessionID,
		sessionVal,
		source,
		mergedRows,
		maxChars,
		limit,
		rawRows,
		selectedRows,
		mergedRows,
		firstIDVal,
		lastIDVal,
		firstTSVal,
		lastTSVal,
	)
	return err
}

func ensureContextReadsTable(db *sql.DB) error {
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
	return ensureContextReadsColumns(db)
}

func resolveTmuxSessionID(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if value := strings.TrimSpace(os.Getenv("TMUXCODER_SESSION_ID")); value != "" {
		return value, nil
	}
	path := filepath.Join(userHomeOrEmpty(), ".tmuxcoder", "current_session_id")
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			value := strings.TrimSpace(string(data))
			if value != "" {
				return value, nil
			}
		}
	}
	return "", fmt.Errorf("tmux_session_id is required (use --tmux-session-id, TMUXCODER_SESSION_ID, or ~/.tmuxcoder/current_session_id)")
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

const rulesMarkerStart = "<!-- tmuxcoder:shared-context-rules:start -->"
const rulesMarkerEnd = "<!-- tmuxcoder:shared-context-rules:end -->"

const rulesTemplate = `<!-- tmuxcoder:shared-context-rules:start -->
## Tmuxcoder Shared Context Rules

You MUST fetch shared context by running exactly one command:
tmuxcoder context

--source is optional (codex|claude). Omit it to get context from all sources.
Do NOT run discovery commands (ls/rg/strings/--help). Do NOT search for TMUXCODER_ROOT.
If tmux_session_id is needed, pass --tmux-session-id or ask the user to set TMUXCODER_SESSION_ID.
Then prepend the fetched context to the prompt and answer the user.
<!-- tmuxcoder:shared-context-rules:end -->
`

func writeRulesTemplates(files []string) error {
	for _, name := range files {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if err := writeRulesTemplate(name); err != nil {
			return err
		}
	}
	return nil
}

func writeRulesTemplate(path string) error {
	target := path
	if !filepath.IsAbs(target) {
		if cwd, err := os.Getwd(); err == nil {
			target = filepath.Join(cwd, path)
		}
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}

	existing, err := os.ReadFile(target)
	if err == nil {
		content := string(existing)
		if strings.Contains(content, rulesMarkerStart) && strings.Contains(content, rulesMarkerEnd) {
			updated, ok := replaceRulesBlock(content)
			if !ok {
				return nil
			}
			return os.WriteFile(target, []byte(updated), 0o644)
		}
		sep := "\n"
		if strings.HasSuffix(content, "\n") {
			sep = ""
		}
		return os.WriteFile(target, []byte(content+sep+"\n"+rulesTemplate), 0o644)
	}
	if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(target, []byte(rulesTemplate), 0o644)
}

func replaceRulesBlock(content string) (string, bool) {
	start := strings.Index(content, rulesMarkerStart)
	end := strings.Index(content, rulesMarkerEnd)
	if start == -1 || end == -1 || end < start {
		return content, false
	}
	end += len(rulesMarkerEnd)
	return content[:start] + rulesTemplate + content[end:], true
}

func printBusStatus(socketFlag string) {
	socketPath := busSocketPath(socketFlag)
	fmt.Println("bus:")
	fmt.Printf("  socket: %s\n", socketPath)
	if socketPath == "" {
		fmt.Println("  status: unknown")
		return
	}
	if _, err := os.Stat(socketPath); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("  status: missing socket")
			return
		}
		fmt.Printf("  status: error (%v)\n", err)
		return
	}
	if err := canRegisterBus(socketPath, 800*time.Millisecond); err != nil {
		fmt.Printf("  status: not responding (%v)\n", err)
		return
	}
	fmt.Println("  status: ok")
}

func printIngestStatus(claimDirFlag string) {
	claimDir := claimDirFlag
	if claimDir == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			claimDir = filepath.Join(home, claim.DefaultClaimDir)
		}
	}
	claimDir = expandHome(claimDir)
	fmt.Println("ingest:")
	fmt.Printf("  claim_dir: %s\n", claimDir)
	entries, err := os.ReadDir(claimDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("  claims: none")
			return
		}
		fmt.Printf("  claims: error (%v)\n", err)
		return
	}

	var claimsInfo []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".lock" {
			continue
		}
		lockPath := filepath.Join(claimDir, entry.Name())
		sessionID := strings.TrimSuffix(entry.Name(), ".lock")
		info, alive, err := readClaim(lockPath)
		if err != nil {
			claimsInfo = append(claimsInfo, fmt.Sprintf("    - %s: error (%v)", sessionID, err))
			continue
		}
		status := "stale"
		if alive {
			status = "alive"
		}
		claimsInfo = append(claimsInfo, fmt.Sprintf("    - %s: pid=%d %s start=%s host=%s", sessionID, info.PID, status, info.StartTS, info.Hostname))
	}

	if len(claimsInfo) == 0 {
		fmt.Println("  claims: none")
		return
	}
	fmt.Println("  claims:")
	for _, line := range claimsInfo {
		fmt.Println(line)
	}
}

func printSinkStatus(dbPathFlag, tmuxSessionID string) {
	dbPath := dbPathFlag
	if dbPath == "" {
		dbPath = defaultStatusDBPath()
	}
	dbPath = expandHome(dbPath)

	fmt.Println("sink:")
	fmt.Printf("  db_path: %s\n", dbPath)

	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("  status: missing db")
			return
		}
		fmt.Printf("  status: error (%v)\n", err)
		return
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		fmt.Printf("  status: error (%v)\n", err)
		return
	}
	defer db.Close()

	totalRows := queryInt(db, `SELECT COUNT(1) FROM model_outputs`)
	totalSessions := queryInt(db, `SELECT COUNT(1) FROM tmux_sessions`)
	lastTS := queryString(db, `SELECT MAX(ts) FROM model_outputs`)

	fmt.Printf("  model_outputs: %d rows\n", totalRows)
	fmt.Printf("  tmux_sessions: %d\n", totalSessions)
	if lastTS != "" {
		fmt.Printf("  last_ts: %s\n", lastTS)
	}

	currentID, source := resolveStatusTmuxSessionID(tmuxSessionID)
	if currentID != "" {
		fmt.Printf("  current_tmux_session_id: %s (%s)\n", currentID, source)
		sessionRows := queryInt(db, `SELECT COUNT(1) FROM model_outputs WHERE tmux_session_id = ?`, currentID)
		fmt.Printf("  session_rows: %d\n", sessionRows)
		createdAt := queryString(db, `SELECT created_at FROM tmux_sessions WHERE tmux_session_id = ?`, currentID)
		if createdAt != "" {
			fmt.Printf("  session_created_at: %s\n", createdAt)
		}
	}
}

func canRegisterBus(path string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cfg := bridgeclient.DefaultConfig()
	cfg.SocketPath = path
	cfg.Label = "tmuxcoder-status"
	cfg.DriveMode = "status"
	client := bridgeclient.New(cfg)

	oldOutput := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(oldOutput)

	if err := client.Connect(ctx); err != nil {
		return err
	}
	return client.Close()
}

func readClaim(lockPath string) (claim.LockInfo, bool, error) {
	var info claim.LockInfo
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return info, false, err
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return info, false, err
	}
	alive := processExists(info.PID)
	return info, alive, nil
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func queryInt(db *sql.DB, query string, args ...interface{}) int {
	var value int
	if err := db.QueryRow(query, args...).Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0
		}
		return 0
	}
	return value
}

func queryString(db *sql.DB, query string, args ...interface{}) string {
	var value sql.NullString
	if err := db.QueryRow(query, args...).Scan(&value); err != nil {
		return ""
	}
	if !value.Valid {
		return ""
	}
	return value.String
}

func resolveStatusTmuxSessionID(explicit string) (string, string) {
	if explicit != "" {
		return explicit, "flag"
	}
	if value := strings.TrimSpace(os.Getenv("TMUXCODER_SESSION_ID")); value != "" {
		return value, "env"
	}
	path := filepath.Join(userHomeOrEmpty(), ".tmuxcoder", "current_session_id")
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			value := strings.TrimSpace(string(data))
			if value != "" {
				return value, "file"
			}
		}
	}
	return "", ""
}

func defaultStatusDBPath() string {
	home := userHomeOrEmpty()
	if home == "" {
		return "./tmuxcoder-model-outputs.db"
	}
	return filepath.Join(home, ".tmuxcoder", "model_outputs.db")
}

func resolveDBPath(explicit string) (string, error) {
	if explicit != "" {
		return expandHome(explicit), nil
	}
	if value := strings.TrimSpace(os.Getenv("TMUXCODER_DB_PATH")); value != "" {
		return expandHome(value), nil
	}
	if value := readDBPathFile(); value != "" {
		return expandHome(value), nil
	}
	path := defaultStatusDBPath()
	if path == "" {
		return "", fmt.Errorf("db path is required")
	}
	return expandHome(path), nil
}

func writeDBPathFile(value string) error {
	path := dbPathFilePath()
	if path == "" {
		return fmt.Errorf("db path file is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.TrimSpace(value)), 0o600)
}

func readDBPathFile() string {
	path := dbPathFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func dbPathFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".tmuxcoder", "current_db_path")
}

func userHomeOrEmpty() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

func listSkillDirs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var skills []string
	for _, entry := range entries {
		if entry.IsDir() {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			skills = append(skills, name)
		}
	}
	return skills, nil
}

func installSkills(srcRoot, dstRoot string, skillNames []string, force bool) error {
	if err := os.MkdirAll(dstRoot, 0o755); err != nil {
		return err
	}
	for _, name := range skillNames {
		src := filepath.Join(srcRoot, name)
		dst := filepath.Join(dstRoot, name)
		if _, err := os.Stat(dst); err == nil {
			if !force {
				return fmt.Errorf("skill already exists: %s (use --force to overwrite)", dst)
			}
			if err := os.RemoveAll(dst); err != nil {
				return err
			}
		}
		if err := copyDir(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", src)
	}
	if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		entryInfo, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, data, entryInfo.Mode().Perm()); err != nil {
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

func listSkillDirsFS(fsys fs.FS, root string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, err
	}
	var skills []string
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			skills = append(skills, entry.Name())
		}
	}
	return skills, nil
}

func installSkillsFromFS(fsys fs.FS, srcRoot, dstRoot string, names []string, force bool) error {
	if err := os.MkdirAll(dstRoot, 0o755); err != nil {
		return err
	}
	for _, name := range names {
		src := srcRoot + "/" + name
		dst := filepath.Join(dstRoot, name)
		if _, err := os.Stat(dst); err == nil {
			if !force {
				return fmt.Errorf("skill already exists: %s (use --force to overwrite)", dst)
			}
			if err := os.RemoveAll(dst); err != nil {
				return err
			}
		}
		if err := copyDirFromFS(fsys, src, dst); err != nil {
			return err
		}
	}
	return nil
}

func copyDirFromFS(fsys fs.FS, src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := fs.ReadDir(fsys, src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := src + "/" + entry.Name()
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyDirFromFS(fsys, srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		data, err := fs.ReadFile(fsys, srcPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func ensureSkillInstalled(skillName, targetDir string) error {
	dst := filepath.Join(targetDir, skillName)
	if _, err := os.Stat(dst); err == nil {
		return nil // already installed
	}
	return installSkillsFromFS(embeddedSkills, "skills", targetDir, []string{skillName}, false)
}

func writeSessionFile(value string) error {
	path := sessionFilePath()
	if path == "" {
		return fmt.Errorf("session file path is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.TrimSpace(value)), 0o600)
}

func sessionFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".tmuxcoder", "current_session_id")
}
