package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// --- Database init ---

func TestOpenCreatesDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("database file was not created")
	}
}

func TestOpenCreatesDir(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sub", "dir", "test.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open with nested dir: %v", err)
	}
	defer s.Close()
}

func TestDefaultDBPath(t *testing.T) {
	path, err := DefaultDBPath()
	if err != nil {
		t.Fatalf("default path: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty default path")
	}
}

// --- Encryption ---

func TestEncryptDecrypt(t *testing.T) {
	s := openTestStore(t)

	plaintext := "super-secret-pt-key-12345"
	encrypted, err := s.encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if encrypted == plaintext {
		t.Error("encrypted should differ from plaintext")
	}

	decrypted, err := s.decrypt(encrypted)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if decrypted != plaintext {
		t.Errorf("decrypt = %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptDifferentEachTime(t *testing.T) {
	s := openTestStore(t)

	plaintext := "same-input"
	enc1, _ := s.encrypt(plaintext)
	enc2, _ := s.encrypt(plaintext)
	if enc1 == enc2 {
		t.Error("two encryptions of same input should produce different ciphertext (random nonce)")
	}
}

func TestDecryptInvalidCiphertext(t *testing.T) {
	s := openTestStore(t)

	_, err := s.decrypt("not-valid-hex!")
	if err == nil {
		t.Error("expected error for invalid hex")
	}
}

func TestDecryptTooShort(t *testing.T) {
	s := openTestStore(t)

	_, err := s.decrypt("ab")
	if err == nil {
		t.Error("expected error for too-short ciphertext")
	}
}

// --- Account CRUD ---

func TestAddAndListAccounts(t *testing.T) {
	s := openTestStore(t)

	err := s.AddAccount("key1", "pt1", "user1", true, "JoyAI-Code", nil)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	accounts, err := s.ListAccounts()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("len = %d, want 1", len(accounts))
	}
	if accounts[0].UserID != "key1" {
		t.Errorf("UserID = %q, want %q", accounts[0].UserID, "key1")
	}
	if accounts[0].Nickname != "user1" {
		t.Errorf("Nickname = %q, want %q", accounts[0].Nickname, "user1")
	}
	if !accounts[0].IsDefault {
		t.Error("expected IsDefault = true")
	}
	if accounts[0].DefaultModel != "JoyAI-Code" {
		t.Errorf("DefaultModel = %q, want %q", accounts[0].DefaultModel, "JoyAI-Code")
	}
}

func TestListAccountsEmpty(t *testing.T) {
	s := openTestStore(t)

	accounts, err := s.ListAccounts()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if accounts != nil {
		t.Errorf("expected nil for empty, got %v", accounts)
	}
}

func TestAddMultipleAccounts(t *testing.T) {
	s := openTestStore(t)

	s.AddAccount("key1", "pt1", "user1", true, "", nil)
	s.AddAccount("key2", "pt2", "user2", false, "GLM-5.1", nil)

	accounts, _ := s.ListAccounts()
	if len(accounts) != 2 {
		t.Fatalf("len = %d, want 2", len(accounts))
	}

	// Only one default
	defaultCount := 0
	for _, a := range accounts {
		if a.IsDefault {
			defaultCount++
		}
	}
	if defaultCount != 1 {
		t.Errorf("default count = %d, want 1", defaultCount)
	}
}

func TestAddAccountOverwrites(t *testing.T) {
	s := openTestStore(t)

	s.AddAccount("key1", "pt1", "user1", true, "", nil)
	s.AddAccount("key1", "pt1-updated", "user1-new", false, "GLM-5.1", nil)

	accounts, _ := s.ListAccounts()
	if len(accounts) != 1 {
		t.Fatalf("len = %d, want 1 (upsert)", len(accounts))
	}

	a, _ := s.GetAccount("key1")
	if a.PtKey != "pt1-updated" {
		t.Errorf("PtKey = %q, want %q", a.PtKey, "pt1-updated")
	}
}

func TestGetAccount(t *testing.T) {
	s := openTestStore(t)

	s.AddAccount("key1", "secret-pt-key", "user1", true, "JoyAI-Code", nil)

	a, err := s.GetAccount("key1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if a == nil {
		t.Fatal("expected account, got nil")
	}
	if a.PtKey != "secret-pt-key" {
		t.Errorf("PtKey = %q, want %q", a.PtKey, "secret-pt-key")
	}
	if a.UserID != "key1" {
		t.Errorf("UserID = %q, want %q", a.UserID, "key1")
	}
}

func TestGetAccountNotFound(t *testing.T) {
	s := openTestStore(t)

	a, err := s.GetAccount("nonexistent")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if a != nil {
		t.Error("expected nil for nonexistent key")
	}
}

func TestRemoveAccount(t *testing.T) {
	s := openTestStore(t)

	s.AddAccount("key1", "pt1", "user1", true, "", nil)
	s.RemoveAccount("key1")

	accounts, _ := s.ListAccounts()
	if len(accounts) != 0 {
		t.Errorf("len = %d, want 0 after remove", len(accounts))
	}
}

func TestRemoveNonexistent(t *testing.T) {
	s := openTestStore(t)

	err := s.RemoveAccount("nonexistent")
	if err != nil {
		t.Errorf("remove nonexistent should not error: %v", err)
	}
}

func TestSetDefault(t *testing.T) {
	s := openTestStore(t)

	s.AddAccount("key1", "pt1", "user1", true, "", nil)
	s.AddAccount("key2", "pt2", "user2", false, "", nil)

	s.SetDefault("key2")

	accounts, _ := s.ListAccounts()
	for _, a := range accounts {
		if a.UserID == "key1" && a.IsDefault {
			t.Error("key1 should no longer be default")
		}
		if a.UserID == "key2" && !a.IsDefault {
			t.Error("key2 should be default")
		}
	}
}

func TestUpdateAccountModel(t *testing.T) {
	s := openTestStore(t)

	s.AddAccount("key1", "pt1", "user1", true, "JoyAI-Code", nil)
	s.UpdateAccountModel("key1", "GLM-5.1")

	a, _ := s.GetAccount("key1")
	if a.DefaultModel != "GLM-5.1" {
		t.Errorf("DefaultModel = %q, want %q", a.DefaultModel, "GLM-5.1")
	}
}

func TestGetDefaultAccount(t *testing.T) {
	s := openTestStore(t)

	s.AddAccount("key1", "pt1", "user1", false, "", nil)
	s.AddAccount("key2", "pt2", "user2", true, "JoyAI-Code", nil)

	a, err := s.GetDefaultAccount()
	if err != nil {
		t.Fatalf("get default: %v", err)
	}
	if a == nil {
		t.Fatal("expected default account, got nil")
	}
	if a.UserID != "key2" {
		t.Errorf("default UserID = %q, want %q", a.UserID, "key2")
	}
}

func TestGetDefaultAccountNone(t *testing.T) {
	s := openTestStore(t)

	a, err := s.GetDefaultAccount()
	if err != nil {
		t.Fatalf("get default: %v", err)
	}
	if a != nil {
		t.Error("expected nil when no default account")
	}
}

// --- Settings ---

func TestGetSettingsEmpty(t *testing.T) {
	s := openTestStore(t)

	settings, err := s.GetSettings()
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	// migrate() seeds internal schema_* flags; ignore them and assert there are
	// no user-facing settings on a fresh store.
	delete(settings, "schema_tz_utc_migrated")
	delete(settings, "schema_future_ts_corrected")
	if len(settings) != 0 {
		t.Errorf("expected empty settings, got %v", settings)
	}
}

func TestSetAndGetSetting(t *testing.T) {
	s := openTestStore(t)

	s.SetSetting("key1", "value1")
	s.SetSetting("key2", "value2")

	settings, _ := s.GetSettings()
	if settings["key1"] != "value1" {
		t.Errorf("key1 = %q, want %q", settings["key1"], "value1")
	}
	if settings["key2"] != "value2" {
		t.Errorf("key2 = %q, want %q", settings["key2"], "value2")
	}
}

func TestSetSettingsBatch(t *testing.T) {
	s := openTestStore(t)

	err := s.SetSettings(map[string]string{
		"a": "1",
		"b": "2",
	})
	if err != nil {
		t.Fatalf("set settings batch: %v", err)
	}

	settings, _ := s.GetSettings()
	// Note: migrate() seeds the schema_tz_utc_migrated flag, so assert the
	// keys we set are present rather than an exact count.
	if settings["a"] != "1" || settings["b"] != "2" {
		t.Errorf("settings = %+v, want a=1 b=2", settings)
	}
}

func TestSetSettingOverwrite(t *testing.T) {
	s := openTestStore(t)

	s.SetSetting("key", "old")
	s.SetSetting("key", "new")

	settings, _ := s.GetSettings()
	if settings["key"] != "new" {
		t.Errorf("key = %q, want %q", settings["key"], "new")
	}
}

// --- Request Logging & Stats ---

func TestLogRequestAndGetStats(t *testing.T) {
	s := openTestStore(t)

	s.LogRequest("key1", "JoyAI-Code", "/v1/chat/completions", true, 200, 500, "", "", 0, 0)
	s.LogRequest("key1", "GLM-5.1", "/v1/chat/completions", false, 200, 300, "", "", 0, 0)
	s.LogRequest("key2", "JoyAI-Code", "/v1/messages", true, 200, 400, "", "", 0, 0)

	stats, err := s.GetStats()
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}
	if stats.TotalRequests != 3 {
		t.Errorf("TotalRequests = %d, want 3", stats.TotalRequests)
	}
	if stats.AccountsCount != 0 {
		t.Errorf("AccountsCount = %d, want 0 (no accounts added)", stats.AccountsCount)
	}
	if len(stats.ByModel) != 2 {
		t.Errorf("ByModel len = %d, want 2", len(stats.ByModel))
	}
	if len(stats.ByAccount) != 1 || stats.ByAccount[0].UserID != "其他" {
		t.Errorf("ByAccount = %v, want [{其他 3}]", stats.ByAccount)
	}
}

