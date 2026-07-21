package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const (
	DefaultDBDir  = ".joycode-proxy"
	DefaultDBName = "proxy.db"
	encKeyFile    = ".enc_key"
)

// AccountCreds holds per-account credential fields that are persisted alongside pt_key.
// These are injected into the joycode.Client at request time via SetColorContext.
// When nil or empty, the client falls back to DB global settings then hardcoded defaults.
type AccountCreds struct {
	LoginType     string `json:"login_type"`
	Tenant        string `json:"tenant"`
	ColorBaseURL  string `json:"color_base_url"`
	MasterBaseURL string `json:"master_base_url"`
	OrgFullName   string `json:"org_full_name"`
}

type Account struct {
	UserID        string `json:"user_id"`
	Nickname      string `json:"nickname"`
	Remark        string `json:"remark"`
	APIToken      string `json:"api_token"`
	PtKey         string `json:"-"`
	IsDefault     bool   `json:"is_default"`
	DefaultModel  string `json:"default_model"`
	CreatedAt     string `json:"created_at,omitempty"`
	LoginType     string `json:"login_type"`
	Tenant        string `json:"tenant"`
	ColorBaseURL  string `json:"color_base_url"`
	MasterBaseURL string `json:"master_base_url"`
	OrgFullName   string `json:"org_full_name"`
}

func (a *Account) DisplayName() string {
	if a.Remark != "" {
		return a.Remark
	}
	if a.Nickname != "" {
		return a.Nickname
	}
	return a.UserID
}

type AccountInfo struct {
	UserID              string `json:"user_id"`
	Nickname            string `json:"nickname"`
	Remark              string `json:"remark"`
	APIToken            string `json:"api_token"`
	IsDefault           bool   `json:"is_default"`
	DefaultModel        string `json:"default_model"`
	CreatedAt           string `json:"created_at,omitempty"`
	DisplayOrder        int    `json:"display_order"`
	ActiveSessions      int64  `json:"active_sessions"`
	TotalRequests       int    `json:"total_requests"`
	TodayRequests       int    `json:"today_requests"`
	TotalTokens         int    `json:"total_tokens"`
	TodayTokens         int    `json:"today_tokens"`
	CredentialValid     int    `json:"credential_valid"` // -1=unknown, 0=expired, 1=valid
	CredentialCheckedAt string `json:"credential_checked_at,omitempty"`
	CredentialRefreshAt string `json:"credential_refreshed_at,omitempty"`
	CredentialError     string `json:"credential_error,omitempty"`
	LoginType           string `json:"login_type"`
	Tenant              string `json:"tenant"`
	ColorBaseURL        string `json:"color_base_url"`
	MasterBaseURL       string `json:"master_base_url"`
	OrgFullName         string `json:"org_full_name"`
	ColorRefreshAt      string `json:"color_refresh_at,omitempty"`
	ColorRefreshError   string `json:"color_refresh_error,omitempty"`
}

func (a *AccountInfo) DisplayName() string {
	if a.Remark != "" {
		return a.Remark
	}
	if a.Nickname != "" {
		return a.Nickname
	}
	return a.UserID
}

type Stats struct {
	TotalRequests int            `json:"total_requests"`
	TotalInputTk  int            `json:"total_input_tokens"`
	TotalOutputTk int            `json:"total_output_tokens"`
	AccountsCount int            `json:"accounts_count"`
	AvgLatencyMs  float64        `json:"avg_latency_ms"`
	ErrorCount    int            `json:"error_count"`
	StreamCount   int            `json:"stream_count"`
	SuccessCount  int            `json:"success_count"`
	ByModel       []ModelCount   `json:"by_model"`
	ByAccount     []AccountCount `json:"by_account"`
}

type ModelCount struct {
	Model string `json:"model"`
	Count int    `json:"count"`
}

type AccountCount struct {
	UserID   string `json:"user_id"`
	Nickname string `json:"nickname"`
	Remark   string `json:"remark"`
	Count    int    `json:"count"`
}

func (a *AccountCount) DisplayName() string {
	if a.Remark != "" {
		return a.Remark
	}
	if a.Nickname != "" {
		return a.Nickname
	}
	return a.UserID
}

type AccountStats struct {
	UserID        string          `json:"user_id"`
	Nickname      string          `json:"nickname"`
	Remark        string          `json:"remark"`
	TotalRequests int             `json:"total_requests"`
	TotalInputTk  int             `json:"total_input_tokens"`
	TotalOutputTk int             `json:"total_output_tokens"`
	SuccessCount  int             `json:"success_count"`
	StreamCount   int             `json:"stream_count"`
	ByModel       []ModelCount    `json:"by_model"`
	ByEndpoint    []EndpointCount `json:"by_endpoint"`
	AvgLatencyMs  float64         `json:"avg_latency_ms"`
	ErrorCount    int             `json:"error_count"`
	AllTime       *AllTimeTotals  `json:"all_time"`
	Hourly        []HourlyData    `json:"hourly"`
}

type EndpointCount struct {
	Endpoint string `json:"endpoint"`
	Count    int    `json:"count"`
}

type AllTimeTotals struct {
	TotalRequests int `json:"total_requests"`
	TotalInputTk  int `json:"total_input_tokens"`
	TotalOutputTk int `json:"total_output_tokens"`
	ErrorCount    int `json:"error_count"`
}

type HourlyData struct {
	Hour         string `json:"hour"`
	Count        int    `json:"count"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	Errors       int    `json:"errors"`
}

type RequestLog struct {
	ID           int64  `json:"id"`
	UserID       string `json:"user_id"`
	Model        string `json:"model"`
	Endpoint     string `json:"endpoint"`
	Stream       bool   `json:"stream"`
	StatusCode   int    `json:"status_code"`
	LatencyMs    int64  `json:"latency_ms"`
	ErrorMessage string `json:"error_message"`
	ErrorDetail string `json:"error_detail"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	CreatedAt    string `json:"created_at"`
}

type Store struct {
	db     *sql.DB
	enc    cipher.AEAD
	mu     sync.Mutex
	dbPath string
}

func DefaultDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, DefaultDBDir)
	return filepath.Join(dir, DefaultDBName), nil
}

