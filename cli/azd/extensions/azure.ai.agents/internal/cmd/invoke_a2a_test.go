// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"azureaiagent/internal/pkg/agents/agent_api"
)

// Version validation and builders.

func TestValidateInvokeA2AVersionFlags(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, version, file string
		protocol            agent_api.AgentProtocol
		wantError           string
	}{
		{"default plain", "", "", agent_api.AgentProtocolA2A, ""},
		{"explicit 0.3", "0.3", "", agent_api.AgentProtocolA2A, ""},
		{"explicit 1.0", "1.0", "", agent_api.AgentProtocolA2A, ""},
		{"file 0.3", "0.3", "request.json", agent_api.AgentProtocolA2A, ""},
		{"file 1.0", "1.0", "request.json", agent_api.AgentProtocolA2A, ""},
		{"file missing version", "", "request.json", agent_api.AgentProtocolA2A, "requires an explicit --a2a-version"},
		{"invalid version", "2.0", "", agent_api.AgentProtocolA2A, "supported versions are 0.3 and 1.0"},
		{"no normalization", " 1.0", "", agent_api.AgentProtocolA2A, "invalid --a2a-version"},
		{"responses", "1.0", "", agent_api.AgentProtocolResponses, "only supported with the a2a protocol"},
		{"invocations", "0.3", "", agent_api.AgentProtocolInvocations, "only supported with the a2a protocol"},
		{"other protocol without flag", "", "request.json", agent_api.AgentProtocolResponses, ""},
		{"unresolved explicit", "1.0", "", "", ""},
		{"unresolved file", "", "request.json", "", ""},
		{"unresolved invalid", "latest", "", "", "invalid --a2a-version"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateInvokeA2AVersionFlags(&invokeFlags{
				a2aVersion: tt.version, inputFile: tt.file,
			}, tt.protocol)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestInvokeA2AVersionCommandValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"missing file version", []string{"-p", "a2a", "-f", "missing.json"}, "requires an explicit --a2a-version"},
		{"invalid version", []string{"-p", "a2a", "--a2a-version", "2.0", "hello"}, "supported versions are 0.3 and 1.0"},
		{"empty version", []string{"-p", "a2a", "--a2a-version=", "hello"}, "invalid --a2a-version"},
		{"responses", []string{"-p", "responses", "--a2a-version", "1.0", "hello"}, "only supported with the a2a protocol"},
		{"invocations", []string{"-p", "invocations", "--a2a-version", "0.3", "hello"},
			"only supported with the a2a protocol"},
		{"endpoint file", []string{"--agent-endpoint",
			"https://acct.services.ai.azure.com/api/projects/project/agents/agent/endpoint/protocols/a2a",
			"-f", "missing.json"}, "requires an explicit --a2a-version"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			command := newInvokeCommand(&azdext.ExtensionContext{NoPrompt: true})
			command.SetOut(io.Discard)
			command.SetErr(io.Discard)
			command.SetArgs(tt.args)
			require.ErrorContains(t, command.Execute(), tt.want)
		})
	}
}

func TestA2AProtocolVersion(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ name, explicit, resolved, want string }{
		{"default", "", "", "1.0"},
		{"explicit", "0.3", "", "0.3"},
		{"resolved", "", "0.3", "0.3"},
		{"explicit takes precedence", "0.3", "1.0", "0.3"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &InvokeAction{flags: &invokeFlags{a2aVersion: tt.explicit}, resolvedA2AVersion: tt.resolved}
			require.Equal(t, tt.want, a.a2aProtocolVersion())
		})
	}
}

func TestBuildA2ARequestBody_ValidatesVersion(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, explicit, resolved, file, wantError string
	}{
		{"invalid explicit", "2.0", "", "", "invalid --a2a-version"},
		{"invalid resolved", "", "2.0", "", "invalid --a2a-version"},
		{"invalid explicit despite resolved", "2.0", "1.0", "", "invalid --a2a-version"},
		{"file missing explicit", "", "", "missing.json", "requires an explicit --a2a-version"},
		{"file with manifest version", "", "1.0", "missing.json", "requires an explicit --a2a-version"},
		{"file with legacy manifest version", "", "0.3", "missing.json", "requires an explicit --a2a-version"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &InvokeAction{
				flags:              &invokeFlags{message: "hello", inputFile: tt.file, a2aVersion: tt.explicit},
				resolvedA2AVersion: tt.resolved,
			}
			_, _, err := a.buildA2ARequestBody()
			require.ErrorContains(t, err, tt.wantError)
		})
	}
}