func TestGetStatsEmpty(t *testing.T) {
	s := openTestStore(t)

	stats, err := s.GetStats()
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}
	if stats.TotalRequests != 0 {
		t.Errorf("TotalRequests = %d, want 0", stats.TotalRequests)
	}
}

func TestGetAccountStats(t *testing.T) {
	s := openTestStore(t)

	s.LogRequest("key1", "JoyAI-Code", "/v1/chat/completions", true, 200, 500, "", "", 0, 0)
	s.LogRequest("key1", "GLM-5.1", "/v1/messages", false, 200, 300, "", "", 0, 0)
	s.LogRequest("key1", "JoyAI-Code", "/v1/chat/completions", true, 500, 100, "", "", 0, 0)

	stats, err := s.GetAccountStats("key1")
	if err != nil {
		t.Fatalf("get account stats: %v", err)
	}
	if stats.TotalRequests != 3 {
		t.Errorf("TotalRequests = %d, want 3", stats.TotalRequests)
	}
	if stats.StreamCount != 2 {
		t.Errorf("StreamCount = %d, want 2", stats.StreamCount)
	}
	if stats.ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", stats.ErrorCount)
	}
	if len(stats.ByModel) != 2 {
		t.Errorf("ByModel len = %d, want 2", len(stats.ByModel))
	}
	if len(stats.ByEndpoint) != 2 {
		t.Errorf("ByEndpoint len = %d, want 2", len(stats.ByEndpoint))
	}
}

