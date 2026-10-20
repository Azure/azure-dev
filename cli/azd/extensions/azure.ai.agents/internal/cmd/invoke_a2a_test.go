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

	"github.com/stretchr/testify/require"

	"azureaiagent/internal/pkg/agents/agent_api"
)

// TestBuildA2ARequestBody_WrapsMessage verifies dialect-specific wire shapes,
// including the configuration that requests synchronous text output.
func TestBuildA2ARequestBody_WrapsMessage(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, explicit, resolved, method, role string
		part                                   map[string]any
		configuration                          map[string]any
	}{
		{"default", "", "", "SendMessage", "ROLE_USER",
			map[string]any{"text": "Hello!"},
			map[string]any{"returnImmediately": false, "acceptedOutputModes": []any{"text"}}},
		{"1.0", "1.0", "", "SendMessage", "ROLE_USER",
			map[string]any{"text": "Hello!"},
			map[string]any{"returnImmediately": false, "acceptedOutputModes": []any{"text"}}},
		{"0.3", "0.3", "", "message/send", "user",
			map[string]any{"kind": "text", "text": "Hello!"},
			map[string]any{"blocking": true, "acceptedOutputModes": []any{"text"}}},
		{"explicit wins", "0.3", "1.0", "message/send", "user",
			map[string]any{"kind": "text", "text": "Hello!"},
			map[string]any{"blocking": true, "acceptedOutputModes": []any{"text"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &InvokeAction{
				flags:              &invokeFlags{message: "Hello!", a2aVersion: tt.explicit},
				resolvedA2AVersion: tt.resolved,
			}
			body, label, err := a.buildA2ARequestBody()
			require.NoError(t, err)
			require.Equal(t, `"Hello!"`, label)
			var req struct {
				JSONRPC string `json:"jsonrpc"`
				ID      string `json:"id"`
				Method  string `json:"method"`
				Params  struct {
					Message struct {
						Kind      string           `json:"kind"`
						Role      string           `json:"role"`
						MessageID string           `json:"messageId"`
						Parts     []map[string]any `json:"parts"`
					} `json:"message"`
					Configuration map[string]any `json:"configuration"`
				} `json:"params"`
			}
			require.NoError(t, json.Unmarshal(body, &req))
			require.Equal(t, "2.0", req.JSONRPC)
			require.NotEmpty(t, req.ID)
			require.Equal(t, tt.method, req.Method)
			require.Equal(t, tt.role, req.Params.Message.Role)
			if tt.role == "user" {
				require.Equal(t, "message", req.Params.Message.Kind, "0.3 requires the message discriminator")
			} else {
				require.Empty(t, req.Params.Message.Kind)
			}
			require.NotEmpty(t, req.Params.Message.MessageID)
			require.Equal(t, []map[string]any{tt.part}, req.Params.Message.Parts)
			require.Equal(t, tt.configuration, req.Params.Configuration)
		})
	}
}

// TestBuildA2ARequestBody_PassesFileVerbatim verifies --input-file contents are
// forwarded unchanged (assumed to be a full JSON-RPC request).
func TestBuildA2ARequestBody_PassesFileVerbatim(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a2a.json")
	content := " \r\n" +
		`{"jsonrpc":"2.0","id":"x","method":"message/send","params":{"message":{"role":"user","parts":[]}}}` + "\n\t"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	for _, version := range []string{"0.3", "1.0"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			a := &InvokeAction{flags: &invokeFlags{inputFile: path, a2aVersion: version}}
			body, label, err := a.buildA2ARequestBody()
			require.NoError(t, err)
			require.Equal(t, content, string(body))
			require.NotEmpty(t, label)
		})
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
			if got := extractA2AText([]byte(tt.body), "0.3"); got != tt.want {
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

// TestApplyA2ARequestHeaders verifies the explicit A2A-Version header (so the
// server negotiates deterministically), the bearer token, and the user identity
// header are set on the request.
func TestApplyA2ARequestHeaders(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"0.3", "1.0"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.com/a2a", nil)
			require.NoError(t, err)
			applyA2ARequestHeaders(req, "test-token", &userIdentityFlags{userIdentity: "user-1"}, version)
			require.Equal(t, version, req.Header.Get(a2aVersionHeader))
			require.Equal(t, "Bearer test-token", req.Header.Get("Authorization"))
			require.Equal(t, "user-1", req.Header.Get(agent_api.UserIdentityHeader))
		})
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
