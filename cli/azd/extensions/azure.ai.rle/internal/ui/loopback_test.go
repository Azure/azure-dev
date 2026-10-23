// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ui

import (
	"net/http/httptest"
	"testing"
)

func TestLoopbackRequestValidation(t *testing.T) {
	const host = "127.0.0.1:12345"
	for _, tc := range []struct {
		name, host, origin string
		want               bool
	}{
		{"local", host, "", true},
		{"same origin", host, "http://" + host, true},
		{"foreign host", "evil.test", "", false},
		{"foreign origin", host, "http://evil.test", false},
		{"wrong port", host, "http://127.0.0.1:12346", false},
		{"opaque origin", host, "null", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", "http://"+tc.host+"/", nil)
			request.Header.Set("Origin", tc.origin)
			got := ValidateLoopbackRequest(httptest.NewRecorder(), request, host)
			if got != tc.want {
				t.Fatalf("expected %t, got %t", tc.want, got)
			}
		})
	}
}

func TestNewSessionToken(t *testing.T) {
	first, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 64 || first == second {
		t.Fatal("expected distinct 256-bit session tokens")
	}
}
