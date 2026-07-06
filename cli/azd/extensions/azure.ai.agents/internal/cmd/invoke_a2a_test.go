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

// TestBuildA2ARequestBody_WrapsMessage verifies a plain message is wrapped into
// a JSON-RPC 2.0 message/send request with a single user text part.
func TestBuildA2ARequestBody_WrapsMessage(t *testing.T) {
	t.Parallel()

	a := &InvokeAction{flags: &invokeFlags{message: "Hello!"}}
	body, label, err := a.buildA2ARequestBody()
	if err != nil {
		t.Fatalf("buildA2ARequestBody: %v", err)
	}
	if label != `"Hello!"` {
		t.Errorf("label = %q, want %q", label, `"Hello!"`)
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, string(body))
	}
	if req["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v, want 2.0", req["jsonrpc"])
	}
	if req["method"] != "message/send" {
		t.Errorf("method = %v, want message/send", req["method"])
	}
	if req["id"] == nil || req["id"] == "" {
		t.Error("request id must be set")
	}
	params, _ := req["params"].(map[string]any)
	msg, _ := params["message"].(map[string]any)
	if msg["role"] != "user" {
		t.Errorf("message.role = %v, want user", msg["role"])
	}
	if msg["messageId"] == nil || msg["messageId"] == "" {
		t.Error("message.messageId must be set")
	}
	parts, _ := msg["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("want 1 part, got %d", len(parts))
	}
	p0, _ := parts[0].(map[string]any)
	if p0["kind"] != "text" || p0["text"] != "Hello!" {
		t.Errorf("part = %+v, want {kind:text, text:Hello!}", p0)
	}
}

// TestBuildA2ARequestBody_PassesFileVerbatim verifies --input-file contents are
// forwarded unchanged (assumed to be a full JSON-RPC request).
func TestBuildA2ARequestBody_PassesFileVerbatim(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a2a.json")
	content := `{"jsonrpc":"2.0","id":"x","method":"message/send","params":{"message":{"role":"user","parts":[]}}}`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	a := &InvokeAction{flags: &invokeFlags{inputFile: path}}
	body, label, err := a.buildA2ARequestBody()
	if err != nil {
		t.Fatalf("buildA2ARequestBody: %v", err)
	}
	if string(body) != content {
		t.Errorf("body = %q, want verbatim %q", string(body), content)
	}
	if label == "" {
		t.Error("expected a non-empty body label for file input")
	}
}

func TestExtractA2AText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "message result parts",
			body: `{"jsonrpc":"2.0","id":"1","result":{"role":"agent","parts":[{"kind":"text","text":"hi there"}]}}`,
			want: "hi there",
		},
		{
			name: "task status message parts",
			body: `{"result":{"status":{"message":{"parts":[{"kind":"text","text":"working"}]}}}}`,
			want: "working",
		},
		{
			name: "task artifacts parts",
			body: `{"result":{"artifacts":[{"parts":[{"kind":"text","text":"one"}]},{"parts":[{"kind":"text","text":"two"}]}]}}`,
			want: "one\ntwo",
		},
		{
			name: "task history last agent message",
			body: `{"result":{"history":[{"role":"user","parts":[{"kind":"text","text":"q"}]},` +
				`{"role":"agent","parts":[{"kind":"text","text":"a"}]}]}}`,
			want: "a",
		},
		{
			name: "type instead of kind",
			body: `{"result":{"parts":[{"type":"text","text":"legacy"}]}}`,
			want: "legacy",
		},
		{
			name: "non-text part ignored",
			body: `{"result":{"parts":[{"kind":"file","file":{}},{"kind":"text","text":"only"}]}}`,
			want: "only",
		},
		{
			name: "no result",
			body: `{"jsonrpc":"2.0","id":"1"}`,
			want: "",
		},
		{
			name: "invalid json",
			body: `not json`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := extractA2AText([]byte(tt.body)); got != tt.want {
				t.Errorf("extractA2AText(%s) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

func TestA2AErrorFromBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		body      string
		wantErr   bool
		wantInMsg string
	}{
		{name: "error with code", body: `{"jsonrpc":"2.0","id":"1","error":{"code":-32601,"message":"method not found"}}`,
			wantErr: true, wantInMsg: "agent error (-32601): method not found"},
		{name: "error without code", body: `{"error":{"message":"boom"}}`, wantErr: true, wantInMsg: "agent error: boom"},
		{name: "no error field", body: `{"result":{"parts":[]}}`, wantErr: false},
		{name: "invalid json", body: `not json`, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := a2aErrorFromBody([]byte(tt.body))
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

// TestA2ALocal_NotSupported verifies A2A fails fast for local invocation.
func TestA2ALocal_NotSupported(t *testing.T) {
	t.Parallel()
	a := &InvokeAction{flags: &invokeFlags{local: true, message: "hi"}}
	if err := a.a2aLocal(t.Context()); err == nil {
		t.Fatal("expected an error for local a2a invocation, got nil")
	}
}

func TestA2ARequestURL(t *testing.T) {
	t.Parallel()

	t.Run("uses request URL when present", func(t *testing.T) {
		u, _ := url.Parse("https://acct.services.ai.azure.com/api/projects/proj/agents/a/endpoint/protocols/a2a")
		resp := &http.Response{Request: &http.Request{URL: u}}
		if got := a2aRequestURL(resp); got != u.String() {
			t.Errorf("a2aRequestURL = %q, want %q", got, u.String())
		}
	})

	t.Run("falls back when no request URL", func(t *testing.T) {
		if got := a2aRequestURL(&http.Response{}); got != "/a2a" {
			t.Errorf("a2aRequestURL = %q, want %q", got, "/a2a")
		}
	})
}
