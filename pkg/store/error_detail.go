package store

import (
	"context"
	"net/http"
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