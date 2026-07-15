package auth

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestLoadFromSystem_NonDarwin(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("skipping non-darwin test on darwin")
	}
	// Isolate to an empty HOME so a real ~/.config/Code/... on the test
	// machine cannot satisfy detection; non-darwin with no credentials must error.
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	_, err := LoadFromSystem()
	if err == nil {
		t.Fatal("expected error on non-darwin platform with no credentials, got nil")
	}
}

func TestCredentials_EmptyFields(t *testing.T) {
	creds := &Credentials{}
	if creds.PtKey != "" {
		t.Errorf("PtKey = %q, want empty string", creds.PtKey)
	}
	if creds.UserID != "" {
		t.Errorf("UserID = %q, want empty string", creds.UserID)
	}
}

func TestCredentials_NilVsNonNil(t *testing.T) {
	var creds *Credentials
	nonNil := &Credentials{PtKey: "x", UserID: "y"}
	if creds == nonNil {
		t.Error("nil should not equal allocated Credentials")
	}
}

func TestStateData_JSONParsing(t *testing.T) {
	raw := `{"joyCoderUser":{"ptKey":"test-pt-key-123","userId":"user-456"}}`
	var data ideStateData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("failed to parse valid JSON: %v", err)
	}
	if data.JoyCoderUser.PtKey != "test-pt-key-123" {
		t.Errorf("PtKey = %q, want %q", data.JoyCoderUser.PtKey, "test-pt-key-123")
	}
	if data.JoyCoderUser.UserID != "user-456" {
		t.Errorf("UserID = %q, want %q", data.JoyCoderUser.UserID, "user-456")
	}
}

func TestStateData_EmptyPtKey(t *testing.T) {
	raw := `{"joyCoderUser":{"ptKey":"","userId":"user-789"}}`
	var data ideStateData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if data.JoyCoderUser.PtKey != "" {
		t.Errorf("PtKey = %q, want empty string", data.JoyCoderUser.PtKey)
	}
	if data.JoyCoderUser.UserID != "user-789" {
		t.Errorf("UserID = %q, want %q", data.JoyCoderUser.UserID, "user-789")
	}
}

func TestStateData_EmptyUserID(t *testing.T) {
	raw := `{"joyCoderUser":{"ptKey":"some-key","userId":""}}`
	var data ideStateData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if data.JoyCoderUser.PtKey != "some-key" {
		t.Errorf("PtKey = %q, want %q", data.JoyCoderUser.PtKey, "some-key")
	}
	if data.JoyCoderUser.UserID != "" {
		t.Errorf("UserID = %q, want empty string", data.JoyCoderUser.UserID)
	}
}

func TestStateData_MissingJoyCoderUser(t *testing.T) {
	raw := `{"otherField":"some-value"}`
	var data ideStateData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if data.JoyCoderUser.PtKey != "" {
		t.Errorf("PtKey = %q, want empty string", data.JoyCoderUser.PtKey)
	}
	if data.JoyCoderUser.UserID != "" {
		t.Errorf("UserID = %q, want empty string", data.JoyCoderUser.UserID)
	}
}

func TestLoadFromSystem_HomeEnvError(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("skipping darwin-specific test on non-darwin")
	}
	origHome := os.Getenv("HOME")
	origXDG := os.Getenv("XDG_CONFIG_HOME")
	os.Unsetenv("HOME")
	os.Unsetenv("XDG_CONFIG_HOME")
	defer func() {
		os.Setenv("HOME", origHome)
		if origXDG != "" {
			os.Setenv("XDG_CONFIG_HOME", origXDG)
		}
	}()

	_, err := LoadFromSystem()
	if err == nil {
		t.Fatal("expected error when HOME is unset, got nil")
	}
	t.Logf("got expected error: %v", err)
}

func TestLoadFromSystem_InvalidDatabase(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("skipping darwin-specific test on non-darwin")
	}
	tmpDir := t.TempDir()

	dbDir := filepath.Join(tmpDir, "Library", "Application Support",
		"JoyCode", "User", "globalStorage")
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		t.Fatalf("failed to create db directory: %v", err)
	}

	dbPath := filepath.Join(dbDir, "state.vscdb")
	if err := os.WriteFile(dbPath, []byte("this is not a sqlite database"), 0644); err != nil {
		t.Fatalf("failed to write fake database: %v", err)
	}

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	_, err := LoadFromSystem()
	if err == nil {
		t.Fatal("expected error for invalid database file, got nil")
	}
	t.Logf("got expected error: %v", err)
}

