package store

import (
	"testing"
)

func TestUpdateAccountColorStatus(t *testing.T) {
	s := openTestStore(t)
	creds := &AccountCreds{ColorBaseURL: "https://api-ai.jd.com"}
	if err := s.AddAccount("u1", "ptkey-1", "nick", true, "GLM-5.1", creds); err != nil {
		t.Fatalf("add account: %v", err)
	}

	if err := s.UpdateAccountColorStatus("u1", "2026-07-17T00:00:00Z", "boom"); err != nil {
		t.Fatalf("update color status: %v", err)
	}
	at, errMsg, err := s.GetAccountColorStatus("u1")
	if err != nil {
		t.Fatalf("get color status: %v", err)
	}
	if at != "2026-07-17T00:00:00Z" || errMsg != "boom" {
		t.Fatalf("got at=%q err=%q, want at=2026-07-17T00:00:00Z err=boom", at, errMsg)
	}

	// success path clears error
	if err := s.UpdateAccountColorStatus("u1", "2026-07-17T01:00:00Z", ""); err != nil {
		t.Fatalf("update color status 2: %v", err)
	}
	at, errMsg, _ = s.GetAccountColorStatus("u1")
	if at != "2026-07-17T01:00:00Z" || errMsg != "" {
		t.Fatalf("got at=%q err=%q, want cleared error", at, errMsg)
	}
}

func TestListAccountsIncludesColorStatus(t *testing.T) {
	s := openTestStore(t)
	if err := s.AddAccount("u2", "ptkey-2", "nick2", true, "GLM-5.1", &AccountCreds{}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	if err := s.UpdateAccountColorStatus("u2", "2026-07-17T02:00:00Z", "failed-x"); err != nil {
		t.Fatalf("update: %v", err)
	}
	accounts, err := s.ListAccounts()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, a := range accounts {
		if a.UserID == "u2" {
			found = true
			if a.ColorRefreshAt != "2026-07-17T02:00:00Z" || a.ColorRefreshError != "failed-x" {
				t.Fatalf("got at=%q err=%q", a.ColorRefreshAt, a.ColorRefreshError)
			}
		}
	}
	if !found {
		t.Fatal("u2 not in list")
	}
}
