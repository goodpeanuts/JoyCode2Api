package auth

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	_ "github.com/mattn/go-sqlite3"
)

// Credentials holds JoyCode authentication data.
type Credentials struct {
	PtKey         string
	UserID        string
	ColorBaseURL  string
	MasterBaseURL string
	Tenant        string
	LoginType     string
	OrgFullName   string
}

// loginFields 是 JoyCoder IDE 与 VS Code 插件共有的登录态字段（仅外层 JSON key 不同）。
type loginFields struct {
	PtKey         string `json:"ptKey"`
	UserID        string `json:"userId"`
	ColorBaseURL  string `json:"colorBaseUrl"`
	MasterBaseURL string `json:"masterBaseUrl"`
	Tenant        string `json:"tenant"`
	LoginType     string `json:"loginType"`
	OrgFullName   string `json:"orgFullName"`
}

// JoyCoder IDE 桌面端：state.vscdb 键 JoyCoder.IDE，载荷在 joyCoderUser 下。
type ideStateData struct {
	JoyCoderUser loginFields `json:"joyCoderUser"`
}

// JoyCode VS Code 插件 joycoder.joycoder-fe：state.vscdb 键 JoyCoder.joycoder-fe，载荷在 jdhLoginInfo 下。
type pluginStateData struct {
	JdhLoginInfo loginFields `json:"jdhLoginInfo"`
}

const (
	// stateDBEnv 显式指向 JoyCoder IDE 的 state.vscdb（历史行为）。
	stateDBEnv = "JOYCODE_STATE_DB"
	// vscodeStateDBEnv 显式指向 JoyCode VS Code 插件所在的 state.vscdb。
	vscodeStateDBEnv = "JOYCODE_VSCODE_STATE_DB"

	containerStateDB = "/root/.joycode-ide/state.vscdb"

	ideItemKey    = "JoyCoder.IDE"
	pluginItemKey = "JoyCoder.joycoder-fe"
)

// LoadFromSystem 自动从本机已登录的 JoyCode 凭据源读取 ptKey/userId 等。
// 查找顺序（取第一个能解析出非空 ptKey+userId 的源）：
//  1. JOYCODE_STATE_DB（显式，IDE）
//  2. JOYCODE_VSCODE_STATE_DB（显式，VS Code 插件）
//  3. 容器内 IDE 路径（Docker 挂载）
//  4. macOS VS Code 插件全局 state.vscdb
//  5. macOS JoyCoder IDE state.vscdb
func LoadFromSystem() (*Credentials, error) {
	home, homeErr := os.UserHomeDir()

	type src struct {
		path    string
		itemKey string
		parse   func([]byte) (*Credentials, error)
		note    string
	}

	var sources []src
	if p := os.Getenv(stateDBEnv); p != "" {
		sources = append(sources, src{p, ideItemKey, parseIDE, "JOYCODE_STATE_DB"})
	}
	if p := os.Getenv(vscodeStateDBEnv); p != "" {
		sources = append(sources, src{p, pluginItemKey, parsePlugin, "JOYCODE_VSCODE_STATE_DB"})
	}
	sources = append(sources, src{containerStateDB, ideItemKey, parseIDE, "container IDE state"})

	if homeErr == nil {
		vscodeDB := filepath.Join(home,
			"Library", "Application Support",
			"Code", "User", "globalStorage", "state.vscdb")
		sources = append(sources, src{vscodeDB, pluginItemKey, parsePlugin, "VS Code plugin state"})

		ideDB := filepath.Join(home,
			"Library", "Application Support",
			"JoyCode", "User", "globalStorage", "state.vscdb")
		sources = append(sources, src{ideDB, ideItemKey, parseIDE, "JoyCoder IDE state"})
	}

	var lastErr error
	for _, s := range sources {
		if s.path == "" {
			continue
		}
		if _, err := os.Stat(s.path); err != nil {
			continue // 该源不存在，继续尝试下一个
		}
		cred, err := loadFromStateDB(s.path, s.itemKey, s.parse)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", s.note, err)
			continue
		}
		return cred, nil
	}

	if runtime.GOOS != "darwin" && homeErr != nil {
		return nil, fmt.Errorf("auto credential extraction requires macOS, or mount a state.vscdb and set %s/%s", stateDBEnv, vscodeStateDBEnv)
	}
	if lastErr != nil {
		return nil, fmt.Errorf("no usable JoyCode login found (last tried %w); please log in to the JoyCode VS Code plugin or JoyCoder IDE", lastErr)
	}
	return nil, fmt.Errorf("no JoyCode state database found; please install and log in to the JoyCode VS Code plugin (or JoyCoder IDE), or set %s/%s", vscodeStateDBEnv, stateDBEnv)
}

// loadFromStateDB 打开 SQLite 只读，按 itemKey 取值并用 parse 解析为凭据。
func loadFromStateDB(dbPath, itemKey string, parse func([]byte) (*Credentials, error)) (*Credentials, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("state database not found at %s: %w", dbPath, err)
	}

	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("cannot open state database: %w", err)
	}
	defer db.Close()

	var value string
	if err := db.QueryRow(
		"SELECT value FROM ItemTable WHERE key=?", itemKey,
	).Scan(&value); err != nil {
		return nil, fmt.Errorf("login info (key %s) not found", itemKey)
	}

	cred, err := parse([]byte(value))
	if err != nil {
		return nil, err
	}
	if cred.PtKey == "" {
		return nil, fmt.Errorf("ptKey is empty in stored credentials")
	}
	if cred.UserID == "" {
		return nil, fmt.Errorf("userId is empty in stored credentials")
	}
	return cred, nil
}

func parseIDE(data []byte) (*Credentials, error) {
	var s ideStateData
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("cannot parse IDE login data: %w", err)
	}
	return fromFields(s.JoyCoderUser), nil
}

func parsePlugin(data []byte) (*Credentials, error) {
	var s pluginStateData
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("cannot parse plugin login data: %w", err)
	}
	return fromFields(s.JdhLoginInfo), nil
}

func fromFields(f loginFields) *Credentials {
	return &Credentials{
		PtKey:         f.PtKey,
		UserID:        f.UserID,
		ColorBaseURL:  f.ColorBaseURL,
		MasterBaseURL: f.MasterBaseURL,
		Tenant:        f.Tenant,
		LoginType:     f.LoginType,
		OrgFullName:   f.OrgFullName,
	}
}