func TestLoadFromSystem_ValidDatabase(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("skipping darwin-specific test on non-darwin")
	}
	tmpDir := t.TempDir()

	createTestDB(t, tmpDir, `{"joyCoderUser":{"ptKey":"valid-pt-key-abc","userId":"valid-user-xyz"}}`)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	creds, err := LoadFromSystem()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if creds.PtKey != "valid-pt-key-abc" {
		t.Errorf("PtKey = %q, want %q", creds.PtKey, "valid-pt-key-abc")
	}
	if creds.UserID != "valid-user-xyz" {
		t.Errorf("UserID = %q, want %q", creds.UserID, "valid-user-xyz")
	}
}

func TestLoadFromSystem_DatabaseMissingKey(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("skipping darwin-specific test on non-darwin")
	}
	tmpDir := t.TempDir()

	dbDir := filepath.Join(tmpDir, "Library", "Application Support",
		"JoyCode", "User", "globalStorage")
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		t.Fatalf("failed to create db directory: %v", err)
	}

	dbPath := filepath.Join(dbDir, "state.vscdb")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to create sqlite database: %v", err)
	}
	defer db.Close()

	_, err = db.Exec("CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT)")
	if err != nil {
		t.Fatalf("failed to create ItemTable: %v", err)
	}
	_, err = db.Exec("INSERT INTO ItemTable (key, value) VALUES ('some.other.key', 'irrelevant')")
	if err != nil {
		t.Fatalf("failed to insert row: %v", err)
	}
	db.Close()

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	_, err = LoadFromSystem()
	if err == nil {
		t.Fatal("expected error when JoyCoder.IDE key is missing, got nil")
	}
	t.Logf("got expected error: %v", err)
}

func TestLoadFromSystem_DatabaseInvalidJSON(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("skipping darwin-specific test on non-darwin")
	}
	tmpDir := t.TempDir()

	createTestDB(t, tmpDir, `{not valid json!!!}`)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	_, err := LoadFromSystem()
	if err == nil {
		t.Fatal("expected error for invalid JSON in database, got nil")
	}
	t.Logf("got expected error: %v", err)
}

func TestParsePlugin_JdhLoginInfo(t *testing.T) {
	raw := `{"jdhLoginInfo":{"ptKey":"BJ.plugin-key","userId":"jd_user","loginType":"ERP","tenant":"JD"}}`
	cred, err := parsePlugin([]byte(raw))
	if err != nil {
		t.Fatalf("parsePlugin failed: %v", err)
	}
	if cred.PtKey != "BJ.plugin-key" {
		t.Errorf("PtKey = %q, want %q", cred.PtKey, "BJ.plugin-key")
	}
	if cred.UserID != "jd_user" {
		t.Errorf("UserID = %q, want %q", cred.UserID, "jd_user")
	}
	if cred.LoginType != "ERP" {
		t.Errorf("LoginType = %q, want %q", cred.LoginType, "ERP")
	}
}

func TestLoadFromSystem_VSCodePlugin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("skipping darwin-specific test on non-darwin")
	}
	tmpDir := t.TempDir()

	createVSCodeTestDB(t, tmpDir, `{"jdhLoginInfo":{"ptKey":"plugin-pt-key","userId":"plugin-user","loginType":"ERP","tenant":"JD"}}`)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	creds, err := LoadFromSystem()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if creds.PtKey != "plugin-pt-key" {
		t.Errorf("PtKey = %q, want %q", creds.PtKey, "plugin-pt-key")
	}
	if creds.UserID != "plugin-user" {
		t.Errorf("UserID = %q, want %q", creds.UserID, "plugin-user")
	}
}

