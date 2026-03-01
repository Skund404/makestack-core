// Package auth handles API key authentication for the makestack-core REST API.
package auth

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
)

// ValidateRequest checks the incoming request for a valid API key.
// It accepts the key in two headers (checked in order):
//
//	Authorization: Bearer <key>
//	X-API-Key: <key>
//
// Returns nil if the key matches. If wantKey is empty, auth is disabled
// and every request passes. Uses constant-time comparison to prevent
// timing attacks.
func ValidateRequest(r *http.Request, wantKey string) error {
	if wantKey == "" {
		return nil // auth disabled
	}

	got := extractKey(r)
	if got == "" {
		return errors.New("missing API key: provide Authorization: Bearer <key> or X-API-Key header")
	}

	if subtle.ConstantTimeCompare([]byte(got), []byte(wantKey)) != 1 {
		return errors.New("invalid API key")
	}

	return nil
}

// extractKey pulls the raw key string from the request headers.
// Checks Authorization: Bearer <token> first, then X-API-Key.
func extractKey(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		if after, ok := strings.CutPrefix(auth, "Bearer "); ok {
			return strings.TrimSpace(after)
		}
	}
	return strings.TrimSpace(r.Header.Get("X-API-Key"))
}