func Open(dbPath string) (*Store, error) {
	if dbPath == "" {
		var err error
		dbPath, err = DefaultDBPath()
		if err != nil {
			return nil, err
		}
	}

	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	s := &Store{db: db, dbPath: dbPath}

	encKey, err := s.loadOrCreateEncKey(dir)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("encryption key: %w", err)
	}

	block, err := aes.NewCipher(encKey)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	s.enc, err = cipher.NewGCM(block)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func generateToken() string {
	b := make([]byte, 32)
	io.ReadFull(rand.Reader, b)
	return "sk-joy-" + hex.EncodeToString(b)
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS accounts (
			user_id TEXT PRIMARY KEY,
			nickname TEXT DEFAULT '',
			remark TEXT DEFAULT '',
			api_token TEXT NOT NULL DEFAULT '',
			pt_key TEXT NOT NULL,
			is_default INTEGER DEFAULT 0,
			default_model TEXT DEFAULT '',
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now')),
			credential_refreshed_at TEXT DEFAULT '',
			credential_valid INTEGER DEFAULT -1,
			display_order INTEGER DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TEXT DEFAULT (datetime('now'))
		);
		CREATE TABLE IF NOT EXISTS request_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			api_key TEXT,
			model TEXT,
			endpoint TEXT,
			stream INTEGER DEFAULT 0,
			status_code INTEGER,
			latency_ms INTEGER,
			created_at TEXT DEFAULT (datetime('now'))
		);
	`)
	if err != nil {
		return err
	}

	// Create remote_configs table (idempotent)
	s.db.Exec(`CREATE TABLE IF NOT EXISTS remote_configs (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at TEXT DEFAULT (datetime('now'))
	)`)

	// Migration: add error_message column to request_logs
	s.db.Exec("ALTER TABLE request_logs ADD COLUMN error_message TEXT DEFAULT ''")

	// Migration: add token columns to request_logs
	s.db.Exec("ALTER TABLE request_logs ADD COLUMN input_tokens INTEGER DEFAULT 0")
	s.db.Exec("ALTER TABLE request_logs ADD COLUMN output_tokens INTEGER DEFAULT 0")

	// Migration: add display_order column to accounts
	s.db.Exec("ALTER TABLE accounts ADD COLUMN display_order INTEGER DEFAULT 0")

	// Migration: add per-account credential fields
	s.db.Exec("ALTER TABLE accounts ADD COLUMN login_type TEXT DEFAULT ''")
	s.db.Exec("ALTER TABLE accounts ADD COLUMN tenant TEXT DEFAULT ''")
	s.db.Exec("ALTER TABLE accounts ADD COLUMN color_base_url TEXT DEFAULT ''")
	s.db.Exec("ALTER TABLE accounts ADD COLUMN master_base_url TEXT DEFAULT ''")
	s.db.Exec("ALTER TABLE accounts ADD COLUMN org_full_name TEXT DEFAULT ''")

	// Migration: add per-account colorBaseUrl refresh status
	s.db.Exec("ALTER TABLE accounts ADD COLUMN color_refresh_at TEXT DEFAULT ''")
	s.db.Exec("ALTER TABLE accounts ADD COLUMN color_refresh_error TEXT DEFAULT ''")

	// Migration: migrate old schema (api_key as PK) to new schema (user_id as PK)
	s.migrateUserIDAsPK()

	// Migration: convert historical local-wall-clock timestamps to UTC storage
	s.migrateToUTC()

	// Migration: rewrite column defaults that still use datetime('now','localtime')
	// to UTC datetime('now'). CREATE TABLE IF NOT EXISTS never updates an existing
	// table's column defaults, so tables created before the UTC switch keep writing
	// server-local wall-clock on inserts that rely on the default.
	s.migrateColumnDefaultsToUTC()

	// Migration: correct rows that were written as local wall-clock AFTER the
	// one-shot migrateToUTC ran (via the stale localtime column default). Those
	// rows read as future instants once the rest of the base is UTC.
	s.migrateFutureTimestampsToUTC()

	// Migration: initialize display_order for existing accounts
	s.migrateDisplayOrder()

	// Migration: add error_detail column to request_logs
	s.db.Exec("ALTER TABLE request_logs ADD COLUMN error_detail TEXT DEFAULT ''")

	// Index for cursor-paginated account log queries
	// (WHERE api_key=? [AND ...] ORDER BY id DESC). Created last so it survives
	// migrateColumnDefaultsToUTC, which rebuilds request_logs and would otherwise
	// drop an earlier index.
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_request_logs_api_key_id ON request_logs (api_key, id DESC)")

	return nil
}

// migrateUserIDAsPK migrates the old accounts table (api_key as PK) to the new
// schema where user_id is the primary key. If the table already uses user_id as
// PK this is a no-op.
func (s *Store) migrateUserIDAsPK() {
	// Check if migration is needed: old schema has api_key as PK
	var colName string
	err := s.db.QueryRow("SELECT name FROM pragma_table_info('accounts') WHERE pk = 1").Scan(&colName)
	if err != nil {
		slog.Error("store: migrateUserIDAsPK pragma check failed", "error", err)
		return
	}
	if colName == "user_id" {
		// Already on new schema
		return
	}
	slog.Info("store: migrating accounts table from api_key PK to user_id PK")

	// Create new table
	_, err = s.db.Exec(`
		CREATE TABLE accounts_new (
			user_id TEXT PRIMARY KEY,
			nickname TEXT DEFAULT '',
			remark TEXT DEFAULT '',
			api_token TEXT NOT NULL DEFAULT '',
			pt_key TEXT NOT NULL,
			is_default INTEGER DEFAULT 0,
			default_model TEXT DEFAULT '',
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now')),
			credential_refreshed_at TEXT DEFAULT '',
			credential_valid INTEGER DEFAULT -1,
			display_order INTEGER DEFAULT 0
				login_type TEXT DEFAULT '',
				tenant TEXT DEFAULT '',
				color_base_url TEXT DEFAULT '',
				master_base_url TEXT DEFAULT '',
				org_full_name TEXT DEFAULT ''
		)`)
	if err != nil {
		slog.Error("store: migrateUserIDAsPK create accounts_new failed", "error", err)
		return
	}

	// Copy data from old table:
	//   user_id = COALESCE(NULLIF(old.user_id, ''), 'local_' || old.api_key)
	//   nickname = old.api_key (the old display name becomes the nickname)
	//   api_token, pt_key, is_default, default_model, created_at, updated_at,
	//   credential_refreshed_at, credential_valid carried over
	_, err = s.db.Exec(`
		INSERT INTO accounts_new (user_id, nickname, api_token, pt_key, is_default, default_model, created_at, updated_at, credential_refreshed_at, credential_valid, display_order)
		SELECT
			CASE WHEN user_id = '' OR user_id IS NULL THEN 'local_' || api_key ELSE user_id END,
			api_key,
			COALESCE(api_token, ''),
			pt_key,
			is_default,
			COALESCE(default_model, ''),
			created_at,
			COALESCE(updated_at, created_at),
			COALESCE(credential_refreshed_at, ''),
			COALESCE(credential_valid, -1),
			COALESCE(display_order, 0)
		FROM accounts`)
	if err != nil {
		slog.Error("store: migrateUserIDAsPK copy data failed", "error", err)
		return
	}

	// Build a mapping of old api_key -> new user_id from the old table for log migration
	type mapping struct {
		oldAPIKey string
		newUserID string
	}
	rows, err := s.db.Query(`
		SELECT api_key,
			CASE WHEN user_id = '' OR user_id IS NULL THEN 'local_' || api_key ELSE user_id END
		FROM accounts`)
	if err == nil {
		var mappings []mapping
		for rows.Next() {
			var m mapping
			if rows.Scan(&m.oldAPIKey, &m.newUserID) == nil {
				mappings = append(mappings, m)
			}
		}
		rows.Close()

		// Migrate request_logs.api_key from old display names to new user_ids
		for _, m := range mappings {
			if m.oldAPIKey != m.newUserID {
				s.db.Exec("UPDATE request_logs SET api_key = ? WHERE api_key = ?", m.newUserID, m.oldAPIKey)
			}
		}
	}

	// Swap tables
	_, err = s.db.Exec("DROP TABLE accounts")
	if err != nil {
		slog.Error("store: migrateUserIDAsPK drop old table failed", "error", err)
		return
	}
	_, err = s.db.Exec("ALTER TABLE accounts_new RENAME TO accounts")
	if err != nil {
		slog.Error("store: migrateUserIDAsPK rename new table failed", "error", err)
		return
	}
	slog.Info("store: accounts table migrated to user_id PK successfully")
}

// migrateToUTC converts historical timestamps that were stored as server
// local wall-clock (the old `datetime('now','localtime')` scheme) into UTC,
// so all rows share a single absolute time base that the dashboard can render
// in any configured display timezone.
//
// Runs exactly once, guarded by the settings flag `schema_tz_utc_migrated`.
// The flag write and the UPDATEs share one transaction, so a crash mid-way
// never leaves rows half-shifted or the flag set without the shift applied.
func (s *Store) migrateToUTC() {
	if s.GetSetting("schema_tz_utc_migrated") == "1" {
		return
	}

	_, offset := time.Now().Zone()
	hours := offset / 3600

	tx, err := s.db.Begin()
	if err != nil {
		slog.Error("store: migrateToUTC begin tx failed", "error", err)
		return
	}
	defer tx.Rollback()

	// hours == 0 (server already on UTC): nothing to shift, just set the flag.
	if hours != 0 {
		// Subtract the server offset to recover the UTC instant from the stored
		// local wall-clock string. datetime(col, '-N hours') accepts negative N.
		shift := fmt.Sprintf("%+d hours", -hours)
		stmts := []string{
			"UPDATE request_logs SET created_at = datetime(created_at, '" + shift + "')",
			"UPDATE accounts SET created_at = datetime(created_at, '" + shift + "'), updated_at = datetime(updated_at, '" + shift + "')",
			"UPDATE settings SET updated_at = datetime(updated_at, '" + shift + "')",
			"UPDATE remote_configs SET updated_at = datetime(updated_at, '" + shift + "')",
		}
		for _, q := range stmts {
			if _, err := tx.Exec(q); err != nil {
				slog.Error("store: migrateToUTC update failed", "query", q, "error", err)
				return
			}
		}
	}

	if _, err := tx.Exec(
		"INSERT OR REPLACE INTO settings (key, value, updated_at) VALUES ('schema_tz_utc_migrated', '1', datetime('now'))",
	); err != nil {
		slog.Error("store: migrateToUTC set flag failed", "error", err)
		return
	}
	if err := tx.Commit(); err != nil {
		slog.Error("store: migrateToUTC commit failed", "error", err)
		return
	}
	slog.Info("store: migrated local-wall-clock timestamps to UTC", "offset_hours", hours)
}

// tzTables lists tables whose timestamp column defaults must be UTC.
var tzTables = []string{"request_logs", "accounts", "settings", "remote_configs"}

// migrateColumnDefaultsToUTC rewrites any column default of the form
// datetime('now','localtime') to datetime('now') (UTC) on the tracked tables.
//
// SQLite cannot alter a column default in place, so each affected table is
// rebuilt: the table's own CREATE SQL is fetched from sqlite_master, the
// localtime modifier is stripped, and the table is recreated and repopulated
// inside a transaction. Tables already free of the localtime default are left
// untouched, which also makes this idempotent.
func (s *Store) migrateColumnDefaultsToUTC() {
	for _, table := range tzTables {
		var createSQL string
		err := s.db.QueryRow(
			"SELECT sql FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&createSQL)
		if err != nil {
			// Table may not exist yet on a fresh db; nothing to fix.
			continue
		}
		if !strings.Contains(createSQL, "localtime") {
			continue
		}
		if err := s.rebuildTableWithUTCDefaults(table, createSQL); err != nil {
			slog.Error("store: migrateColumnDefaultsToUTC failed", "table", table, "error", err)
		}
	}
}

// rebuildTableWithUTCDefaults recreates table using createSQL with the
// datetime(...,'localtime') defaults rewritten to UTC, copying all existing
// rows across by their shared column names.
func (s *Store) rebuildTableWithUTCDefaults(table, createSQL string) error {
	// Normalize both spacing variants of the localtime modifier to plain UTC.
	fixedSQL := createSQL
	for _, from := range []string{"datetime('now', 'localtime')", "datetime('now','localtime')"} {
		fixedSQL = strings.ReplaceAll(fixedSQL, from, "datetime('now')")
	}
	// Recreate under a temporary name, then swap. The stored SQL may reference a
	// different original name than the current table (e.g. after a prior ALTER
	// TABLE RENAME), so rewrite whatever identifier follows "CREATE TABLE" up to
	// the opening parenthesis rather than assuming it equals table.
	tmp := table + "_utc_tmp"
	openParen := strings.Index(fixedSQL, "(")
	const createKw = "CREATE TABLE "
	kwIdx := strings.Index(fixedSQL, createKw)
	if kwIdx < 0 || openParen < 0 || openParen < kwIdx {
		return fmt.Errorf("could not parse CREATE statement for %s", table)
	}
	newSQL := createKw + tmp + " " + fixedSQL[openParen:]

	cols, err := s.tableColumns(table)
	if err != nil {
		return err
	}
	colList := strings.Join(cols, ", ")

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(newSQL); err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	if _, err := tx.Exec(fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s", tmp, colList, colList, table)); err != nil {
		return fmt.Errorf("copy into %s: %w", tmp, err)
	}
	if _, err := tx.Exec("DROP TABLE " + table); err != nil {
		return fmt.Errorf("drop %s: %w", table, err)
	}
	if _, err := tx.Exec(fmt.Sprintf("ALTER TABLE %s RENAME TO %s", tmp, table)); err != nil {
		return fmt.Errorf("rename %s: %w", tmp, err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	slog.Info("store: rebuilt table with UTC column defaults", "table", table)
	return nil
}

// tableColumns returns the column names of table in schema order.
func (s *Store) tableColumns(table string) ([]string, error) {
	rows, err := s.db.Query("SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols = append(cols, name)
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("no columns for table %s", table)
	}
	return cols, nil
}

// migrateFutureTimestampsToUTC corrects rows whose timestamps were written as
// server-local wall-clock through the stale localtime column default AFTER the
// one-shot migrateToUTC ran. Because the rest of the base is UTC, those rows
// sit in the future; each is shifted back by the server's UTC offset.
//
// Runs once, guarded by the settings flag `schema_future_ts_corrected`. A
// server already on UTC has no local/UTC skew, so it only sets the flag.
func (s *Store) migrateFutureTimestampsToUTC() {
	if s.GetSetting("schema_future_ts_corrected") == "1" {
		return
	}

	_, offset := time.Now().Zone()
	hours := offset / 3600

	tx, err := s.db.Begin()
	if err != nil {
		slog.Error("store: migrateFutureTimestampsToUTC begin tx failed", "error", err)
		return
	}
	defer tx.Rollback()

	if hours != 0 {
		shift := fmt.Sprintf("%+d hours", -hours)
		// A small buffer past 'now' avoids touching a row legitimately written
		// microseconds ago whose second-resolution string rounds just ahead.
		const buffer = "+2 minutes"
		type target struct{ table, col string }
		targets := []target{
			{"request_logs", "created_at"},
			{"accounts", "created_at"},
			{"accounts", "updated_at"},
			{"settings", "updated_at"},
			{"remote_configs", "updated_at"},
		}
		for _, tg := range targets {
			q := fmt.Sprintf(
				"UPDATE %s SET %s = datetime(%s, '%s') WHERE %s > datetime('now', '%s')",
				tg.table, tg.col, tg.col, shift, tg.col, buffer,
			)
			if _, err := tx.Exec(q); err != nil {
				slog.Error("store: migrateFutureTimestampsToUTC update failed", "query", q, "error", err)
				return
			}
		}
	}

	if _, err := tx.Exec(
		"INSERT OR REPLACE INTO settings (key, value, updated_at) VALUES ('schema_future_ts_corrected', '1', datetime('now'))",
	); err != nil {
		slog.Error("store: migrateFutureTimestampsToUTC set flag failed", "error", err)
		return
	}
	if err := tx.Commit(); err != nil {
		slog.Error("store: migrateFutureTimestampsToUTC commit failed", "error", err)
		return
	}
	slog.Info("store: corrected future (local-wall-clock) timestamps to UTC", "offset_hours", hours)
}

func (s *Store) migrateDisplayOrder() {
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM accounts WHERE display_order = 0").Scan(&count)
	if count == 0 {
		return
	}
	slog.Info("store: initializing display_order for existing accounts", "count", count)
	rows, err := s.db.Query("SELECT user_id FROM accounts ORDER BY created_at")
	if err != nil {
		slog.Error("store: migrateDisplayOrder query failed", "error", err)
		return
	}
	defer rows.Close()
	order := 1
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			continue
		}
		s.db.Exec("UPDATE accounts SET display_order = ? WHERE user_id = ?", order, userID)
		order++
	}
}

// --- Encryption ---

func (s *Store) loadOrCreateEncKey(dir string) ([]byte, error) {
	keyPath := filepath.Join(dir, encKeyFile)
	data, err := os.ReadFile(keyPath)
	if err == nil {
		key, err := hex.DecodeString(string(data))
		if err == nil && len(key) == 32 {
			return key, nil
		}
	}

	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	if err := os.WriteFile(keyPath, []byte(hex.EncodeToString(key)), 0600); err != nil {
		return nil, fmt.Errorf("write key: %w", err)
	}
	return key, nil
}

func (s *Store) encrypt(plaintext string) (string, error) {
	nonce := make([]byte, s.enc.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := s.enc.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), nil
}

func (s *Store) decrypt(ciphertext string) (string, error) {
	data, err := hex.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	nonceSize := s.enc.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ct := data[:nonceSize], data[nonceSize:]
	plaintext, err := s.enc.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// --- Account CRUD ---

const MaxAccounts = 10

func (s *Store) AddAccount(userID, ptKey, nickname string, isDefault bool, defaultModel string, creds *AccountCreds) error {
	if userID == "" {
		return fmt.Errorf("user_id cannot be empty")
	}
	if ptKey == "" {
		return fmt.Errorf("pt_key cannot be empty")
	}

	// Extract credential fields; nil creds means empty strings (backward compat)
	var cLoginType, cTenant, cColorBaseURL, cMasterBaseURL, cOrgFullName string
	if creds != nil {
		cLoginType = creds.LoginType
		cTenant = creds.Tenant
		cColorBaseURL = creds.ColorBaseURL
		cMasterBaseURL = creds.MasterBaseURL
		cOrgFullName = creds.OrgFullName
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if account already exists — updates bypass the limit
	var existingToken string
	err := s.db.QueryRow("SELECT api_token FROM accounts WHERE user_id = ?", userID).Scan(&existingToken)
	if err == nil {
		encPtKey, err := s.encrypt(ptKey)
		if err != nil {
			slog.Error("store: encrypt pt_key failed", "user_id", userID, "error", err)
			return fmt.Errorf("encrypt pt_key: %w", err)
		}
		_, err = s.db.Exec(
			"UPDATE accounts SET pt_key = ?, nickname = CASE WHEN nickname = '' OR nickname IS NULL THEN ? ELSE nickname END, login_type = ?, tenant = ?, color_base_url = ?, master_base_url = ?, org_full_name = ?, updated_at = datetime('now') WHERE user_id = ?",
			encPtKey, nickname, cLoginType, cTenant, cColorBaseURL, cMasterBaseURL, cOrgFullName, userID,
		)
		if err != nil {
			slog.Error("store: update account failed", "user_id", userID, "error", err)
			return err
		}
		slog.Info("store: updated existing account credentials", "user_id", userID)
		return nil
	}

	// Check if another account already has the same pt_key (dedup by credential)
	rows, err := s.db.Query("SELECT user_id, pt_key FROM accounts")
	if err == nil {
		for rows.Next() {
			var existingUserID, encExistingPtKey string
			if rows.Scan(&existingUserID, &encExistingPtKey) != nil {
				continue
			}
			existingPtKey, decErr := s.decrypt(encExistingPtKey)
			if decErr != nil {
				continue
			}
			if existingPtKey == ptKey {
				rows.Close()
				encPtKey, encErr := s.encrypt(ptKey)
				if encErr != nil {
					slog.Error("store: encrypt pt_key failed", "user_id", userID, "error", encErr)
					return fmt.Errorf("encrypt pt_key: %w", encErr)
				}
				_, err = s.db.Exec(
					"UPDATE accounts SET user_id = ?, pt_key = ?, nickname = CASE WHEN nickname = '' OR nickname IS NULL THEN ? ELSE nickname END, login_type = ?, tenant = ?, color_base_url = ?, master_base_url = ?, org_full_name = ?, updated_at = datetime('now') WHERE user_id = ?",
					userID, encPtKey, nickname, cLoginType, cTenant, cColorBaseURL, cMasterBaseURL, cOrgFullName, existingUserID,
				)
				if err != nil {
					slog.Error("store: update account (pt_key dedup) failed", "old_user_id", existingUserID, "new_user_id", userID, "error", err)
					return err
				}
				slog.Info("store: merged account by pt_key dedup", "old_user_id", existingUserID, "new_user_id", userID)
				return nil
			}
		}
		rows.Close()
	}

	// New account — enforce limit
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM accounts").Scan(&count)
	if count >= MaxAccounts {
		return fmt.Errorf("账号数量已达上限（%d 个）。本工具仅供个人学习和研究使用，禁止用于商业转售、API 中转服务或任何违法违规用途", MaxAccounts)
	}

	encPtKey, err := s.encrypt(ptKey)
	if err != nil {
		slog.Error("store: encrypt pt_key failed", "user_id", userID, "error", err)
		return fmt.Errorf("encrypt pt_key: %w", err)
	}

	// New account
	if isDefault {
		s.db.Exec("UPDATE accounts SET is_default = 0 WHERE is_default = 1")
	}

	def := 0
	if isDefault {
		def = 1
	}

	// Get max display_order
	var maxOrder int
	s.db.QueryRow("SELECT COALESCE(MAX(display_order), 0) FROM accounts").Scan(&maxOrder)

	token := generateToken()
	_, err = s.db.Exec(
		"INSERT INTO accounts (user_id, nickname, api_token, pt_key, is_default, default_model, display_order, login_type, tenant, color_base_url, master_base_url, org_full_name) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		userID, nickname, token, encPtKey, def, defaultModel, maxOrder+1, cLoginType, cTenant, cColorBaseURL, cMasterBaseURL, cOrgFullName,
	)
	if err != nil {
		slog.Error("store: add account failed", "user_id", userID, "error", err)
		return err
	}
	return nil
}

func (s *Store) ListAccounts() ([]AccountInfo, error) {
	rows, err := s.db.Query("SELECT user_id, nickname, remark, api_token, is_default, default_model, created_at, credential_valid, credential_refreshed_at, COALESCE(display_order, 0), COALESCE(login_type,''), COALESCE(tenant,''), COALESCE(color_base_url,''), COALESCE(master_base_url,''), COALESCE(org_full_name,''), COALESCE(color_refresh_at,''), COALESCE(color_refresh_error,'') FROM accounts ORDER BY display_order, created_at")
	if err != nil {
		slog.Error("store: list accounts query failed", "error", err)
		return nil, err
	}
	defer rows.Close()

	var accounts []AccountInfo
	for rows.Next() {
		var a AccountInfo
		var isDef int
		if err := rows.Scan(&a.UserID, &a.Nickname, &a.Remark, &a.APIToken, &isDef, &a.DefaultModel, &a.CreatedAt, &a.CredentialValid, &a.CredentialRefreshAt, &a.DisplayOrder, &a.LoginType, &a.Tenant, &a.ColorBaseURL, &a.MasterBaseURL, &a.OrgFullName, &a.ColorRefreshAt, &a.ColorRefreshError); err != nil {
			slog.Error("store: list accounts scan failed", "error", err)
			return nil, err
		}
		a.IsDefault = isDef == 1
		a.CredentialCheckedAt = a.CredentialRefreshAt
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

// FillAccountStats populates request/token statistics for each account using batch queries.
func (s *Store) FillAccountStats(accounts []AccountInfo) {
	if len(accounts) == 0 {
		return
	}

	// All-time stats: single GROUP BY query
	allRows, err := s.db.Query(`
		SELECT api_key,
			COUNT(*) as req_count,
			COALESCE(SUM(input_tokens + output_tokens), 0) as token_sum
		FROM request_logs
		GROUP BY api_key`)
	if err != nil {
		return
	}
	allMap := make(map[string][2]int)
	for allRows.Next() {
		var key string
		var reqCount, tokenSum int
		if allRows.Scan(&key, &reqCount, &tokenSum) == nil {
			allMap[key] = [2]int{reqCount, tokenSum}
		}
	}
	allRows.Close()

	// Today stats: single GROUP BY query, bucketed in the display timezone
	todayFilter := "date(created_at) = date('now')"
	if mod := s.tzOffsetModifier(); mod != "" {
		todayFilter = "date(created_at, '" + mod + "') = date('now', '" + mod + "')"
	}
	todayRows, err := s.db.Query(`
		SELECT api_key,
			COUNT(*) as req_count,
			COALESCE(SUM(input_tokens + output_tokens), 0) as token_sum
		FROM request_logs
		WHERE ` + todayFilter + `
		GROUP BY api_key`)
	if err != nil {
		return
	}
	todayMap := make(map[string][2]int)
	for todayRows.Next() {
		var key string
		var reqCount, tokenSum int
		if todayRows.Scan(&key, &reqCount, &tokenSum) == nil {
			todayMap[key] = [2]int{reqCount, tokenSum}
		}
	}
	todayRows.Close()

	for i := range accounts {
		if v, ok := allMap[accounts[i].UserID]; ok {
			accounts[i].TotalRequests = v[0]
			accounts[i].TotalTokens = v[1]
		}
		if v, ok := todayMap[accounts[i].UserID]; ok {
			accounts[i].TodayRequests = v[0]
			accounts[i].TodayTokens = v[1]
		}
	}
}

func (s *Store) GetAccount(userID string) (*Account, error) {
	var a Account
	var encPtKey string
	var isDef int
	err := s.db.QueryRow(
		"SELECT user_id, nickname, remark, api_token, pt_key, is_default, default_model, created_at, COALESCE(login_type,''), COALESCE(tenant,''), COALESCE(color_base_url,''), COALESCE(master_base_url,''), COALESCE(org_full_name,'') FROM accounts WHERE user_id = ?",
		userID,
	).Scan(&a.UserID, &a.Nickname, &a.Remark, &a.APIToken, &encPtKey, &isDef, &a.DefaultModel, &a.CreatedAt, &a.LoginType, &a.Tenant, &a.ColorBaseURL, &a.MasterBaseURL, &a.OrgFullName)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		slog.Error("store: get account query failed", "user_id", userID, "error", err)
		return nil, err
	}

	ptKey, err := s.decrypt(encPtKey)
	if err != nil {
		slog.Error("store: decrypt pt_key failed", "user_id", userID, "error", err)
		return nil, fmt.Errorf("decrypt pt_key: %w", err)
	}
	a.PtKey = ptKey
	a.IsDefault = isDef == 1
	return &a, nil
}

func (s *Store) GetAccountByToken(token string) (*Account, error) {
	var a Account
	var encPtKey string
	var isDef int
	err := s.db.QueryRow(
		"SELECT user_id, nickname, remark, api_token, pt_key, is_default, default_model, created_at, COALESCE(login_type,''), COALESCE(tenant,''), COALESCE(color_base_url,''), COALESCE(master_base_url,''), COALESCE(org_full_name,'') FROM accounts WHERE api_token = ?",
		token,
	).Scan(&a.UserID, &a.Nickname, &a.Remark, &a.APIToken, &encPtKey, &isDef, &a.DefaultModel, &a.CreatedAt, &a.LoginType, &a.Tenant, &a.ColorBaseURL, &a.MasterBaseURL, &a.OrgFullName)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		slog.Error("store: get account by token query failed", "error", err)
		return nil, err
	}

	ptKey, err := s.decrypt(encPtKey)
	if err != nil {
		slog.Error("store: decrypt pt_key by token failed", "error", err)
		return nil, fmt.Errorf("decrypt pt_key: %w", err)
	}
	a.PtKey = ptKey
	a.IsDefault = isDef == 1
	return &a, nil
}

func (s *Store) RenewToken(userID string) (string, error) {
	token := generateToken()
	_, err := s.db.Exec("UPDATE accounts SET api_token = ?, updated_at = datetime('now') WHERE user_id = ?", token, userID)
	if err != nil {
		slog.Error("store: renew token failed", "user_id", userID, "error", err)
		return "", err
	}
	return token, nil
}

func (s *Store) GetDefaultAccount() (*Account, error) {
	var a Account
	var encPtKey string
	err := s.db.QueryRow(
		"SELECT user_id, nickname, remark, api_token, pt_key, is_default, default_model, created_at, COALESCE(login_type,''), COALESCE(tenant,''), COALESCE(color_base_url,''), COALESCE(master_base_url,''), COALESCE(org_full_name,'') FROM accounts WHERE is_default = 1 LIMIT 1",
	).Scan(&a.UserID, &a.Nickname, &a.Remark, &a.APIToken, &encPtKey, new(int), &a.DefaultModel, &a.CreatedAt, &a.LoginType, &a.Tenant, &a.ColorBaseURL, &a.MasterBaseURL, &a.OrgFullName)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		slog.Error("store: get default account query failed", "error", err)
		return nil, err
	}

	ptKey, err := s.decrypt(encPtKey)
	if err != nil {
		slog.Error("store: decrypt default account pt_key failed", "error", err)
		return nil, fmt.Errorf("decrypt pt_key: %w", err)
	}
	a.PtKey = ptKey
	a.IsDefault = true
	return &a, nil
}

func (s *Store) ReorderAccounts(userIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for i, uid := range userIDs {
		if _, err := tx.Exec("UPDATE accounts SET display_order = ? WHERE user_id = ?", i+1, uid); err != nil {
			return fmt.Errorf("update display_order for %s: %w", uid, err)
		}
	}
	return tx.Commit()
}

func (s *Store) RemoveAccount(userID string) error {
	_, err := s.db.Exec("DELETE FROM accounts WHERE user_id = ?", userID)
	if err != nil {
		slog.Error("store: remove account failed", "user_id", userID, "error", err)
	}
	return err
}

func (s *Store) ClearAllAccounts() (int, error) {
	result, err := s.db.Exec("DELETE FROM accounts")
	if err != nil {
		slog.Error("store: clear all accounts failed", "error", err)
		return 0, err
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

// UpdatePtKey updates the encrypted pt_key for an account.
func (s *Store) UpdatePtKey(userID, ptKey string) error {
	encPtKey, err := s.encrypt(ptKey)
	if err != nil {
		slog.Error("store: encrypt pt_key for update failed", "user_id", userID, "error", err)
		return fmt.Errorf("encrypt pt_key: %w", err)
	}
	result, err := s.db.Exec(
		"UPDATE accounts SET pt_key = ?, updated_at = datetime('now'), credential_refreshed_at = datetime('now') WHERE user_id = ?",
		encPtKey, userID,
	)
	if err != nil {
		slog.Error("store: update pt_key failed", "user_id", userID, "error", err)
		return err
	}
	rows, _ := result.RowsAffected()
	slog.Info("store: pt_key updated",
		"user_id", userID,
		"rows_affected", rows,
	)
	return nil
}

// UpdateCredentialRefreshedAt sets credential_refreshed_at to now for an account
// that was validated but did not need a pt_key refresh.
func (s *Store) UpdateCredentialRefreshedAt(userID string) {
	s.db.Exec(
		"UPDATE accounts SET credential_refreshed_at = datetime('now') WHERE user_id = ?",
		userID,
	)
}

// SetCredentialValid updates the credential_valid status for an account.
func (s *Store) SetCredentialValid(userID string, valid bool) {
	v := 0
	if valid {
		v = 1
	}
	s.db.Exec("UPDATE accounts SET credential_valid = ? WHERE user_id = ?", v, userID)
}

// ListStaleAccounts returns accounts that need credential checking.
// Valid accounts (credential_valid=1) use the normal threshold.
// Failed accounts (credential_valid=0) use a 4x longer backoff threshold.
// Unknown accounts (credential_valid=-1) or never-refreshed are always included.
func (s *Store) ListStaleAccounts(threshold time.Duration) ([]Account, error) {
	normalCutoff := time.Now().Add(-threshold).Format("2006-01-02 15:04:05")
	backoffCutoff := time.Now().Add(-threshold * 4).Format("2006-01-02 15:04:05")
	rows, err := s.db.Query(
		`SELECT user_id, nickname, pt_key, default_model FROM accounts
		 WHERE credential_refreshed_at = ''
		    OR credential_valid = -1
		    OR (credential_valid = 1 AND credential_refreshed_at < ?)
		    OR (credential_valid = 0 AND credential_refreshed_at < ?)
		 ORDER BY created_at`,
		normalCutoff, backoffCutoff,
	)
	if err != nil {
		slog.Error("store: list stale accounts query failed", "error", err)
		return nil, err
	}
	defer rows.Close()

	var accounts []Account
	for rows.Next() {
		var a Account
		var encPtKey string
		if err := rows.Scan(&a.UserID, &a.Nickname, &encPtKey, &a.DefaultModel); err != nil {
			slog.Error("store: list stale accounts scan failed", "error", err)
			return nil, err
		}
		ptKey, err := s.decrypt(encPtKey)
		if err != nil {
			slog.Error("store: decrypt pt_key failed for stale account", "user_id", a.UserID, "error", err)
			continue
		}
		a.PtKey = ptKey
		accounts = append(accounts, a)
	}
	return accounts, nil
}

// ListAllAccountsWithCredentials returns all accounts with decrypted pt_keys.
func (s *Store) ListAllAccountsWithCredentials() ([]Account, error) {
	rows, err := s.db.Query("SELECT user_id, nickname, pt_key, default_model FROM accounts ORDER BY created_at")
	if err != nil {
		slog.Error("store: list accounts with credentials query failed", "error", err)
		return nil, err
	}
	defer rows.Close()

	var accounts []Account
	for rows.Next() {
		var a Account
		var encPtKey string
		if err := rows.Scan(&a.UserID, &a.Nickname, &encPtKey, &a.DefaultModel); err != nil {
			slog.Error("store: list accounts with credentials scan failed", "error", err)
			return nil, err
		}
		ptKey, err := s.decrypt(encPtKey)
		if err != nil {
			slog.Error("store: decrypt pt_key failed for keepalive", "user_id", a.UserID, "error", err)
			continue
		}
		a.PtKey = ptKey
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

// UpdateRemark updates the remark for an account.
func (s *Store) UpdateRemark(userID, remark string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.Exec("UPDATE accounts SET remark = ?, updated_at = datetime('now') WHERE user_id = ?", remark, userID)
	if err != nil {
		slog.Error("store: update remark failed", "user_id", userID, "error", err)
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("account %q not found", userID)
	}
	return nil
}

func (s *Store) SetDefault(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		slog.Error("store: set default begin tx failed", "user_id", userID, "error", err)
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("UPDATE accounts SET is_default = 0, updated_at = datetime('now')"); err != nil {
		slog.Error("store: set default clear failed", "error", err)
		return err
	}
	if _, err := tx.Exec("UPDATE accounts SET is_default = 1, updated_at = datetime('now') WHERE user_id = ?", userID); err != nil {
		slog.Error("store: set default assign failed", "user_id", userID, "error", err)
		return err
	}
	return tx.Commit()
}

func (s *Store) UpdateAccountModel(userID, model string) error {
	_, err := s.db.Exec(
		"UPDATE accounts SET default_model = ?, updated_at = datetime('now') WHERE user_id = ?",
		model, userID,
	)
	if err != nil {
		slog.Error("store: update account model failed", "user_id", userID, "model", model, "error", err)
	}
	return err
}

// UpdateAccountCreds updates the per-account credential fields for a given user.
// Non-empty fields in creds overwrite the stored values; empty fields are left unchanged.
func (s *Store) UpdateAccountCreds(userID string, creds *AccountCreds) error {
	if creds == nil {
		return nil
	}
	_, err := s.db.Exec(
		"UPDATE accounts SET login_type = CASE WHEN ? != '' THEN ? ELSE login_type END, tenant = CASE WHEN ? != '' THEN ? ELSE tenant END, color_base_url = CASE WHEN ? != '' THEN ? ELSE color_base_url END, master_base_url = CASE WHEN ? != '' THEN ? ELSE master_base_url END, org_full_name = CASE WHEN ? != '' THEN ? ELSE org_full_name END, updated_at = datetime('now') WHERE user_id = ?",
		creds.LoginType, creds.LoginType,
		creds.Tenant, creds.Tenant,
		creds.ColorBaseURL, creds.ColorBaseURL,
		creds.MasterBaseURL, creds.MasterBaseURL,
		creds.OrgFullName, creds.OrgFullName,
		userID,
	)
	if err != nil {
		slog.Error("store: update account creds failed", "user_id", userID, "error", err)
	}
	return err
}

// UpdateAccountColorStatus records the outcome of a colorBaseUrl refresh attempt
// for an account. refreshErr is empty on success (which clears any prior error).
func (s *Store) UpdateAccountColorStatus(userID, refreshAt, refreshErr string) error {
	_, err := s.db.Exec(
		"UPDATE accounts SET color_refresh_at = ?, color_refresh_error = ? WHERE user_id = ?",
		refreshAt, refreshErr, userID,
	)
	if err != nil {
		slog.Error("store: update account color status failed", "user_id", userID, "error", err)
	}
	return err
}

// GetAccountColorStatus returns the last colorBaseUrl refresh time and error for an account.
func (s *Store) GetAccountColorStatus(userID string) (refreshAt, refreshErr string, err error) {
	err = s.db.QueryRow(
		"SELECT COALESCE(color_refresh_at,''), COALESCE(color_refresh_error,'') FROM accounts WHERE user_id = ?",
		userID,
	).Scan(&refreshAt, &refreshErr)
	return refreshAt, refreshErr, err
}

// --- Settings ---

func (s *Store) GetSettings() (map[string]string, error) {
	rows, err := s.db.Query("SELECT key, value FROM settings")
	if err != nil {
		slog.Error("store: get settings query failed", "error", err)
		return nil, err
	}
	defer rows.Close()

	m := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			slog.Error("store: get settings scan failed", "error", err)
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}

func (s *Store) GetSetting(key string) string {
	var val string
	s.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&val)
	return val
}

func (s *Store) GetIntSetting(key string, defaultVal int) int {
	v := s.GetSetting(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		"INSERT OR REPLACE INTO settings (key, value, updated_at) VALUES (?, ?, datetime('now'))",
		key, value,
	)
	if err != nil {
		slog.Error("store: set setting failed", "key", key, "error", err)
	}
	return err
}

func (s *Store) SetSettings(settings map[string]string) error {
	tx, err := s.db.Begin()
	if err != nil {
		slog.Error("store: set settings begin tx failed", "error", err)
		return err
	}
	defer tx.Rollback()

	for k, v := range settings {
		if _, err := tx.Exec(
			"INSERT OR REPLACE INTO settings (key, value, updated_at) VALUES (?, ?, datetime('now'))",
			k, v,
		); err != nil {
			slog.Error("store: set settings exec failed", "key", k, "error", err)
			return err
		}
	}
	return tx.Commit()
}

// --- Remote Configs ---

// GetRemoteConfig retrieves a stored remote config JSON blob by key.
// Returns empty string if not found.
func (s *Store) GetRemoteConfig(key string) string {
	var val string
	err := s.db.QueryRow("SELECT value FROM remote_configs WHERE key = ?", key).Scan(&val)
	if err != nil {
		return ""
	}
	return val
}

// SetRemoteConfig stores a remote config JSON blob by key.
func (s *Store) SetRemoteConfig(key, value string) error {
	_, err := s.db.Exec(
		"INSERT OR REPLACE INTO remote_configs (key, value, updated_at) VALUES (?, ?, datetime('now'))",
		key, value,
	)
	if err != nil {
		slog.Error("store: set remote config failed", "key", key, "error", err)
	}
	return err
}

// --- Request Logging ---

func (s *Store) LogRequest(userID, model, endpoint string, stream bool, statusCode int, latencyMs int64, errMsg string, errorDetail string, inputTokens, outputTokens int) error {
	sInt := 0
	if stream {
		sInt = 1
	}
	_, err := s.db.Exec(
		"INSERT INTO request_logs (api_key, model, endpoint, stream, status_code, latency_ms, error_message, error_detail, input_tokens, output_tokens) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		userID, model, endpoint, sInt, statusCode, latencyMs, errMsg, errorDetail, inputTokens, outputTokens,
	)
	if err != nil {
		slog.Error("store: log request failed", "user_id", userID, "endpoint", endpoint, "error", err)
	}
	return err
}

// tzOffsetModifier returns a SQLite datetime modifier (e.g. "+8 hours",
// "-5 hours", or "" for UTC) derived from the configured `timezone` setting,
// used to bucket/compare UTC-stored timestamps in the display timezone.
//
// Known limitation: this is a fixed offset sampled at "now", so around a DST
// transition the hourly buckets can be off by one hour. For zones without DST
// (e.g. Asia/Shanghai, always +8) it is exact. Empty setting or a zone that
// fails to load falls back to UTC ("").
func (s *Store) tzOffsetModifier() string {
	name := s.GetSetting("timezone")
	if name == "" || name == "UTC" {
		return ""
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return ""
	}
	_, offset := time.Now().In(loc).Zone()
	if offset == 0 {
		return ""
	}
	// SQLite accepts fractional hours poorly; express in minutes for zones
	// like Asia/Kolkata (+5:30) or Asia/Kathmandu (+5:45).
	minutes := offset / 60
	return fmt.Sprintf("%+d minutes", minutes)
}

// displayLocation returns the configured display timezone as a *time.Location,
// falling back to UTC when unset or unresolvable. Unlike tzOffsetModifier this
// is DST-aware: each instant is converted with its own offset, matching the
// frontend's Intl-based hour bucketing.
func (s *Store) displayLocation() *time.Location {
	name := s.GetSetting("timezone")
	if name == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil || loc == nil {
		return time.UTC
	}
	return loc
}

// hourlyInWindow returns per-hour buckets over the rolling last 24 hours for
// rows matching extraWhere (which may reference api_key via the args). Buckets
// are keyed "%m-%d %H" in the display timezone, computed in Go so DST
// transitions align with the frontend's per-instant bucketing.
func (s *Store) hourlyInWindow(extraWhere string, args ...interface{}) []HourlyData {
	loc := s.displayLocation()
	where := "created_at >= datetime('now', '-24 hours')"
	if extraWhere != "" {
		where = extraWhere + " AND " + where
	}
	rows, err := s.db.Query(`
		SELECT created_at, input_tokens, output_tokens, status_code
		FROM request_logs WHERE `+where, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	type agg struct {
		key                    string
		hourStart              time.Time // truncated-to-hour instant, for chronological sort
		count, in, out, errors int
	}
	buckets := map[string]*agg{}
	for rows.Next() {
		var createdAt string
		var in, out, status int
		if rows.Scan(&createdAt, &in, &out, &status) != nil {
			continue
		}
		t, err := time.Parse("2006-01-02 15:04:05", createdAt)
		if err != nil {
			continue
		}
		local := t.UTC().In(loc)
		key := local.Format("01-02 15")
		b := buckets[key]
		if b == nil {
			b = &agg{key: key, hourStart: local.Truncate(time.Hour)}
			buckets[key] = b
		}
		b.count++
		b.in += in
		b.out += out
		if status >= 400 {
			b.errors++
		}
	}
	aggs := make([]*agg, 0, len(buckets))
	for _, b := range buckets {
		aggs = append(aggs, b)
	}
	// Sort by the actual instant so buckets stay chronological across a month
	// boundary (12-31 → 01-01), which a lexical sort of the "MM-DD HH" key would
	// reverse.
	sort.Slice(aggs, func(i, j int) bool { return aggs[i].hourStart.Before(aggs[j].hourStart) })
	result := make([]HourlyData, 0, len(aggs))
	for _, b := range aggs {
		result = append(result, HourlyData{
			Hour: b.key, Count: b.count, InputTokens: b.in, OutputTokens: b.out, Errors: b.errors,
		})
	}
	return result
}

func (s *Store) GetStats() (*Stats, error) {
	stats := &Stats{}
	// created_at is stored as UTC (DEFAULT datetime('now')). Shift both it and
	// 'now' by the configured display-timezone offset so "today" is bucketed in
	// the user's timezone, not UTC.
	mod := s.tzOffsetModifier()
	tf := "date(created_at) = date('now')"
	if mod != "" {
		tf = "date(created_at, '" + mod + "') = date('now', '" + mod + "')"
	}

	err := s.db.QueryRow("SELECT COUNT(*) FROM request_logs WHERE " + tf).Scan(&stats.TotalRequests)
	if err != nil {
		slog.Error("store: get stats count failed", "error", err)
		return nil, err
	}

	s.db.QueryRow("SELECT COALESCE(AVG(latency_ms), 0) FROM request_logs WHERE " + tf).Scan(&stats.AvgLatencyMs)
	s.db.QueryRow("SELECT COUNT(*) FROM accounts").Scan(&stats.AccountsCount)
	s.db.QueryRow("SELECT COUNT(*) FROM request_logs WHERE " + tf + " AND status_code >= 400").Scan(&stats.ErrorCount)
	s.db.QueryRow("SELECT COUNT(*) FROM request_logs WHERE " + tf + " AND stream = 1").Scan(&stats.StreamCount)
	s.db.QueryRow("SELECT COUNT(*) FROM request_logs WHERE " + tf + " AND status_code < 400").Scan(&stats.SuccessCount)
	s.db.QueryRow("SELECT COALESCE(SUM(input_tokens), 0) FROM request_logs WHERE " + tf).Scan(&stats.TotalInputTk)
	s.db.QueryRow("SELECT COALESCE(SUM(output_tokens), 0) FROM request_logs WHERE " + tf).Scan(&stats.TotalOutputTk)

	rows, err := s.db.Query("SELECT model, COUNT(*) as cnt FROM request_logs WHERE " + tf + " AND model != '' GROUP BY model ORDER BY cnt DESC")
	if err != nil {
		slog.Error("store: get stats by model query failed", "error", err)
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var mc ModelCount
		if err := rows.Scan(&mc.Model, &mc.Count); err != nil {
			return nil, err
		}
		stats.ByModel = append(stats.ByModel, mc)
	}

	validKeys := make(map[string]bool)
	accounts, _ := s.ListAccounts()
	for _, a := range accounts {
		validKeys[a.UserID] = true
	}

	rows2, err := s.db.Query("SELECT api_key, COUNT(*) as cnt FROM request_logs WHERE " + tf + " GROUP BY api_key ORDER BY cnt DESC")
	if err != nil {
		slog.Error("store: get stats by account query failed", "error", err)
		return nil, err
	}
	defer rows2.Close()
	otherCount := 0
	for rows2.Next() {
		var ac AccountCount
		if err := rows2.Scan(&ac.UserID, &ac.Count); err != nil {
			return nil, err
		}
		if validKeys[ac.UserID] {
			stats.ByAccount = append(stats.ByAccount, ac)
		} else {
			otherCount += ac.Count
		}
	}
	if otherCount > 0 {
		stats.ByAccount = append(stats.ByAccount, AccountCount{UserID: "其他", Count: otherCount})
	}

	return stats, nil
}

func (s *Store) GetAllTimeTotals() (*AllTimeTotals, error) {
	t := &AllTimeTotals{}
	s.db.QueryRow("SELECT COUNT(*) FROM request_logs").Scan(&t.TotalRequests)
	s.db.QueryRow("SELECT COALESCE(SUM(input_tokens), 0) FROM request_logs").Scan(&t.TotalInputTk)
	s.db.QueryRow("SELECT COALESCE(SUM(output_tokens), 0) FROM request_logs").Scan(&t.TotalOutputTk)
	s.db.QueryRow("SELECT COUNT(*) FROM request_logs WHERE status_code >= 400").Scan(&t.ErrorCount)
	return t, nil
}

func (s *Store) GetHourlyStats() ([]HourlyData, error) {
	// Bucketed in the display timezone over a rolling 24h window, DST-aware
	// (see hourlyInWindow) so keys align with the frontend's per-instant buckets.
	return s.hourlyInWindow(""), nil
}

func (s *Store) GetAccountStats(userID string) (*AccountStats, error) {
	as := &AccountStats{UserID: userID}
	// "Today" scalar metrics use the display-timezone calendar day, matching
	// GetStats and FillAccountStats so the same account reconciles across the
	// overview, the account list, and this detail page.
	mod := s.tzOffsetModifier()
	todayTF := "date(created_at) = date('now')"
	if mod != "" {
		todayTF = "date(created_at, '" + mod + "') = date('now', '" + mod + "')"
	}

	s.db.QueryRow("SELECT COUNT(*) FROM request_logs WHERE api_key = ? AND "+todayTF, userID).Scan(&as.TotalRequests)
	s.db.QueryRow("SELECT COALESCE(AVG(latency_ms), 0) FROM request_logs WHERE api_key = ? AND "+todayTF, userID).Scan(&as.AvgLatencyMs)
	s.db.QueryRow("SELECT COUNT(*) FROM request_logs WHERE api_key = ? AND stream = 1 AND "+todayTF, userID).Scan(&as.StreamCount)
	s.db.QueryRow("SELECT COUNT(*) FROM request_logs WHERE api_key = ? AND status_code >= 400 AND "+todayTF, userID).Scan(&as.ErrorCount)
	s.db.QueryRow("SELECT COUNT(*) FROM request_logs WHERE api_key = ? AND status_code < 400 AND "+todayTF, userID).Scan(&as.SuccessCount)
	s.db.QueryRow("SELECT COALESCE(SUM(input_tokens), 0) FROM request_logs WHERE api_key = ? AND "+todayTF, userID).Scan(&as.TotalInputTk)
	s.db.QueryRow("SELECT COALESCE(SUM(output_tokens), 0) FROM request_logs WHERE api_key = ? AND "+todayTF, userID).Scan(&as.TotalOutputTk)

	rows, err := s.db.Query("SELECT model, COUNT(*) as cnt FROM request_logs WHERE api_key = ? AND "+todayTF+" GROUP BY model ORDER BY cnt DESC", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var mc ModelCount
		if err := rows.Scan(&mc.Model, &mc.Count); err != nil {
			return nil, err
		}
		as.ByModel = append(as.ByModel, mc)
	}

	rows2, err := s.db.Query("SELECT endpoint, COUNT(*) as cnt FROM request_logs WHERE api_key = ? AND "+todayTF+" GROUP BY endpoint ORDER BY cnt DESC", userID)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var ec EndpointCount
		if err := rows2.Scan(&ec.Endpoint, &ec.Count); err != nil {
			return nil, err
		}
		as.ByEndpoint = append(as.ByEndpoint, ec)
	}

	// All-time totals
	allTime := &AllTimeTotals{}
	s.db.QueryRow("SELECT COUNT(*) FROM request_logs WHERE api_key = ?", userID).Scan(&allTime.TotalRequests)
	s.db.QueryRow("SELECT COALESCE(SUM(input_tokens), 0) FROM request_logs WHERE api_key = ?", userID).Scan(&allTime.TotalInputTk)
	s.db.QueryRow("SELECT COALESCE(SUM(output_tokens), 0) FROM request_logs WHERE api_key = ?", userID).Scan(&allTime.TotalOutputTk)
	s.db.QueryRow("SELECT COUNT(*) FROM request_logs WHERE api_key = ? AND status_code >= 400", userID).Scan(&allTime.ErrorCount)
	as.AllTime = allTime

	// Hourly breakdown for the last 24 hours (rolling window, independent of the
	// calendar-today scalar metrics above). Feeds the "24 小时趋势" charts.
	// DST-aware bucketing (see hourlyInWindow) so keys align with the frontend.
	as.Hourly = s.hourlyInWindow("api_key = ?", userID)

	return as, nil
}

func (s *Store) GetAccountLogs(userID string, limit int) ([]RequestLog, error) {
	return s.GetAccountLogsPaged(userID, 0, "all", limit)
}

// LogQuery parameterises GetAccountLogsQuery. Empty/zero fields are ignored,
// so callers select only the filters they need.
//
//	BeforeID: only rows with id < BeforeID (<=0 starts from the newest row),
//	          enabling stable cursor pagination.
//	Category: "stream" (stream=1) or "errors" (status_code>=400); any other
//	          value returns all rows.
//	Endpoint: exact endpoint match; "" returns all.
//	Model:    the REAL upstream model name. The stored model column may hold a
//	          display string like "Resolved(Requested)"; a non-empty Model is
//	          matched against either the exact stored value or the part before
//	          the first '('. "" returns all.
//	FromUTC / ToUTC: half-open UTC window [FromUTC, ToUTC) compared against
//	          created_at (SQLite datetime('now') text, "YYYY-MM-DD HH:MM:SS").
//	Limit:    page size (defaults to 100 when <=0).
type LogQuery struct {
	BeforeID int64
	Category string
	Endpoint string
	Model    string
	FromUTC  string
	ToUTC    string
	Limit    int
}

// GetAccountLogsPaged returns a page of request logs for userID, newest first.
// It is retained as a thin wrapper for backwards compatibility; new callers
// should use GetAccountLogsQuery.
func (s *Store) GetAccountLogsPaged(userID string, beforeID int64, filter string, limit int) ([]RequestLog, error) {
	return s.GetAccountLogsQuery(userID, LogQuery{BeforeID: beforeID, Category: filter, Limit: limit})
}

// GetAccountLogsQuery returns a page of request logs for userID matching the
// given filters, newest first. See LogQuery for the meaning of each field.
func (s *Store) GetAccountLogsQuery(userID string, q LogQuery) ([]RequestLog, error) {
	if q.Limit <= 0 {
		q.Limit = 100
	}

	where := "api_key = ?"
	args := []interface{}{userID}
	switch q.Category {
	case "stream":
		where += " AND stream = 1"
	case "errors":
		where += " AND status_code >= 400"
	}
	if q.Endpoint != "" {
		where += " AND endpoint = ?"
		args = append(args, q.Endpoint)
	}
	if q.Model != "" {
		// Match the exact stored value OR the real-model prefix (text before
		// the first '('), so a display string "Resolved(Requested)" and the
		// bare "Resolved" value collapse to the same real upstream model.
		where += " AND (model = ? OR (instr(model, '(') > 0 AND substr(model, 1, instr(model, '(') - 1) = ?))"
		args = append(args, q.Model, q.Model)
	}
	if q.FromUTC != "" {
		where += " AND created_at >= ?"
		args = append(args, q.FromUTC)
	}
	if q.ToUTC != "" {
		where += " AND created_at < ?"
		args = append(args, q.ToUTC)
	}
	if q.BeforeID > 0 {
		where += " AND id < ?"
		args = append(args, q.BeforeID)
	}
	args = append(args, q.Limit)

	rows, err := s.db.Query(
		"SELECT id, api_key, model, endpoint, stream, status_code, latency_ms, COALESCE(error_message, ''), COALESCE(error_detail, ''), COALESCE(input_tokens, 0), COALESCE(output_tokens, 0), created_at FROM request_logs WHERE "+where+" ORDER BY id DESC LIMIT ?",
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []RequestLog
	for rows.Next() {
		var l RequestLog
		var streamInt int
		if err := rows.Scan(&l.ID, &l.UserID, &l.Model, &l.Endpoint, &streamInt, &l.StatusCode, &l.LatencyMs, &l.ErrorMessage, &l.ErrorDetail, &l.InputTokens, &l.OutputTokens, &l.CreatedAt); err != nil {
			return nil, err
		}
		l.Stream = streamInt == 1
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

// realModelOf returns the real upstream model name from a stored model value.
// Display strings look like "Resolved(Requested)"; the real name is the text
// before the first '('. Values without '(' are returned unchanged.
func realModelOf(m string) string {
	if i := strings.IndexByte(m, '('); i > 0 {
		return m[:i]
	}
	return m
}

// GetAccountLogFilters returns the distinct endpoints and real upstream model
// names present in a user's logs, for populating filter dropdowns.
func (s *Store) GetAccountLogFilters(userID string) (endpoints, models []string, err error) {
	erows, err := s.db.Query("SELECT DISTINCT endpoint FROM request_logs WHERE api_key = ? AND endpoint != '' ORDER BY endpoint", userID)
	if err != nil {
		return nil, nil, err
	}
	for erows.Next() {
		var e string
		if err := erows.Scan(&e); err != nil {
			erows.Close()
			return nil, nil, err
		}
		endpoints = append(endpoints, e)
	}
	if err := erows.Err(); err != nil {
		erows.Close()
		return nil, nil, err
	}
	erows.Close()

	mrows, err := s.db.Query("SELECT DISTINCT model FROM request_logs WHERE api_key = ? AND model != '' ORDER BY model", userID)
	if err != nil {
		return endpoints, nil, err
	}
	seen := make(map[string]bool)
	for mrows.Next() {
		var m string
		if err := mrows.Scan(&m); err != nil {
			mrows.Close()
			return endpoints, nil, err
		}
		real := realModelOf(m)
		if !seen[real] {
			seen[real] = true
			models = append(models, real)
		}
	}
	if err := mrows.Err(); err != nil {
		mrows.Close()
		return endpoints, nil, err
	}
	mrows.Close()
	return endpoints, models, nil
}

func (s *Store) GetRecentLogs(limit int) ([]RequestLog, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(
		"SELECT id, api_key, model, endpoint, stream, status_code, latency_ms, COALESCE(error_message, ''), COALESCE(input_tokens, 0), COALESCE(output_tokens, 0), created_at FROM request_logs ORDER BY id DESC LIMIT ?",
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []RequestLog
	for rows.Next() {
		var l RequestLog
		var streamInt int
		if err := rows.Scan(&l.ID, &l.UserID, &l.Model, &l.Endpoint, &streamInt, &l.StatusCode, &l.LatencyMs, &l.ErrorMessage, &l.InputTokens, &l.OutputTokens, &l.CreatedAt); err != nil {
			return nil, err
		}
		l.Stream = streamInt == 1
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

// GetRecentErrors returns request logs with status_code >= 400.
func (s *Store) GetRecentErrors(limit int) ([]RequestLog, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(
		"SELECT id, api_key, model, endpoint, stream, status_code, latency_ms, COALESCE(error_message, ''), COALESCE(error_detail, ''), COALESCE(input_tokens, 0), COALESCE(output_tokens, 0), created_at FROM request_logs WHERE status_code >= 400 ORDER BY id DESC LIMIT ?",
		limit,
	)
	if err != nil {
		slog.Error("store: get recent errors query failed", "error", err)
		return nil, err
	}
	defer rows.Close()

	var logs []RequestLog
	for rows.Next() {
		var l RequestLog
		var streamInt int
		if err := rows.Scan(&l.ID, &l.UserID, &l.Model, &l.Endpoint, &streamInt, &l.StatusCode, &l.LatencyMs, &l.ErrorMessage, &l.ErrorDetail, &l.InputTokens, &l.OutputTokens, &l.CreatedAt); err != nil {
			slog.Error("store: get recent errors scan failed", "error", err)
			return nil, err
		}
		l.Stream = streamInt == 1
		logs = append(logs, l)
	}
	if err := rows.Err(); err != nil {
		slog.Error("store: get recent errors iteration failed", "error", err)
		return nil, err
	}
	return logs, nil
}

// CleanupOldLogs deletes request logs older than the specified number of days.
func (s *Store) CleanupOldLogs(days int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	result, err := s.db.Exec(
		"DELETE FROM request_logs WHERE created_at < datetime('now', '-' || ? || ' days')",
		days,
	)
	if err != nil {
		slog.Error("store: cleanup old logs failed", "days", days, "error", err)
		return 0, err
	}
	affected, _ := result.RowsAffected()
	if affected > 0 {
		slog.Info("store: cleaned up old logs", "days", days, "deleted", affected)
	}
	return affected, nil
}

// MigrateTokenLogs reassigns request_logs stored under api_token values to the account's user_id.
func (s *Store) MigrateTokenLogs() (int64, error) {
	result, err := s.db.Exec(`
		UPDATE request_logs SET api_key = (
			SELECT a.user_id FROM accounts a WHERE a.api_token = request_logs.api_key
		) WHERE api_key LIKE 'sk-joy-%' AND EXISTS (
			SELECT 1 FROM accounts a WHERE a.api_token = request_logs.api_key
		)`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// ReassignLogs maps old api_key values in request_logs to a new api_key.
func (s *Store) ReassignLogs(oldKeys []string, newKey string) (int64, error) {
	ph := "?"
	for i := 1; i < len(oldKeys); i++ {
		ph += ",?"
	}
	args := make([]interface{}, len(oldKeys)+1)
	args[0] = newKey
	for i, k := range oldKeys {
		args[i+1] = k
	}
	result, err := s.db.Exec("UPDATE request_logs SET api_key = ? WHERE api_key IN ("+ph+")", args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// EnsureDataDir ensures the data directory exists with correct permissions.
func EnsureDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, DefaultDBDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

// ExportAccountItem is the format for account export/import.
type ExportAccountItem struct {
	UserID        string `json:"user_id"`
	Nickname      string `json:"nickname"`
	Remark        string `json:"remark"`
	PtKey         string `json:"pt_key"`
	IsDefault     bool   `json:"is_default"`
	DefaultModel  string `json:"default_model"`
	DisplayOrder  int    `json:"display_order"`
	LoginType     string `json:"login_type"`
	Tenant        string `json:"tenant"`
	ColorBaseURL  string `json:"color_base_url"`
	MasterBaseURL string `json:"master_base_url"`
	OrgFullName   string `json:"org_full_name"`
}

// ExportAccounts returns all accounts with decrypted pt_keys for export.
func (s *Store) ExportAccounts() ([]ExportAccountItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(
		"SELECT user_id, nickname, remark, pt_key, is_default, default_model, COALESCE(display_order, 0), COALESCE(login_type,''), COALESCE(tenant,''), COALESCE(color_base_url,''), COALESCE(master_base_url,''), COALESCE(org_full_name,'') FROM accounts ORDER BY display_order, created_at",
	)
	if err != nil {
		return nil, fmt.Errorf("query accounts for export: %w", err)
	}
	defer rows.Close()

	var items []ExportAccountItem
	for rows.Next() {
		var item ExportAccountItem
		var encPtKey string
		var isDef int
		if err := rows.Scan(&item.UserID, &item.Nickname, &item.Remark, &encPtKey, &isDef, &item.DefaultModel, &item.DisplayOrder, &item.LoginType, &item.Tenant, &item.ColorBaseURL, &item.MasterBaseURL, &item.OrgFullName); err != nil {
			return nil, fmt.Errorf("scan account for export: %w", err)
		}
		ptKey, err := s.decrypt(encPtKey)
		if err != nil {
			slog.Warn("store: skip account in export, decrypt failed", "user_id", item.UserID, "error", err)
			continue
		}
		item.PtKey = ptKey
		item.IsDefault = isDef == 1
		items = append(items, item)
	}
	if items == nil {
		items = []ExportAccountItem{}
	}
	return items, nil
}

// ImportAccounts imports accounts from export data. Existing accounts are updated (pt_key only).
func (s *Store) ImportAccounts(items []ExportAccountItem) (added int, updated int, err error) {
	for _, item := range items {
		if item.UserID == "" || item.PtKey == "" {
			continue
		}
		var existing int
		s.mu.Lock()
		e := s.db.QueryRow("SELECT COUNT(*) FROM accounts WHERE user_id = ?", item.UserID).Scan(&existing)
		s.mu.Unlock()
		if e != nil {
			return added, updated, fmt.Errorf("check existing account %s: %w", item.UserID, e)
		}
		creds := &AccountCreds{
			LoginType:     item.LoginType,
			Tenant:        item.Tenant,
			ColorBaseURL:  item.ColorBaseURL,
			MasterBaseURL: item.MasterBaseURL,
			OrgFullName:   item.OrgFullName,
		}
		if err := s.AddAccount(item.UserID, item.PtKey, item.Nickname, item.IsDefault, item.DefaultModel, creds); err != nil {
			return added, updated, fmt.Errorf("import account %s: %w", item.UserID, err)
		}
		if existing > 0 {
			updated++
		} else {
			added++
		}
	}
	return added, updated, nil
}

// Copy from os.ReadFile pattern -- used to check if DB exists.
func DBExists() bool {
	path, err := DefaultDBPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}
