// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// TestBuildActivityRequestBody_WrapsMessage verifies that a plain message
// argument is wrapped into a minimal message Activity.
func TestBuildActivityRequestBody_WrapsMessage(t *testing.T) {
	t.Parallel()

	a := &InvokeAction{flags: &invokeFlags{message: "Hello!"}}
	body, label, err := a.buildActivityRequestBody()
	if err != nil {
		t.Fatalf("buildActivityRequestBody: %v", err)
	}
	if label != `"Hello!"` {
		t.Errorf("label = %q, want %q", label, `"Hello!"`)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, string(body))
	}
	if got["type"] != activityMessageType {
		t.Errorf("type = %v, want %q", got["type"], activityMessageType)
	}
	if got["text"] != "Hello!" {
		t.Errorf("text = %v, want %q", got["text"], "Hello!")
	}
}

// TestBuildActivityRequestBody_PassesFileVerbatim verifies that --input-file
// contents are forwarded unchanged (the file is assumed to be a full Activity).
func TestBuildActivityRequestBody_PassesFileVerbatim(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "activity.json")
	content := `{"type":"message","text":"from file","channelId":"directline"}`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	a := &InvokeAction{flags: &invokeFlags{inputFile: path}}
	body, label, err := a.buildActivityRequestBody()
	if err != nil {
		t.Fatalf("buildActivityRequestBody: %v", err)
	}
	if string(body) != content {
		t.Errorf("body = %q, want verbatim %q", string(body), content)
	}
	if label == "" {
		t.Error("expected a non-empty body label for file input")
	}
}

func TestExtractActivityText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "single message activity", body: `{"type":"message","text":"hi there"}`, want: "hi there"},
		{name: "invoke response body.text", body: `{"body":{"text":"nested reply"}}`, want: "nested reply"},
		{name: "array of activities", body: `[{"text":"one"},{"text":"two"}]`, want: "one\ntwo"},
		{name: "array skips empty text", body: `[{"type":"typing"},{"text":"only"}]`, want: "only"},
		{name: "object without text", body: `{"type":"event","name":"ping"}`, want: ""},
		{name: "whitespace-only text ignored", body: `{"text":"   "}`, want: ""},
		{name: "plain json string", body: `"just a string"`, want: ""},
		{name: "invalid json", body: `not json`, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := extractActivityText([]byte(tt.body)); got != tt.want {
				t.Errorf("extractActivityText(%s) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

func TestActivityErrorFromBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		body      string
		wantErr   bool
		wantInMsg string
	}{
		{name: "error with code", body: `{"error":{"message":"boom","code":"E1"}}`, wantErr: true, wantInMsg: "agent error (E1): boom"},
		{name: "error with type only", body: `{"error":{"message":"boom","type":"BadThing"}}`, wantErr: true, wantInMsg: "agent error (BadThing): boom"},
		{name: "error without label", body: `{"error":{"message":"boom"}}`, wantErr: true, wantInMsg: "agent error: boom"},
		{name: "no error field", body: `{"text":"all good"}`, wantErr: false},
		{name: "invalid json", body: `not json`, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := activityErrorFromBody([]byte(tt.body))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.wantInMsg != "" && err.Error() != tt.wantInMsg {
					t.Errorf("error = %q, want %q", err.Error(), tt.wantInMsg)
				}
				return
			}
			if err != nil {
				t.Errorf("expected nil error, got %v", err)
			}
		})
	}
}

func TestActivityRequestURL(t *testing.T) {
	t.Parallel()

	t.Run("uses request URL when present", func(t *testing.T) {
		u, _ := url.Parse("https://acct.services.ai.azure.com/api/projects/proj/agents/a/endpoint/protocols/activity")
		resp := &http.Response{Request: &http.Request{URL: u}}
		if got := activityRequestURL(resp); got != u.String() {
			t.Errorf("activityRequestURL = %q, want %q", got, u.String())
		}
	})

	t.Run("falls back when no request URL", func(t *testing.T) {
		if got := activityRequestURL(&http.Response{}); got != "/activity" {
			t.Errorf("activityRequestURL = %q, want %q", got, "/activity")
		}
	})
}
