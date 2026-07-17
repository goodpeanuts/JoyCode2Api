package configref

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/vibe-coding-labs/JoyCodeProxy/pkg/joycode"
	"github.com/vibe-coding-labs/JoyCodeProxy/pkg/store"
)

// newUserInfoClient builds the joycode client used for colorBaseUrl refresh.
// It is a package var so tests can point the client at a mock server.
var newUserInfoClient = func(ptKey, userID string) *joycode.Client {
	return joycode.NewClient(ptKey, userID)
}

// RefreshAccountColorBaseURL calls joycode_userInfo with the account's ptKey and,
// on success, writes the returned colorBaseUrl (plus master/tenant/orgFullName when
// present) back to the account. On any failure the stored colorBaseUrl is left
// unchanged. Either way the per-account color refresh status (time + error) is updated.
// Returns the new colorBaseUrl on success, or ("", err) on failure.
func RefreshAccountColorBaseURL(s *store.Store, userID string) (string, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	acc, err := s.GetAccount(userID)
	if err != nil || acc == nil {
		e := fmt.Errorf("account %q not found: %w", userID, err)
		s.UpdateAccountColorStatus(userID, now, e.Error())
		return "", e
	}

	client := newUserInfoClient(acc.PtKey, acc.UserID)
	resp, err := client.UserInfo()
	if err != nil {
		s.UpdateAccountColorStatus(userID, now, err.Error())
		return "", err
	}
	code, _ := resp["code"].(float64)
	if code != 0 {
		msg, _ := resp["msg"].(string)
		e := fmt.Errorf("userInfo error (code=%.0f): %s", code, msg)
		s.UpdateAccountColorStatus(userID, now, e.Error())
		return "", e
	}
	data, _ := resp["data"].(map[string]interface{})
	colorBaseURL, _ := data["colorBaseUrl"].(string)
	if colorBaseURL == "" {
		e := fmt.Errorf("userInfo returned empty colorBaseUrl")
		s.UpdateAccountColorStatus(userID, now, e.Error())
		return "", e
	}

	creds := &store.AccountCreds{ColorBaseURL: colorBaseURL}
	if v, ok := data["masterBaseUrl"].(string); ok {
		creds.MasterBaseURL = v
	}
	if v, ok := data["tenant"].(string); ok {
		creds.Tenant = v
	}
	if v, ok := data["orgFullName"].(string); ok {
		creds.OrgFullName = v
	}
	if err := s.UpdateAccountCreds(userID, creds); err != nil {
		s.UpdateAccountColorStatus(userID, now, err.Error())
		return "", err
	}
	s.UpdateAccountColorStatus(userID, now, "")
	return colorBaseURL, nil
}

// RefreshAllAccountsColorBaseURL refreshes colorBaseUrl for every account.
// A single account's failure is logged and does not stop the others.
func RefreshAllAccountsColorBaseURL(s *store.Store) {
	accounts, err := s.ListAccounts()
	if err != nil {
		slog.Warn("configref: list accounts for color refresh failed", "error", err)
		return
	}
	for _, a := range accounts {
		if _, err := RefreshAccountColorBaseURL(s, a.UserID); err != nil {
			slog.Warn("configref: colorBaseUrl refresh failed", "user_id", a.UserID, "error", err)
		}
	}
}