func TestGetAccountStatsEmpty(t *testing.T) {
	s := openTestStore(t)

	stats, err := s.GetAccountStats("nonexistent")
	if err != nil {
		t.Fatalf("get account stats: %v", err)
	}
	if stats.TotalRequests != 0 {
		t.Errorf("TotalRequests = %d, want 0", stats.TotalRequests)
	}
}

func TestGetRecentLogs(t *testing.T) {
	s := openTestStore(t)

	s.LogRequest("key1", "model1", "/v1/test", true, 200, 100, "", "", 0, 0)
	s.LogRequest("key2", "model2", "/v1/test", false, 200, 200, "", "", 0, 0)
	s.LogRequest("key1", "model3", "/v1/test", true, 200, 300, "", "", 0, 0)

	logs, err := s.GetRecentLogs(2)
	if err != nil {
		t.Fatalf("get recent logs: %v", err)
	}
	if len(logs) != 2 {
		t.Errorf("len = %d, want 2", len(logs))
	}
	// Most recent first
	if logs[0].Model != "model3" {
		t.Errorf("first log model = %q, want %q", logs[0].Model, "model3")
	}
}

func TestGetRecentLogsDefault(t *testing.T) {
	s := openTestStore(t)

	s.LogRequest("key1", "m1", "/v1", true, 200, 100, "", "", 0, 0)
	s.LogRequest("key1", "m2", "/v1", true, 200, 100, "", "", 0, 0)

	logs, err := s.GetRecentLogs(0)
	if err != nil {
		t.Fatalf("get recent logs: %v", err)
	}
	// Default limit = 100
	if len(logs) != 2 {
		t.Errorf("len = %d, want 2", len(logs))
	}
}

