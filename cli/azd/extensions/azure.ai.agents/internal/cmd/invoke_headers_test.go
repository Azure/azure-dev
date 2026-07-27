// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseCustomHeaders(t *testing.T) {
	t.Parallel()

	t.Run("nil for no entries", func(t *testing.T) {
		t.Parallel()
		got, err := parseCustomHeaders(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil header, got %v", got)
		}
	})

	t.Run("parses and canonicalizes name, trims value", func(t *testing.T) {
		t.Parallel()
		got, err := parseCustomHeaders([]string{"x-client-request-id:  abc123  "})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v := got.Get("X-Client-Request-Id"); v != "abc123" {
			t.Fatalf("expected trimmed canonical value, got %q", v)
		}
	})

	t.Run("repeated name adds multiple values", func(t *testing.T) {
		t.Parallel()
		got, err := parseCustomHeaders([]string{"X-Client-Tag: a", "X-Client-Tag: b"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		values := got.Values("X-Client-Tag")
		if len(values) != 2 || values[0] != "a" || values[1] != "b" {
			t.Fatalf("expected [a b], got %v", values)
		}
	})

	t.Run("allows empty value", func(t *testing.T) {
		t.Parallel()
		got, err := parseCustomHeaders([]string{"X-Client-Empty:"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := got["X-Client-Empty"]; !ok {
			t.Fatalf("expected header to be present with empty value, got %v", got)
		}
	})

	t.Run("accepts prefix case-insensitively", func(t *testing.T) {
		t.Parallel()
		got, err := parseCustomHeaders([]string{"X-CLIENT-Foo: bar"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v := got.Get("X-Client-Foo"); v != "bar" {
			t.Fatalf("expected header to be accepted, got %q", v)
		}
	})

	t.Run("rejects missing colon", func(t *testing.T) {
		t.Parallel()
		if _, err := parseCustomHeaders([]string{"NoColonHere"}); err == nil {
			t.Fatal("expected error for entry without a colon")
		}
	})

	t.Run("rejects empty name", func(t *testing.T) {
		t.Parallel()
		if _, err := parseCustomHeaders([]string{":value"}); err == nil {
			t.Fatal("expected error for empty header name")
		}
	})

	t.Run("rejects invalid name", func(t *testing.T) {
		t.Parallel()
		if _, err := parseCustomHeaders([]string{"x-client-bad name: value"}); err == nil {
			t.Fatal("expected error for header name with a space")
		}
	})

	t.Run("rejects non x-client name", func(t *testing.T) {
		t.Parallel()
		if _, err := parseCustomHeaders([]string{"X-Custom-Header: value"}); err == nil {
			t.Fatal("expected error for header name outside the x-client- family")
		}
	})
}

func TestIsValidHeaderName(t *testing.T) {
	t.Parallel()

	valid := []string{"X-Client-Request-Id", "Authorization", "a", "X-Foo_Bar", "x.y", "A1!#$%&'*+-.^_`|~"}
	for _, name := range valid {
		if !isValidHeaderName(name) {
			t.Errorf("expected %q to be valid", name)
		}
	}

	invalid := []string{"has space", "with:colon", "tab\ttab", "new\nline"}
	for _, name := range invalid {
		if isValidHeaderName(name) {
			t.Errorf("expected %q to be invalid", name)
		}
	}
}

func TestApplyCustomHeaders(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "http://localhost/responses", nil)
	headers := http.Header{
		"X-Client-Request-Id": {"abc"},
		"X-Client-Tag":        {"a", "b"},
	}
	applyCustomHeaders(req, headers)

	if v := req.Header.Get("X-Client-Request-Id"); v != "abc" {
		t.Fatalf("expected header to be applied, got %q", v)
	}
	if values := req.Header.Values("X-Client-Tag"); len(values) != 2 {
		t.Fatalf("expected 2 values for X-Client-Tag, got %v", values)
	}

	// Simulate builder order: custom applied first, managed overrides.
	req2 := httptest.NewRequest(http.MethodPost, "http://localhost/responses", nil)
	applyCustomHeaders(req2, http.Header{"Content-Type": {"text/plain"}})
	req2.Header.Set("Content-Type", "application/json")
	if v := req2.Header.Get("Content-Type"); v != "application/json" {
		t.Fatalf("expected managed Content-Type to win, got %q", v)
	}
}