func TestA2ARequestBuilderRejectsUnknownVersion(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"", "latest", "1.1", "2.0", "1.0.0", " 1.0"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			build, err := a2aRequestBuilderForVersion(version)
			require.ErrorContains(t, err, "supported versions are 0.3 and 1.0")
			require.Nil(t, build)
			require.EqualError(t, validateA2AVersion(version), err.Error())
		})
	}
}

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
			map[string]any{"returnImmediately": false, "acceptedOutputModes": []any{"text/plain"}}},
		{"1.0", "1.0", "", "SendMessage", "ROLE_USER",
			map[string]any{"text": "Hello!"},
			map[string]any{"returnImmediately": false, "acceptedOutputModes": []any{"text/plain"}}},
		{"0.3", "0.3", "", "message/send", "user",
			map[string]any{"kind": "text", "text": "Hello!"},
			map[string]any{"blocking": true, "acceptedOutputModes": []any{"text/plain"}}},
		{"explicit wins", "0.3", "1.0", "message/send", "user",
			map[string]any{"kind": "text", "text": "Hello!"},
			map[string]any{"blocking": true, "acceptedOutputModes": []any{"text/plain"}}},
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

func TestBuildA2ARequestBody_OpaqueFile(t *testing.T) {
	t.Parallel()

	// A file is opaque: do not parse, validate, normalize, or convert its body.
	content := []byte(" \r\nnot JSON\x00\xff\n")
	path := filepath.Join(t.TempDir(), "opaque.bin")
	require.NoError(t, os.WriteFile(path, content, 0600))
	for _, version := range []string{"0.3", "1.0"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			a := &InvokeAction{flags: &invokeFlags{inputFile: path, a2aVersion: version}}
			body, _, err := a.buildA2ARequestBody()
			require.NoError(t, err)
			require.Equal(t, content, body)
		})
	}
}