// --- Encryption key persistence ---

func TestEncryptionKeyReused(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Open twice with same dir — should reuse same encryption key
	s1, _ := Open(dbPath)
	enc1, _ := s1.encrypt("hello")
	s1.Close()

	s2, _ := Open(dbPath)
	dec2, err := s2.decrypt(enc1)
	s2.Close()

	if err != nil {
		t.Fatalf("decrypt with reopened store: %v", err)
	}
	if dec2 != "hello" {
		t.Errorf("decrypted = %q, want %q", dec2, "hello")
	}
}

// TestGetHourlyStatsBucketsInUTC verifies that with no timezone setting,
// created_at is stored as UTC and the hourly bucket is formatted in UTC
// (strftime with no offset modifier). This is the default display base.
func TestGetHourlyStatsBucketsInUTC(t *testing.T) {
	s := openTestStore(t)
	if err := s.LogRequest("k1", "GLM-5.1", "/v1/chat", true, 200, 100, "", "", 1, 2); err != nil {
		t.Fatalf("log request: %v", err)
	}

	// Expected bucket: UTC now, since created_at is stored as UTC and no
	// timezone setting is configured (offset modifier is empty).
	var want string
	if err := s.db.QueryRow("SELECT strftime('%m-%d %H', 'now')").Scan(&want); err != nil {
		t.Fatalf("compute expected hour: %v", err)
	}

	hourly, err := s.GetHourlyStats()
	if err != nil {
		t.Fatalf("hourly stats: %v", err)
	}
	if len(hourly) != 1 {
		t.Fatalf("hourly rows = %d, want 1: %+v", len(hourly), hourly)
	}
	if hourly[0].Hour != want {
		t.Errorf("hour = %q, want %q (UTC bucket)", hourly[0].Hour, want)
	}
	if hourly[0].Count != 1 {
		t.Errorf("count = %d, want 1", hourly[0].Count)
	}
}

// TestGetHourlyStatsRespectsTimezone verifies the bucket shifts when a
// timezone is configured.
func TestGetHourlyStatsRespectsTimezone(t *testing.T) {
	s := openTestStore(t)
	s.SetSetting("timezone", "Asia/Shanghai")
	if err := s.LogRequest("k1", "GLM-5.1", "/v1/chat", true, 200, 100, "", "", 1, 2); err != nil {
		t.Fatalf("log request: %v", err)
	}

	// Expected bucket: UTC now shifted +8 hours (Asia/Shanghai, no DST).
	var want string
	if err := s.db.QueryRow("SELECT strftime('%m-%d %H', 'now', '+480 minutes')").Scan(&want); err != nil {
		t.Fatalf("compute expected hour: %v", err)
	}

	hourly, err := s.GetHourlyStats()
	if err != nil {
		t.Fatalf("hourly stats: %v", err)
	}
	if len(hourly) != 1 {
		t.Fatalf("hourly rows = %d, want 1: %+v", len(hourly), hourly)
	}
	if hourly[0].Hour != want {
		t.Errorf("hour = %q, want %q (Asia/Shanghai bucket)", hourly[0].Hour, want)
	}
}