func TestLoadFromSystem_LinuxVSCodePlugin(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("skipping linux-specific test on non-linux")
	}
	tmpDir := t.TempDir()

	createLinuxVSCodeTestDB(t, tmpDir, `{"jdhLoginInfo":{"ptKey":"linux-pt-key","userId":"linux-user","loginType":"ERP","tenant":"JD"}}`)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	creds, err := LoadFromSystem()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if creds.PtKey != "linux-pt-key" {
		t.Errorf("PtKey = %q, want %q", creds.PtKey, "linux-pt-key")
	}
	if creds.UserID != "linux-user" {
		t.Errorf("UserID = %q, want %q", creds.UserID, "linux-user")
	}
}

func TestLoadFromSystem_VSCodeStateDBEnv(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "state.vscdb")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT)"); err != nil {
		db.Close()
		t.Fatalf("create table: %v", err)
	}
	_, err = db.Exec("INSERT INTO ItemTable (key, value) VALUES ('JoyCoder.joycoder-fe', ?)",
		`{"jdhLoginInfo":{"ptKey":"env-pt-key","userId":"env-user"}}`)
	db.Close()
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	os.Setenv("JOYCODE_VSCODE_STATE_DB", dbPath)
	defer os.Unsetenv("JOYCODE_VSCODE_STATE_DB")

	creds, err := LoadFromSystem()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if creds.PtKey != "env-pt-key" {
		t.Errorf("PtKey = %q, want %q", creds.PtKey, "env-pt-key")
	}
	if creds.UserID != "env-user" {
		t.Errorf("UserID = %q, want %q", creds.UserID, "env-user")
	}
}

func createTestDB(t *testing.T, baseDir string, jsonValue string) {	t.Helper()

	dbDir := filepath.Join(baseDir, "Library", "Application Support",
		"JoyCode", "User", "globalStorage")
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		t.Fatalf("failed to create db directory: %v", err)
	}

	dbPath := filepath.Join(dbDir, "state.vscdb")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to create sqlite database: %v", err)
	}

	_, err = db.Exec("CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT)")
	if err != nil {
		db.Close()
		t.Fatalf("failed to create ItemTable: %v", err)
	}

	_, err = db.Exec("INSERT INTO ItemTable (key, value) VALUES ('JoyCoder.IDE', ?)", jsonValue)
	if err != nil {
		db.Close()
		t.Fatalf("failed to insert test data: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("failed to close database after setup: %v", err)
	}
}

// createVSCodeTestDB 在 baseDir 下构造 VS Code 插件的 globalStorage/state.vscdb，
// 键为 JoyCoder.joycoder-fe，载荷在 jdhLoginInfo 下。
func createVSCodeTestDB(t *testing.T, baseDir string, jsonValue string) {
	t.Helper()

	dbDir := filepath.Join(baseDir, "Library", "Application Support",
		"Code", "User", "globalStorage")
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		t.Fatalf("failed to create db directory: %v", err)
	}

	dbPath := filepath.Join(dbDir, "state.vscdb")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to create sqlite database: %v", err)
	}

	_, err = db.Exec("CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT)")
	if err != nil {
		db.Close()
		t.Fatalf("failed to create ItemTable: %v", err)
	}

	_, err = db.Exec("INSERT INTO ItemTable (key, value) VALUES ('JoyCoder.joycoder-fe', ?)", jsonValue)
	if err != nil {
		db.Close()
		t.Fatalf("failed to insert test data: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("failed to close database after setup: %v", err)
	}
}

// createLinuxVSCodeTestDB 在 baseDir 下构造 Linux VS Code 插件的
// ~/.config/Code/User/globalStorage/state.vscdb，键为 JoyCoder.joycoder-fe。
func createLinuxVSCodeTestDB(t *testing.T, baseDir string, jsonValue string) {
	t.Helper()

	dbDir := filepath.Join(baseDir, ".config", "Code", "User", "globalStorage")
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		t.Fatalf("failed to create db directory: %v", err)
	}

	dbPath := filepath.Join(dbDir, "state.vscdb")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to create sqlite database: %v", err)
	}

	if _, err := db.Exec("CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT)"); err != nil {
		db.Close()
		t.Fatalf("failed to create ItemTable: %v", err)
	}

	if _, err := db.Exec("INSERT INTO ItemTable (key, value) VALUES ('JoyCoder.joycoder-fe', ?)", jsonValue); err != nil {
		db.Close()
		t.Fatalf("failed to insert test data: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("failed to close database after setup: %v", err)
	}
}