func TestA2ATextRequestBuildersPreserveText(t *testing.T) {
	t.Parallel()
	const text = "A quote: \"hello\"\nA backslash: \\ and Unicode: \u4f60\u597d"
	for _, version := range []string{"0.3", "1.0"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			build, err := a2aRequestBuilderForVersion(version)
			require.NoError(t, err)
			body, err := json.Marshal(build(text, "request-id", "message-id"))
			require.NoError(t, err)
			var request struct {
				ID     string `json:"id"`
				Params struct {
					Message struct {
						MessageID string `json:"messageId"`
						Parts     []struct {
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"message"`
				} `json:"params"`
			}
			require.NoError(t, json.Unmarshal(body, &request))
			require.Equal(t, "request-id", request.ID)
			require.Equal(t, "message-id", request.Params.Message.MessageID)
			require.Len(t, request.Params.Message.Parts, 1)
			require.Equal(t, text, request.Params.Message.Parts[0].Text)
		})
	}
}

// Wire and docs fixtures.

func TestA2ATextRequestBuilders(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		version string
		build   a2aTextRequestBuilder
	}{
		{"0.3", buildA2A03TextRequest},
		{"1.0", buildA2A10TextRequest},
	} {
		t.Run(tt.version, func(t *testing.T) {
			t.Parallel()
			request := tt.build("Hello!", "hello-1", "hello-message-1")
			body, err := json.Marshal(request)
			require.NoError(t, err)
			golden, err := os.ReadFile(filepath.Join("testdata", "a2a", "request-"+tt.version+".json"))
			require.NoError(t, err)
			require.JSONEq(t, string(golden), string(body))

			// The selector must select this exact dialect, not a default body
			// subsequently patched by version-dependent branches.
			selected, err := a2aRequestBuilderForVersion(tt.version)
			require.NoError(t, err)
			selectedBody, err := json.Marshal(selected("Hello!", "hello-1", "hello-message-1"))
			require.NoError(t, err)
			require.JSONEq(t, string(golden), string(selectedBody))
		})
	}
}

func TestA2AReadmeRequestsMatchWireFixtures(t *testing.T) {
	t.Parallel()
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	require.NoError(t, err)
	lfReadme := strings.ReplaceAll(string(readme), "\r\n", "\n")
	for _, tt := range []struct{ name, text string }{
		{"LF", lfReadme},
		{"CRLF", strings.ReplaceAll(lfReadme, "\n", "\r\n")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Windows checkouts may use CRLF; markdown and JSON semantics do not depend on it.
			text := strings.ReplaceAll(tt.text, "\r\n", "\n")
			_, section, found := strings.Cut(text, "## Invoking agents over A2A\n")
			require.True(t, found)
			section, _, _ = strings.Cut(section, "\n## ")
			examples := regexp.MustCompile("(?s)```json\\n(.*?)\\n```").FindAllStringSubmatch(section, -1)
			seen := map[string]bool{}
			for _, example := range examples {
				var request struct {
					Method string `json:"method"`
				}
				require.NoError(t, json.Unmarshal([]byte(example[1]), &request))
				var version string
				switch request.Method {
				case "message/send":
					version = "0.3"
				case "SendMessage":
					version = "1.0"
				default:
					continue
				}
				golden, err := os.ReadFile(filepath.Join("testdata", "a2a", "request-"+version+".json"))
				require.NoError(t, err)
				require.JSONEq(t, string(golden), example[1], "README A2A %s request must remain usable", version)
				seen[version] = true
			}
			require.Equal(t, map[string]bool{"0.3": true, "1.0": true}, seen)
		})
	}
}

func TestA2ARemoteSelectedDialect(t *testing.T) {
	for _, version := range []string{"", "0.3", "1.0"} {
		t.Run("version="+version, func(t *testing.T) {
			wireVersion, method, role := "1.0", "SendMessage", "ROLE_USER"
			if version == "0.3" {
				wireVersion, method, role = "0.3", "message/send", "user"
			}
			var sends int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sends++
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/agents/agent/endpoint/protocols/a2a", r.URL.Path)
				assert.Equal(t, wireVersion, r.Header.Get("A2A-Version"))
				assert.NotEmpty(t, r.Header.Get("Authorization"))
				var request struct {
					Method string `json:"method"`
					Params struct {
						Message struct {
							Role string `json:"role"`
						} `json:"message"`
						Configuration struct {
							AcceptedOutputModes []string `json:"acceptedOutputModes"`
						} `json:"configuration"`
					} `json:"params"`
				}
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				assert.Equal(t, method, request.Method)
				assert.Equal(t, role, request.Params.Message.Role)
				w.Header().Set("Content-Type", "application/json")
				// A normal text-only agent offers text/plain, not the part kind
				// "text". Enforce negotiation instead of accepting any client value.
				if !slices.Contains(request.Params.Configuration.AcceptedOutputModes, "text/plain") {
					_, _ = io.WriteString(w, `{"jsonrpc":"2.0","error":{"code":-32602,`+
						`"message":"No acceptable output media type; this agent offers text/plain"}}`)
					return
				}
				if wireVersion == "1.0" {
					_, _ = io.WriteString(w, `{"jsonrpc":"2.0","result":{"message":{"parts":[{"text":"dialect-ok"}]}}}`)
				} else {
					_, _ = io.WriteString(w, `{"jsonrpc":"2.0","result":{"parts":[{"kind":"text","text":"dialect-ok"}]}}`)
				}
			}))
			defer server.Close()
			action := &InvokeAction{
				flags:      &invokeFlags{message: "hello", a2aVersion: version},
				credential: responseTestCredential{},
				resolvedRemoteContext: &remoteContext{
					name: "agent", projectEndpoint: server.URL, apiVersion: "v1",
				},
			}
			output, err := captureStdout(t, func() error { return action.a2aRemote(t.Context()) })
			require.NoError(t, err)
			assert.Equal(t, 1, sends)
			assert.Contains(t, output, "[agent] dialect-ok")
		})
	}
}

// HTTP headers.

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

// Responses, errors, and local guard.

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

func TestExtractA2AText_Versions(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ name, version, body, want string }{
		{"1.0 message", "1.0", `{"result":{"message":{"role":"ROLE_AGENT","parts":[{"text":"hello"}]}}}`, "hello"},
		{"1.0 task status", "1.0",
			`{"result":{"task":{"status":{"message":{"parts":[{"text":"done"}]}}}}}`, "done"},
		{"1.0 task artifacts", "1.0",
			`{"result":{"task":{"artifacts":[{"parts":[{"text":"one"},{"text":"two"}]}]}}}`, "one\ntwo"},
		{"1.0 GetTask direct result", "1.0",
			`{"result":{"id":"task-1","contextId":"context-1","status":{"state":"TASK_STATE_COMPLETED"},` +
				`"artifacts":[{"parts":[{"text":"lookup answer"}]}]}}`, "lookup answer"},
		{"1.0 CancelTask direct result", "1.0",
			`{"result":{"id":"task-1","contextId":"context-1","status":{"state":"TASK_STATE_CANCELED",` +
				`"message":{"parts":[{"text":"Canceled"}]}}}}`, "Canceled"},
		{"1.0 task history skips user", "1.0",
			`{"result":{"task":{"history":[{"role":"ROLE_AGENT","parts":[{"text":"answer"}]},` +
				`{"role":"ROLE_USER","parts":[{"text":"question"}]}]}}}`, "answer"},
		{"0.3 history skips both user spellings", "0.3",
			`{"result":{"history":[{"role":"agent","parts":[{"text":"answer"}]},` +
				`{"role":"user","parts":[{"text":"q1"}]},{"role":"ROLE_USER","parts":[{"text":"q2"}]}]}}`, "answer"},
		{"1.0 user only history", "1.0",
			`{"result":{"task":{"history":[{"role":"ROLE_USER","parts":[{"text":"question"}]}]}}}`, ""},
		{"1.0 rejects unwrapped message", "1.0", `{"result":{"parts":[{"text":"legacy"}]}}`, ""},
		{"0.3 does not unwrap 1.0", "0.3", `{"result":{"message":{"parts":[{"text":"new"}]}}}`, ""},
		{"1.0 malformed wrapper", "1.0", `{"result":{"task":"not a task"}}`, ""},
		{"1.0 null result", "1.0", `{"result":null}`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, extractA2AText([]byte(tt.body), tt.version))
		})
	}
}

func TestExtractA2ATextPrefersTaskArtifacts(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"0.3", "1.0"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			// A native SDK task can finish with a generic lifecycle message while
			// its actual answer is carried in artifacts. Both dialects support this.
			task := map[string]any{
				"status": map[string]any{
					"message": map[string]any{"parts": []any{map[string]any{"text": "Done"}}},
				},
				"artifacts": []any{
					map[string]any{"parts": []any{map[string]any{"text": "native answer"}}},
				},
			}
			result := task
			if version == "1.0" {
				result = map[string]any{"task": task}
			}
			body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "result": result})
			require.NoError(t, err)
			require.Equal(t, "native answer", extractA2AText(body, version))
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

func TestHandleA2AResponse_HTTP200JSONRPCError(t *testing.T) {
	t.Parallel()

	for _, version := range []string{"0.3", "1.0"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(
					`{"jsonrpc":"2.0","error":{"code":-32601,"message":"method not found"}}`,
				)),
			}
			err := handleA2AResponse(resp, "agent", false, version)
			require.ErrorContains(t, err, "agent error (-32601): method not found")
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

// TestA2ALocal_NotSupported verifies A2A fails fast for local invocation.
func TestA2ALocal_NotSupported(t *testing.T) {
	t.Parallel()
	a := &InvokeAction{flags: &invokeFlags{local: true, message: "hi"}}
	if err := a.a2aLocal(t.Context()); err == nil {
		t.Fatal("expected an error for local a2a invocation, got nil")
	}
}