// TestMigrateToUTCIdempotent verifies that a row stored as server-local
// wall-clock is shifted to UTC exactly once, even across multiple opens.
func TestMigrateToUTCIdempotent(t *testing.T) {
	_, offset := time.Now().Zone()
	if offset == 0 {
		t.Skip("server is on UTC; migration is a no-op shift")
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s := openAt(t, dbPath)
	// Simulate a legacy row written before the flag existed: clear the flag
	// and insert a known local-wall-clock timestamp.
	if _, err := s.db.Exec("DELETE FROM settings WHERE key = 'schema_tz_utc_migrated'"); err != nil {
		t.Fatalf("clear flag: %v", err)
	}
	if _, err := s.db.Exec(
		"INSERT INTO request_logs (api_key, model, endpoint, stream, status_code, latency_ms, created_at) VALUES ('k','m','/e',0,200,10,'2026-01-01 12:00:00')",
	); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	s.Close()

	readTS := func(st *Store) string {
		var ts string
		if err := st.db.QueryRow("SELECT created_at FROM request_logs WHERE api_key='k'").Scan(&ts); err != nil {
			t.Fatalf("read ts: %v", err)
		}
		return ts
	}

	// First reopen: migration runs, shifts local→UTC once.
	s1 := openAt(t, dbPath)
	after1 := readTS(s1)
	if after1 == "2026-01-01 12:00:00" {
		t.Fatalf("expected timestamp to shift on first migration, got %q", after1)
	}
	s1.Close()

	// Second reopen: flag is set, no further shift.
	s2 := openAt(t, dbPath)
	after2 := readTS(s2)
	s2.Close()
	if after1 != after2 {
		t.Errorf("migration not idempotent: after1=%q after2=%q", after1, after2)
	}
}

func openAt(t *testing.T, dbPath string) *Store {
	t.Helper()
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return s
}

// colDefault returns the SQL default expression for a column.
func colDefault(t *testing.T, s *Store, table, col string) string {
	t.Helper()
	var dflt string
	if err := s.db.QueryRow(
		"SELECT COALESCE(dflt_value,'') FROM pragma_table_info(?) WHERE name=?", table, col,
	).Scan(&dflt); err != nil {
		t.Fatalf("read default for %s.%s: %v", table, col, err)
	}
	return dflt
}

// TestMigrateColumnDefaultsToUTC verifies that a table carrying the legacy
// datetime('now','localtime') column default is rebuilt so the default becomes
// UTC datetime('now'), and that a subsequent default-based insert lands as UTC.
func TestMigrateColumnDefaultsToUTC(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// First open creates the schema (already UTC). Reintroduce the legacy
	// localtime default to simulate a pre-UTC database, then reopen.
	s := openAt(t, dbPath)
	if _, err := s.db.Exec(`
		CREATE TABLE request_logs_legacy (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			api_key TEXT, model TEXT, endpoint TEXT,
			stream INTEGER DEFAULT 0, status_code INTEGER, latency_ms INTEGER,
			created_at TEXT DEFAULT (datetime('now', 'localtime')),
			error_message TEXT DEFAULT '', input_tokens INTEGER DEFAULT 0, output_tokens INTEGER DEFAULT 0
		);
		INSERT INTO request_logs_legacy (api_key, model, endpoint, created_at)
			SELECT api_key, model, endpoint, created_at FROM request_logs;
		DROP TABLE request_logs;
		ALTER TABLE request_logs_legacy RENAME TO request_logs;
	`); err != nil {
		t.Fatalf("install legacy schema: %v", err)
	}
	// Clear the guard flags so the timestamp-correction migration re-runs too.
	s.db.Exec("DELETE FROM settings WHERE key IN ('schema_future_ts_corrected')")
	if got := colDefault(t, s, "request_logs", "created_at"); !strings.Contains(got, "localtime") {
		t.Fatalf("precondition failed: expected localtime default, got %q", got)
	}
	s.Close()

	// Reopen: migrateColumnDefaultsToUTC rebuilds the table with a UTC default.
	s2 := openAt(t, dbPath)
	defer s2.Close()

	dflt := colDefault(t, s2, "request_logs", "created_at")
	if strings.Contains(dflt, "localtime") {
		t.Errorf("created_at default still localtime after migration: %q", dflt)
	}
	if !strings.Contains(dflt, "datetime('now')") {
		t.Errorf("created_at default not UTC after migration: %q", dflt)
	}

	// A default-based insert must now store UTC (matches datetime('now')).
	if err := s2.LogRequest("k", "m", "/e", false, 200, 1, "", "", 0, 0); err != nil {
		t.Fatalf("log request: %v", err)
	}
	var stored, utcNow string
	s2.db.QueryRow("SELECT created_at FROM request_logs WHERE api_key='k'").Scan(&stored)
	s2.db.QueryRow("SELECT datetime('now')").Scan(&utcNow)
	// Compare to the minute to avoid a rare second-boundary flake.
	if stored[:16] != utcNow[:16] {
		t.Errorf("new row stored %q, want UTC ~%q", stored, utcNow)
	}
}

// TestMigrateFutureTimestampsToUTC verifies that rows sitting in the future
// (local wall-clock written through a stale default after the UTC migration)
// are shifted back to UTC, and that the correction runs only once.
func TestMigrateFutureTimestampsToUTC(t *testing.T) {
	_, offset := time.Now().Zone()
	if offset == 0 {
		t.Skip("server is on UTC; future-timestamp correction is a no-op shift")
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s := openAt(t, dbPath)
	// A polluted row: local wall-clock stored as if UTC => sits in the future.
	// Use datetime('now','localtime') which, on a non-UTC server, is ahead of
	// (offset>0) or behind (offset<0) datetime('now').
	if offset > 0 {
		if _, err := s.db.Exec(
			"INSERT INTO request_logs (api_key, model, endpoint, created_at) VALUES ('future','m','/e', datetime('now','localtime'))",
		); err != nil {
			t.Fatalf("insert future row: %v", err)
		}
	} else {
		t.Skip("server offset is negative; future-in-time simulation not applicable")
	}
	// A legitimate past row must be left untouched.
	if _, err := s.db.Exec(
		"INSERT INTO request_logs (api_key, model, endpoint, created_at) VALUES ('past','m','/e','2020-01-01 00:00:00')",
	); err != nil {
		t.Fatalf("insert past row: %v", err)
	}
	// Clear the guard so reopening re-runs the correction.
	s.db.Exec("DELETE FROM settings WHERE key = 'schema_future_ts_corrected'")
	s.Close()

	s2 := openAt(t, dbPath)
	defer s2.Close()

	var future, utcNow, past string
	s2.db.QueryRow("SELECT created_at FROM request_logs WHERE api_key='future'").Scan(&future)
	s2.db.QueryRow("SELECT datetime('now')").Scan(&utcNow)
	s2.db.QueryRow("SELECT created_at FROM request_logs WHERE api_key='past'").Scan(&past)

	if future > utcNow {
		t.Errorf("future row not corrected: %q still ahead of now %q", future, utcNow)
	}
	if past != "2020-01-01 00:00:00" {
		t.Errorf("past row should be untouched, got %q", past)
	}
}

// seedLogs inserts n request logs for userID; every 3rd is an error (500) and
// every 2nd is a stream row, giving a mix for filter/pagination assertions.
func seedLogs(t *testing.T, s *Store, userID string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		status := 200
		if i%3 == 0 {
			status = 500
		}
		stream := i%2 == 0
		if err := s.LogRequest(userID, "m", "/e", stream, status, 10, "", "", 0, 0); err != nil {
			t.Fatalf("seed log %d: %v", i, err)
		}
	}
}

func TestGetAccountLogsPaged(t *testing.T) {
	s := openTestStore(t)
	seedLogs(t, s, "u1", 25)
	seedLogs(t, s, "other", 5) // must never leak into u1's results

	// Page 1 (newest first), then cursor to page 2 via the last id.
	p1, err := s.GetAccountLogsPaged("u1", 0, "all", 10)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(p1) != 10 {
		t.Fatalf("page1 len = %d, want 10", len(p1))
	}
	// Descending id order.
	for i := 1; i < len(p1); i++ {
		if p1[i].ID >= p1[i-1].ID {
			t.Fatalf("not descending at %d: %d >= %d", i, p1[i].ID, p1[i-1].ID)
		}
	}
	// Only u1 rows.
	for _, l := range p1 {
		if l.UserID != "u1" {
			t.Fatalf("leaked row from %q", l.UserID)
		}
	}

	p2, err := s.GetAccountLogsPaged("u1", p1[len(p1)-1].ID, "all", 10)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(p2) != 10 {
		t.Fatalf("page2 len = %d, want 10", len(p2))
	}
	// No overlap between pages.
	seen := map[int64]bool{}
	for _, l := range p1 {
		seen[l.ID] = true
	}
	for _, l := range p2 {
		if seen[l.ID] {
			t.Fatalf("page2 overlaps page1 at id %d", l.ID)
		}
	}

	// Last page is short → caller derives has_more=false.
	p3, err := s.GetAccountLogsPaged("u1", p2[len(p2)-1].ID, "all", 10)
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(p3) != 5 {
		t.Errorf("page3 len = %d, want 5 (25 total - 20)", len(p3))
	}
}

func TestGetAccountLogsPagedFilters(t *testing.T) {
	s := openTestStore(t)
	seedLogs(t, s, "u1", 30)

	errs, err := s.GetAccountLogsPaged("u1", 0, "errors", 100)
	if err != nil {
		t.Fatalf("errors: %v", err)
	}
	if len(errs) == 0 {
		t.Fatal("expected some error rows")
	}
	for _, l := range errs {
		if l.StatusCode < 400 {
			t.Errorf("errors filter returned status %d", l.StatusCode)
		}
	}

	streams, err := s.GetAccountLogsPaged("u1", 0, "stream", 100)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if len(streams) == 0 {
		t.Fatal("expected some stream rows")
	}
	for _, l := range streams {
		if !l.Stream {
			t.Error("stream filter returned a non-stream row")
		}
	}

	all, err := s.GetAccountLogsPaged("u1", 0, "all", 100)
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if len(all) != 30 {
		t.Errorf("all len = %d, want 30", len(all))
	}
}

// insertLogAt inserts a request_log row with an explicit UTC created_at, since
// LogRequest always uses the column default (now).
func insertLogAt(t *testing.T, s *Store, userID, model, endpoint, createdUTC string, status int) {
	t.Helper()
	stream := 0
	_, err := s.db.Exec(
		"INSERT INTO request_logs (api_key, model, endpoint, stream, status_code, latency_ms, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		userID, model, endpoint, stream, status, 7, createdUTC,
	)
	if err != nil {
		t.Fatalf("insertLogAt %q/%q: %v", model, createdUTC, err)
	}
}

// TestGetAccountLogsQueryFilters covers endpoint, real-model, and date-window
// filters, plus their composition with limit/cursor.
func TestGetAccountLogsQueryFilters(t *testing.T) {
	s := openTestStore(t)
	// Three rows on 2026-07-10 (two bare real models + one display form), all
	// /v1/messages; the display form must collapse to the same real model.
	insertLogAt(t, s, "u1", "claude-opus-4.8-hq", "/v1/messages", "2026-07-10 10:00:00", 200)
	insertLogAt(t, s, "u1", "claude-opus-4.8-hq", "/v1/messages", "2026-07-10 12:00:00", 200)
	insertLogAt(t, s, "u1", "claude-opus-4.8-hq(claude-opus-4.8)", "/v1/messages", "2026-07-10 14:00:00", 200)
	// Different model + endpoint, on 2026-07-11.
	insertLogAt(t, s, "u1", "gpt-4o", "/v1/chat/completions", "2026-07-11 09:00:00", 200)
	insertLogAt(t, s, "u1", "claude-opus-4.8-hq", "/v1/chat/completions", "2026-07-11 11:00:00", 200)
	// Other user must not leak.
	insertLogAt(t, s, "other", "claude-opus-4.8-hq", "/v1/messages", "2026-07-10 10:00:00", 200)

	// Real-model filter: bare + display form both match; gpt-4o excluded.
	byModel, err := s.GetAccountLogsQuery("u1", LogQuery{Model: "claude-opus-4.8-hq", Limit: 100})
	if err != nil {
		t.Fatalf("model filter: %v", err)
	}
	if len(byModel) != 4 {
		t.Errorf("model filter len = %d, want 4 (2 bare + 1 display + 1 on 07-11)", len(byModel))
	}

	// Endpoint filter.
	byEndpoint, err := s.GetAccountLogsQuery("u1", LogQuery{Endpoint: "/v1/messages", Limit: 100})
	if err != nil {
		t.Fatalf("endpoint filter: %v", err)
	}
	if len(byEndpoint) != 3 {
		t.Errorf("endpoint filter len = %d, want 3", len(byEndpoint))
	}
	for _, l := range byEndpoint {
		if l.Endpoint != "/v1/messages" {
			t.Errorf("endpoint filter returned %q", l.Endpoint)
		}
	}

	// Date window covering 2026-07-10 UTC only (half-open).
	byDate, err := s.GetAccountLogsQuery("u1", LogQuery{FromUTC: "2026-07-10 00:00:00", ToUTC: "2026-07-11 00:00:00", Limit: 100})
	if err != nil {
		t.Fatalf("date filter: %v", err)
	}
	if len(byDate) != 3 {
		t.Errorf("date filter len = %d, want 3 (all on 07-10)", len(byDate))
	}

	// Composition: real-model + date window on 2026-07-11.
	composed, err := s.GetAccountLogsQuery("u1", LogQuery{Model: "claude-opus-4.8-hq", FromUTC: "2026-07-11 00:00:00", ToUTC: "2026-07-12 00:00:00", Limit: 100})
	if err != nil {
		t.Fatalf("composed filter: %v", err)
	}
	if len(composed) != 1 {
		t.Errorf("composed filter len = %d, want 1", len(composed))
	}

	// Limit + cursor still paginate within a filtered set.
	page, err := s.GetAccountLogsQuery("u1", LogQuery{Model: "claude-opus-4.8-hq", Limit: 2})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(page))
	}
	page2, err := s.GetAccountLogsQuery("u1", LogQuery{Model: "claude-opus-4.8-hq", BeforeID: page[len(page)-1].ID, Limit: 2})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 {
		t.Errorf("page2 len = %d, want 2", len(page2))
	}
	seen := map[int64]bool{}
	for _, l := range page {
		seen[l.ID] = true
	}
	for _, l := range page2 {
		if seen[l.ID] {
			t.Errorf("page2 overlaps page1 at id %d", l.ID)
		}
	}
}

