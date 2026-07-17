package configref

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/vibe-coding-labs/JoyCodeProxy/pkg/joycode"
	"github.com/vibe-coding-labs/JoyCodeProxy/pkg/store"
)

func newColorTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// mockUserInfoServer returns a test server that replies with the given colorBaseUrl.
func mockUserInfoServer(t *testing.T, colorBaseURL string, code int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if code != 0 {
			w.Write([]byte(`{"code":999,"msg":"boom","data":null}`))
			return
		}
		w.Write([]byte(`{"code":0,"msg":"ok","data":{"userId":"u1","colorBaseUrl":"` + colorBaseURL + `"}}`))
	}))
}

func TestRefreshAccountColorBaseURL_Success(t *testing.T) {
	s := newColorTestStore(t)
	if err := s.AddAccount("u1", "ptkey", "nick", true, "GLM-5.1", &store.AccountCreds{ColorBaseURL: "https://old.example.com"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	srv := mockUserInfoServer(t, "https://new.example.com", 0)
	defer srv.Close()

	// Route the client's requests to the mock by overriding the UserInfo endpoint origin.
	origNew := newUserInfoClient
	newUserInfoClient = func(ptKey, userID string) *joycode.Client {
		c := joycode.NewClient(ptKey, userID)
		c.ColorBaseURL = srv.URL // color gateway origin -> mock
		return c
	}
	defer func() { newUserInfoClient = origNew }()

	url, err := RefreshAccountColorBaseURL(s, "u1")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if url != "https://new.example.com" {
		t.Fatalf("got %q, want https://new.example.com", url)
	}
	acc, _ := s.GetAccount("u1")
	if acc.ColorBaseURL != "https://new.example.com" {
		t.Fatalf("db colorBaseUrl = %q, want new", acc.ColorBaseURL)
	}
	at, errMsg, _ := s.GetAccountColorStatus("u1")
	if at == "" || errMsg != "" {
		t.Fatalf("status at=%q err=%q, want time set + no error", at, errMsg)
	}
}

func TestRefreshAccountColorBaseURL_FailureKeepsOldValue(t *testing.T) {
	s := newColorTestStore(t)
	if err := s.AddAccount("u1", "ptkey", "nick", true, "GLM-5.1", &store.AccountCreds{ColorBaseURL: "https://old.example.com"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	srv := mockUserInfoServer(t, "", 999)
	defer srv.Close()
	origNew := newUserInfoClient
	newUserInfoClient = func(ptKey, userID string) *joycode.Client {
		c := joycode.NewClient(ptKey, userID)
		c.ColorBaseURL = srv.URL
		return c
	}
	defer func() { newUserInfoClient = origNew }()

	url, err := RefreshAccountColorBaseURL(s, "u1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if url != "" {
		t.Fatalf("got url %q, want empty on failure", url)
	}
	acc, _ := s.GetAccount("u1")
	if acc.ColorBaseURL != "https://old.example.com" {
		t.Fatalf("old value not preserved: %q", acc.ColorBaseURL)
	}
	_, errMsg, _ := s.GetAccountColorStatus("u1")
	if errMsg == "" {
		t.Fatal("expected recorded error, got empty")
	}
}

func TestRefreshCycleRefreshesColorBaseURL(t *testing.T) {
	s := newColorTestStore(t)
	if err := s.AddAccount("u1", "ptkey", "nick", true, "GLM-5.1", &store.AccountCreds{ColorBaseURL: "https://old.example.com"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	srv := mockUserInfoServer(t, "https://cycle.example.com", 0)
	defer srv.Close()
	origNew := newUserInfoClient
	newUserInfoClient = func(ptKey, userID string) *joycode.Client {
		c := joycode.NewClient(ptKey, userID)
		c.ColorBaseURL = srv.URL
		return c
	}
	defer func() { newUserInfoClient = origNew }()

	// Directly exercise the all-accounts helper the cycle calls.
	RefreshAllAccountsColorBaseURL(s)

	acc, _ := s.GetAccount("u1")
	if acc.ColorBaseURL != "https://cycle.example.com" {
		t.Fatalf("colorBaseUrl not refreshed by cycle helper: %q", acc.ColorBaseURL)
	}
}
