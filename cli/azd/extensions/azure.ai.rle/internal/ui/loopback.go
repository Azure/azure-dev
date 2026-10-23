// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ValidateLoopbackRequest rejects foreign hosts and browser origins.
func ValidateLoopbackRequest(w http.ResponseWriter, r *http.Request, expectedHost string) bool {
	if !strings.EqualFold(r.Host, expectedHost) {
		http.Error(w, "invalid host", http.StatusForbidden)
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	originURL, err := url.Parse(origin)
	if err != nil || originURL.Scheme != "http" || !strings.EqualFold(originURL.Host, expectedHost) {
		http.Error(w, "invalid origin", http.StatusForbidden)
		return false
	}
	return true
}

// NewSessionToken creates a random credential for a local browser session.
func NewSessionToken() (string, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return "", fmt.Errorf("create local UI session token: %w", err)
	}
	return hex.EncodeToString(token), nil
}
