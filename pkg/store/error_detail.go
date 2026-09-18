package store

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type errorDetailCtxKey struct{}

// SetErrorDetail stores parsed upstream error detail in request context.
func SetErrorDetail(r *http.Request, detail string) {
	*r = *r.WithContext(context.WithValue(r.Context(), errorDetailCtxKey{}, detail))
}

// GetErrorDetail retrieves upstream error detail from request context.
func GetErrorDetail(r *http.Request) string {
	if v, ok := r.Context().Value(errorDetailCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// ParseUpstreamErrorDetail extracts structured error info from an upstream
// error string (either a client.Post error like "API error 400: {...}" or an
// SSE data line like "{\"error\":{...}}"). Returns a JSON string with
// error_code, error_message, error_type, error_status fields, or "" on parse
// failure.
func ParseUpstreamErrorDetail(raw string) string {
	idx := strings.Index(raw, "{")
	if idx < 0 {
		return ""
	}
	body := raw[idx:]
	var errResp map[string]interface{}
	if json.Unmarshal([]byte(body), &errResp) != nil {
		return ""
	}
	if errObj, ok := errResp["error"].(map[string]interface{}); ok {
		detail := map[string]interface{}{
			"error_code":    errObj["code"],
			"error_message": errObj["message"],
			"error_type":    errObj["type"],
			"error_status":  errObj["status"],
		}
		b, _ := json.Marshal(detail)
		return string(b)
	}
	return ""
}