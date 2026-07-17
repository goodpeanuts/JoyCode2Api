package configref

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vibe-coding-labs/JoyCodeProxy/pkg/auth"
	"github.com/vibe-coding-labs/JoyCodeProxy/pkg/joycode"
	"github.com/vibe-coding-labs/JoyCodeProxy/pkg/store"
)

// RefreshStatus tracks the state of the last config refresh.
type RefreshStatus struct {
	LastRefresh time.Time `json:"last_refresh"`
	Success     bool      `json:"success"`
	Error       string    `json:"error,omitempty"`
	Duration    string    `json:"duration,omitempty"`
	ModelCount  int       `json:"model_count"`
	RefreshedBy string    `json:"refreshed_by"`
}

// ConfigRefresher runs periodic config refresh from JoyCode API.
// It caches model lists and plugin configs in memory and persists them to SQLite.
type ConfigRefresher struct {
	store  *store.Store
	mu     sync.RWMutex
	status RefreshStatus

	// In-memory caches for fast reads
	modelList     []joycode.ModelInfo
	modelNames    []string                // visible (non-hidden) model chatApiModel values
	pluginConfigs map[string]interface{}  // sceneType -> parsed value

	running bool
	stopCh  chan struct{}
}

// NewConfigRefresher creates a new config refresher.
// It loads cached configs from SQLite on startup so data is available immediately.
func NewConfigRefresher(s *store.Store) *ConfigRefresher {
	r := &ConfigRefresher{
		store:         s,
		stopCh:        make(chan struct{}),
		pluginConfigs: make(map[string]interface{}),
	}
	r.loadFromStore()
	return r
}

// GetModelList returns the cached full model info list.
func (r *ConfigRefresher) GetModelList() []joycode.ModelInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.modelList
}

// GetModelNames returns the cached list of visible model names (chatApiModel values).
// Hidden models are excluded.
func (r *ConfigRefresher) GetModelNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.modelNames
}

// GetAllPluginConfigs returns a copy of all cached plugin configs.
func (r *ConfigRefresher) GetAllPluginConfigs() map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(map[string]interface{}, len(r.pluginConfigs))
	for k, v := range r.pluginConfigs {
		result[k] = v
	}
	return result
}

// GetStatus returns the last refresh status.
func (r *ConfigRefresher) GetStatus() RefreshStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

// Start begins the periodic config refresh loop.
func (r *ConfigRefresher) Start(interval time.Duration) {
	if r.running {
		return
	}
	r.running = true

	// Initial refresh in background
	go r.refresh()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.refresh()
			case <-r.stopCh:
				return
			}
		}
	}()
	slog.Info("configref: started", "interval", interval)
}

// Stop terminates the refresh loop.
func (r *ConfigRefresher) Stop() {
	if r.running {
		r.running = false
		close(r.stopCh)
		slog.Info("configref: stopped")
	}
}

// RefreshNow triggers an immediate refresh and returns the status.
// If userID is non-empty, uses that account's credentials; otherwise uses default.
func (r *ConfigRefresher) RefreshNow(userID string) RefreshStatus {
	r.refreshWithUser(userID)
	return r.GetStatus()
}

// loadFromStore loads cached configs from SQLite on startup.
func (r *ConfigRefresher) loadFromStore() {
	// Load model list
	if raw := r.store.GetRemoteConfig("model_list"); raw != "" {
		var models []joycode.ModelInfo
		if err := json.Unmarshal([]byte(raw), &models); err == nil && len(models) > 0 {
			r.modelList = models
			r.modelNames = extractVisibleModelNames(models)
			slog.Info("configref: loaded cached model list from store", "count", len(models))
		}
	}

	// Load plugin configs
	for _, sceneType := range pluginConfigSceneTypes {
		key := "plugin_config_" + sceneType
		if raw := r.store.GetRemoteConfig(key); raw != "" {
			var val interface{}
			if json.Unmarshal([]byte(raw), &val) == nil {
				r.pluginConfigs[sceneType] = val
			}
		}
	}

	// Load status
	if raw := r.store.GetRemoteConfig("config_refresh_status"); raw != "" {
		json.Unmarshal([]byte(raw), &r.status)
	}
}

// pluginConfigSceneTypes lists all sceneType values we fetch.
var pluginConfigSceneTypes = []string{
	"jifei_status",
	"jifei_model",
	"jifei_message",
	"integral_default_model",
	"billingRate",
	"customModelList",
	"notification_message",
	"whiteList",
	"statCodeV2",
}

// refresh performs one round of config fetching using the default account.
func (r *ConfigRefresher) refresh() {
	r.refreshWithUser("")
}