// TestGetAccountLogFilters checks distinct endpoints and real-model dedup,
// including cross-user isolation.
func TestGetAccountLogFilters(t *testing.T) {
	s := openTestStore(t)
	insertLogAt(t, s, "u1", "claude-opus-4.8-hq", "/v1/messages", "2026-07-10 10:00:00", 200)
	insertLogAt(t, s, "u1", "claude-opus-4.8-hq(claude-opus-4.8)", "/v1/messages", "2026-07-10 11:00:00", 200)
	insertLogAt(t, s, "u1", "gpt-4o", "/v1/chat/completions", "2026-07-10 12:00:00", 200)
	insertLogAt(t, s, "u1", "", "", "2026-07-10 13:00:00", 200) // blank → excluded
	insertLogAt(t, s, "other", "claude-sonnet-5", "/v1/messages", "2026-07-10 10:00:00", 200)

	endpoints, models, err := s.GetAccountLogFilters("u1")
	if err != nil {
		t.Fatalf("GetAccountLogFilters: %v", err)
	}
	// Endpoints sorted, blanks excluded.
	wantEndpoints := []string{"/v1/chat/completions", "/v1/messages"}
	if len(endpoints) != len(wantEndpoints) {
		t.Fatalf("endpoints = %v, want %v", endpoints, wantEndpoints)
	}
	for i, e := range endpoints {
		if e != wantEndpoints[i] {
			t.Errorf("endpoints[%d] = %q, want %q", i, e, wantEndpoints[i])
		}
	}
	// Models are real names, deduped (display form collapses to bare).
	wantModels := []string{"claude-opus-4.8-hq", "gpt-4o"}
	if len(models) != len(wantModels) {
		t.Fatalf("models = %v, want %v", models, wantModels)
	}
	for i, m := range models {
		if m != wantModels[i] {
			t.Errorf("models[%d] = %q, want %q", i, m, wantModels[i])
		}
	}
}

