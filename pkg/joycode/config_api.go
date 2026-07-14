package joycode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
)

// PostByFunctionID calls the color gateway directly by functionId,
// bypassing the colorEndpoints path map. The body is sent raw (no prepareBody injection).
// Used for plugin_config, model_strategy_config, model_error_config_get, etc.
func (c *Client) PostByFunctionID(functionID string, body map[string]interface{}) (map[string]interface{}, error) {
	if c.ColorBaseURL == "" {
		return nil, fmt.Errorf("color gateway not configured (no ColorBaseURL)")
	}
	u, err := url.Parse(c.ColorBaseURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid ColorBaseURL: %s", c.ColorBaseURL)
	}
	query, sign := colorSign(functionID)
	fullURL := u.Scheme + "://" + u.Host + colorGatewayPath + "?" + query + "&sign=" + sign

	data, err := json.Marshal(body)
	if err != nil {
		slog.Error("marshal PostByFunctionID body", "functionId", functionID, "error", err)
		return nil, err
	}
	req, err := http.NewRequest("POST", fullURL, bytes.NewReader(data))
	if err != nil {
		slog.Error("create PostByFunctionID request", "functionId", functionID, "error", err)
		return nil, err
	}
	req.Header = c.headers()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Error("PostByFunctionID request failed", "functionId", functionID, "error", err)
		return nil, err
	}
	respData, err := decodeBody(resp)
	if err != nil {
		slog.Error("decode PostByFunctionID response", "functionId", functionID, "status", resp.StatusCode, "error", err)
		return nil, err
	}
	if resp.StatusCode != 200 {
		slog.Error("PostByFunctionID non-200", "functionId", functionID, "status", resp.StatusCode, "body", truncate(string(respData), 500))
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, truncate(string(respData), 500))
	}
	var result map[string]interface{}
	if err := json.Unmarshal(respData, &result); err != nil {
		slog.Error("unmarshal PostByFunctionID response", "functionId", functionID, "error", err)
		return nil, fmt.Errorf("invalid JSON response (parse error: %s): %s", err.Error(), truncate(string(respData), 500))
	}
	return result, nil
}

// FetchPluginConfig calls plugin_config with a specific sceneType.
// Returns the value for that sceneType from the response data, or nil if not found.
func (c *Client) FetchPluginConfig(sceneType string) (interface{}, error) {
	resp, err := c.PostByFunctionID("plugin_config", map[string]interface{}{
		"sceneType": sceneType,
	})
	if err != nil {
		return nil, fmt.Errorf("plugin_config(%s) request failed: %w", sceneType, err)
	}
	code, ok := resp["code"].(float64)
	if !ok || code != 0 {
		msg, _ := resp["msg"].(string)
		return nil, fmt.Errorf("plugin_config(%s) error (code=%.0f): %s", sceneType, code, msg)
	}
	data, ok := resp["data"].(map[string]interface{})
	if !ok || data == nil {
		return nil, nil
	}
	return data[sceneType], nil
}

// FetchModelStrategyConfig calls model_strategy_config and returns the strategy items.
func (c *Client) FetchModelStrategyConfig() ([]ModelStrategyItem, error) {
	resp, err := c.PostByFunctionID("model_strategy_config", map[string]interface{}{
		"enable":  true,
		"display": true,
	})
	if err != nil {
		return nil, fmt.Errorf("model_strategy_config request failed: %w", err)
	}
	code, ok := resp["code"].(float64)
	if !ok || code != 0 {
		msg, _ := resp["msg"].(string)
		return nil, fmt.Errorf("model_strategy_config error (code=%.0f): %s", code, msg)
	}
	data, ok := resp["data"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("model_strategy_config unexpected data format")
	}
	items := make([]ModelStrategyItem, 0, len(data))
	for _, item := range data {
		b, err := json.Marshal(item)
		if err != nil {
			continue
		}
		var s ModelStrategyItem
		if err := json.Unmarshal(b, &s); err != nil {
			continue
		}
		items = append(items, s)
	}
	return items, nil
}

// FetchModelErrorConfig calls model_error_config_get and returns the error config items.
func (c *Client) FetchModelErrorConfig() ([]ErrorConfigItem, error) {
	resp, err := c.PostByFunctionID("model_error_config_get", map[string]interface{}{})
	if err != nil {
		return nil, fmt.Errorf("model_error_config_get request failed: %w", err)
	}
	code, ok := resp["code"].(float64)
	if !ok || code != 0 {
		msg, _ := resp["msg"].(string)
		return nil, fmt.Errorf("model_error_config_get error (code=%.0f): %s", code, msg)
	}
	data, ok := resp["data"].(map[string]interface{})
	if !ok || data == nil {
		return nil, nil
	}
	errorList, ok := data["errorList"].([]interface{})
	if !ok {
		return nil, nil
	}
	items := make([]ErrorConfigItem, 0, len(errorList))
	for _, item := range errorList {
		b, err := json.Marshal(item)
		if err != nil {
			continue
		}
		var e ErrorConfigItem
		if err := json.Unmarshal(b, &e); err != nil {
			continue
		}
		items = append(items, e)
	}
	return items, nil
}