// refreshWithUser performs one round of config fetching.
// If userID is non-empty, uses that account's credentials.
func (r *ConfigRefresher) refreshWithUser(userID string) {
	start := time.Now()
	slog.Info("configref: refresh round started", "user_id", userID)

	client, usedUserID, err := r.resolveClient(userID)
	if err != nil {
		slog.Warn("configref: no credentials available, skipping", "error", err)
		r.setStatus(false, err.Error(), start, "")
		return
	}

	var firstErr error
	modelCount := 0

	// 1. Fetch model list
	models, err := client.ListModels()
	if err != nil {
		slog.Error("configref: fetch model list failed", "error", err)
		firstErr = err
	} else {
		modelCount = len(models)
		data, _ := json.Marshal(models)
		r.store.SetRemoteConfig("model_list", string(data))

		// Also store visible model names
		visible := extractVisibleModelNames(models)
		namesData, _ := json.Marshal(visible)
		r.store.SetRemoteConfig("model_names", string(namesData))

		r.mu.Lock()
		r.modelList = models
		r.modelNames = visible
		r.mu.Unlock()

		slog.Info("configref: fetched model list", "count", modelCount, "visible", len(visible))
	}

	// 2. Fetch plugin configs
	for _, sceneType := range pluginConfigSceneTypes {
		val, err := client.FetchPluginConfig(sceneType)
		if err != nil {
			slog.Warn("configref: fetch plugin_config failed", "sceneType", sceneType, "error", err)
			continue
		}
		data, err := json.Marshal(val)
		if err != nil {
			slog.Warn("configref: marshal plugin_config failed", "sceneType", sceneType, "error", err)
			continue
		}
		key := "plugin_config_" + sceneType
		r.store.SetRemoteConfig(key, string(data))

		r.mu.Lock()
		r.pluginConfigs[sceneType] = val
		r.mu.Unlock()
	}

	// 3. Fetch strategy config
	strategy, err := client.FetchModelStrategyConfig()
	if err != nil {
		slog.Warn("configref: fetch strategy config failed", "error", err)
	} else if strategy != nil {
		data, _ := json.Marshal(strategy)
		r.store.SetRemoteConfig("strategy_config", string(data))
	}

	// 4. Fetch error config
	errorConfig, err := client.FetchModelErrorConfig()
	if err != nil {
		slog.Warn("configref: fetch error config failed", "error", err)
	} else if errorConfig != nil {
		data, _ := json.Marshal(errorConfig)
		r.store.SetRemoteConfig("error_config", string(data))
	}

	// 5. Refresh each account's colorBaseUrl from joycode_userInfo.
	//    Runs on the same cycle as config refresh (first run at startup + every interval).
	RefreshAllAccountsColorBaseURL(r.store)

	if firstErr != nil {
		r.setStatus(false, firstErr.Error(), start, usedUserID)
	} else {
		r.setStatus(true, "", start, usedUserID)
		slog.Info("configref: refresh completed",
			"duration", time.Since(start).Round(time.Millisecond),
			"models", modelCount,
			"user_id", usedUserID,
		)
	}
}

// setStatus updates the refresh status in memory and persists it.
func (r *ConfigRefresher) setStatus(success bool, errMsg string, start time.Time, usedUserID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.status = RefreshStatus{
		LastRefresh: time.Now(),
		Success:     success,
		Error:       errMsg,
		Duration:    time.Since(start).Round(time.Millisecond).String(),
		ModelCount:  len(r.modelList),
		RefreshedBy: usedUserID,
	}

	data, _ := json.Marshal(r.status)
	r.store.SetRemoteConfig("config_refresh_status", string(data))
}

// resolveClient creates a joycode.Client from available credentials.
// If userID is specified, uses that account. Otherwise tries default account,
// then first valid account, then system credentials.
func (r *ConfigRefresher) resolveClient(userID string) (*joycode.Client, string, error) {
	dialect := func(cl *joycode.Client) {
		cl.SetDialect(
			r.store.GetSetting("source_type"),
			r.store.GetSetting("client_name"),
			r.store.GetSetting("client_version"),
			r.store.GetSetting("user_agent"),
			r.store.GetSetting("login_type"),
			r.store.GetSetting("tenant"),
		)
		cl.SetTimeout(30 * time.Second)
	}

	// If userID specified, use that account
	if userID != "" {
		acc, err := r.store.GetAccount(userID)
		if err != nil || acc == nil {
			return nil, "", fmt.Errorf("account %q not found: %w", userID, err)
		}
		cl := joycode.NewClient(acc.PtKey, acc.UserID)
		cl.SetColorContext("", "", "", "", "") // will be set by dialect
		dialect(cl)
		return cl, acc.UserID, nil
	}

	// Try default account
	if acc, err := r.store.GetDefaultAccount(); err == nil && acc != nil {
		cl := joycode.NewClient(acc.PtKey, acc.UserID)
		dialect(cl)
		return cl, acc.UserID, nil
	}

	// Try first valid account
	if accounts, err := r.store.ListAccounts(); err == nil && len(accounts) > 0 {
		for _, info := range accounts {
			if info.CredentialValid != 0 { // -1 (unknown) or 1 (valid) is acceptable
				acc, err := r.store.GetAccount(info.UserID)
				if err == nil && acc != nil {
					cl := joycode.NewClient(acc.PtKey, acc.UserID)
					dialect(cl)
					return cl, acc.UserID, nil
				}
			}
		}
		// Fall back to first account even if credential_valid=0
		if acc, err := r.store.GetAccount(accounts[0].UserID); err == nil && acc != nil {
			cl := joycode.NewClient(acc.PtKey, acc.UserID)
			dialect(cl)
			return cl, acc.UserID, nil
		}
	}

	// Try system credentials
	if creds, err := auth.LoadFromSystem(); err == nil && creds.PtKey != "" {
		cl := joycode.NewClient(creds.PtKey, creds.UserID)
		cl.SetColorContext(creds.ColorBaseURL, creds.MasterBaseURL, creds.Tenant, creds.LoginType, creds.OrgFullName)
		dialect(cl)
		return cl, creds.UserID, nil
	}

	return nil, "", fmt.Errorf("no accounts available for config refresh")
}

// extractVisibleModelNames returns chatApiModel values for non-hidden models.
func extractVisibleModelNames(models []joycode.ModelInfo) []string {
	names := make([]string, 0, len(models))
	for _, m := range models {
		if m.IsHidden || m.Hidden {
			continue
		}
		names = append(names, m.ChatAPIModel)
	}
	return names
}