// TestLogIndexSurvivesTableRebuild verifies the cursor-pagination index exists
// and is still present after migrateColumnDefaultsToUTC rebuilds request_logs.
func TestLogIndexSurvivesTableRebuild(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	indexExists := func(s *Store) bool {
		var name string
		err := s.db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='index' AND name='idx_request_logs_api_key_id'",
		).Scan(&name)
		return err == nil && name != ""
	}

	// Fresh open: index created by migrate().
	s := openAt(t, dbPath)
	if !indexExists(s) {
		t.Fatal("index missing on fresh db")
	}
	// Reintroduce the legacy localtime default so a reopen triggers the table
	// rebuild path, which must not leave the index dropped.
	if _, err := s.db.Exec(`
		CREATE TABLE request_logs_legacy (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			api_key TEXT, model TEXT, endpoint TEXT,
			stream INTEGER DEFAULT 0, status_code INTEGER, latency_ms INTEGER,
			created_at TEXT DEFAULT (datetime('now', 'localtime')),
			error_message TEXT DEFAULT '', input_tokens INTEGER DEFAULT 0, output_tokens INTEGER DEFAULT 0
		);
		DROP TABLE request_logs;
		ALTER TABLE request_logs_legacy RENAME TO request_logs;
	`); err != nil {
		t.Fatalf("install legacy schema: %v", err)
	}
	s.Close()

	s2 := openAt(t, dbPath)
	defer s2.Close()
	if !indexExists(s2) {
		t.Error("index missing after table rebuild")
	}
}
