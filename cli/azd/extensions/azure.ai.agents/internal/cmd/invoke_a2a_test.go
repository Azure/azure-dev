// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/featureflags"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
)

// Version validation and builders.

func TestA2APreviewGateAndLocalValidation(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-p", "a2a", "--a2a-version", "1.0", "hi"}, `preview feature "agents.a2a" is disabled`},
		{[]string{"-p", "a2a", "--a2a-version", "0.3", "-f", "missing.json"}, `preview feature "agents.a2a" is disabled`},
		{[]string{"--a2a-version", "1.0", "hi"}, `preview feature "agents.a2a" is disabled`},
		{[]string{"-p", "a2a", "--a2a-version=", "hi"}, "invalid --a2a-version"},
		{[]string{"-p", "a2a", "--a2a-version", "2.0", "hi"}, "supported versions are 0.3 and 1.0"},
		{[]string{"-p", "responses", "--a2a-version", "1.0", "hi"}, "only supported with the a2a protocol"},
		{[]string{"-p", "a2a", "--a2a-version", "1.0", "--local", "hi"}, "cannot be invoked against a local agent"},
		{[]string{"--a2a-version", "1.0", "--no-wait", "hi"}, "--no-wait requires --long-running"},
		{[]string{"--a2a-version", "1.0", "--client-header", "bad", "hi"}, "invalid --client-header"},
		{[]string{"--a2a-version", "1.0", "agent", "hi", "-f", "missing.json"}, "cannot use --input-file and a message"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			clearPreviewProcess(t)
			t.Chdir(t.TempDir())
			_, env := startPreviewCommandServer(t, nil, nil, nil)
			_, err := executeRoot(t, append([]string{"invoke"}, tc.args...)...)
			require.ErrorContains(t, err, tc.want)
			require.Zero(t, env.reads.Load())
			entries, err := os.ReadDir(".")
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestA2APreviewFilePolicy(t *testing.T) {
	for _, preview := range []bool{false, true} {
		for _, protocol := range []agent_api.AgentProtocol{"", agent_api.AgentProtocolA2A, agent_api.AgentProtocolResponses} {
			flags := &invokeFlags{inputFile: "missing.json"}
			err := validateInvokeA2AVersionFlags(flags, protocol, preview)
			if preview && protocol == agent_api.AgentProtocolA2A {
				require.ErrorContains(t, err, "requires an explicit --a2a-version")
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, flags.a2aVersion)
		}
	}
}

func TestValidateInvokeArgsUsesPreviewDecision(t *testing.T) {
	for _, value := range []string{"false", "true", "invalid"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(featureflags.A2A.EnvVar, value)
			command := newInvokeCommand(nil)
			require.NoError(t, command.ParseFlags([]string{"--protocol", "a2a", "--input-file", "missing.json"}))
			flags := invokeFlags{protocol: "a2a", inputFile: "missing.json"}
			before := flags
			for range 2 {
				require.NoError(t, validateInvokeArgs(command, &flags, false))
				require.ErrorContains(t, validateInvokeArgs(command, &flags, true), "requires an explicit --a2a-version")
				require.Equal(t, before, flags)
				require.Empty(t, flags.a2aVersion, "validation must not resolve a version")
				value, err := command.Flags().GetString("input-file")
				require.NoError(t, err)
				require.Equal(t, "missing.json", value)
				require.True(t, command.Flags().Changed("input-file"))
				require.False(t, command.Flags().Changed("a2a-version"))
			}
		})
	}
}

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
			}, tt.protocol, true)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestInvokeA2AVersionCommandValidation(t *testing.T) {
	t.Setenv("AZD_ALPHA_ENABLE_AGENTS_A2A", "true")
	for _, tt := range []struct {
		name     string
		args     []string
		want     string
		wantCode string
	}{
		{"missing file version", []string{"-p", "a2a", "-f", "missing.json"},
			"requires an explicit --a2a-version", exterrors.CodeInvalidParameter},
		{"invalid version", []string{"-p", "a2a", "--a2a-version", "2.0", "hello"},
			"supported versions are 0.3 and 1.0", exterrors.CodeInvalidParameter},
		{"empty version", []string{"-p", "a2a", "--a2a-version=", "hello"},
			"invalid --a2a-version", exterrors.CodeInvalidParameter},
		{"responses", []string{"-p", "responses", "--a2a-version", "1.0", "hello"},
			"only supported with the a2a protocol", exterrors.CodeConflictingArguments},
		{"invocations", []string{"-p", "invocations", "--a2a-version", "0.3", "hello"},
			"only supported with the a2a protocol", exterrors.CodeConflictingArguments},
		{"endpoint file", []string{"--agent-endpoint",
			"https://acct.services.ai.azure.com/api/projects/project/agents/agent/endpoint/protocols/a2a",
			"-f", "missing.json"}, "requires an explicit --a2a-version", exterrors.CodeInvalidParameter},
	} {
		t.Run(tt.name, func(t *testing.T) {
			command := newInvokeCommand(&azdext.ExtensionContext{NoPrompt: true})
			command.SetOut(io.Discard)
			command.SetErr(io.Discard)
			command.SetArgs(tt.args)
			err := command.Execute()
			require.ErrorContains(t, err, tt.want)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "expected a structured local error")
			require.Equal(t, tt.wantCode, localErr.Code)
		})
	}
}

func TestA2AProtocolVersion(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ name, explicit, want string }{
		{"default", "", "1.0"},
		{"explicit 0.3", "0.3", "0.3"},
		{"explicit 1.0", "1.0", "1.0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, resolveA2AVersion(tt.explicit, true))
			wantOff := tt.explicit
			if wantOff == "" {
				wantOff = "0.3"
			}
			require.Equal(t, wantOff, resolveA2AVersion(tt.explicit, false))
		})
	}
}

func TestBuildA2ARequestBody_ValidatesVersion(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, explicit, file, wantError string
	}{
		{"invalid explicit", "2.0", "", "invalid --a2a-version"},
		{"empty unresolved version", "", "missing.json", "invalid --a2a-version"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &InvokeAction{
				flags: &invokeFlags{message: "hello", inputFile: tt.file, a2aVersion: tt.explicit},
			}
			_, _, err := a.buildA2ARequestBody(tt.explicit)
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
// preserving 0.3's unspecified defaults and 1.0's synchronous text configuration.
func TestBuildA2ARequestBody_WrapsMessage(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, explicit, method, role string
		part                         map[string]any
		configuration                map[string]any
	}{
		{"default", "", "SendMessage", "ROLE_USER",
			map[string]any{"text": "Hello!"},
			map[string]any{"returnImmediately": false, "acceptedOutputModes": []any{"text/plain"}}},
		{"1.0", "1.0", "SendMessage", "ROLE_USER",
			map[string]any{"text": "Hello!"},
			map[string]any{"returnImmediately": false, "acceptedOutputModes": []any{"text/plain"}}},
		{"0.3", "0.3", "message/send", "user",
			map[string]any{"kind": "text", "text": "Hello!"}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &InvokeAction{
				flags: &invokeFlags{message: "Hello!", a2aVersion: tt.explicit},
			}
			body, label, err := a.buildA2ARequestBody(resolveA2AVersion(tt.explicit, true))
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
			body, label, err := a.buildA2ARequestBody(version)
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
			body, _, err := a.buildA2ARequestBody(version)
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
	for _, tc := range []struct{ version, preview string }{
		{"", "false"}, {"", "true"}, {"0.3", "true"}, {"1.0", "true"},
	} {
		version := tc.version
		t.Run("version="+version+"/preview="+tc.preview, func(t *testing.T) {
			t.Setenv(featureflags.A2A.EnvVar, tc.preview)
			wireVersion, method, role := "1.0", "SendMessage", "ROLE_USER"
			if version == "0.3" || tc.preview == "false" {
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
						Configuration *struct {
							AcceptedOutputModes []string `json:"acceptedOutputModes"`
						} `json:"configuration"`
					} `json:"params"`
				}
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				assert.Equal(t, method, request.Method)
				assert.Equal(t, role, request.Params.Message.Role)
				w.Header().Set("Content-Type", "application/json")
				if wireVersion == "0.3" {
					assert.Nil(t, request.Params.Configuration, "preserve unspecified 0.3 execution/output modes")
				} else {
					// Explicit 1.0 negotiation uses media types, not the part kind "text".
					if !assert.NotNil(t, request.Params.Configuration) {
						return
					}
					if !slices.Contains(request.Params.Configuration.AcceptedOutputModes, "text/plain") {
						_, _ = io.WriteString(w, `{"jsonrpc":"2.0","error":{"code":-32602,`+
							`"message":"No acceptable output media type; this agent offers text/plain"}}`)
						return
					}
				}
				if wireVersion == "1.0" {
					_, _ = io.WriteString(w, `{"jsonrpc":"2.0","result":{"message":{"parts":[{"text":"dialect-ok"}]}}}`)
				} else {
					_, _ = io.WriteString(w, `{"jsonrpc":"2.0","result":{"parts":[{"kind":"text","text":"dialect-ok"}]}}`)
				}
			}))
			defer server.Close()
			action := &InvokeAction{
				flags:      &invokeFlags{message: "hello", protocol: "a2a", a2aVersion: version},
				credential: responseTestCredential{},
				resolvedRemoteContext: &remoteContext{
					name: "agent", projectEndpoint: server.URL, apiVersion: "v1",
				},
			}
			output, err := captureStdout(t, func() error { return action.Run(t.Context()) })
			require.NoError(t, err)
			assert.Equal(t, 1, sends)
			assert.Contains(t, output, "[agent] dialect-ok")
		})
	}
}

func TestA2A03LegacyOneShotRequests(t *testing.T) {
	for _, preview := range []string{"false", "true"} {
		for _, tc := range []struct{ name, response, want string }{
			{"pending task", `{"jsonrpc":"2.0","result":{"kind":"task","id":"pending-task",` +
				`"status":{"state":"working","message":{"parts":[{"kind":"text","text":"Still working"}]}}}}`,
				"Still working"},
			{"non-text output", `{"jsonrpc":"2.0","result":{"kind":"task","id":"data-task",` +
				`"status":{"state":"completed"},"artifacts":[{"parts":[{"kind":"data","data":{"answer":42}}]}]}}`,
				`"answer": 42`},
		} {
			t.Run(preview+"/"+tc.name, func(t *testing.T) {
				t.Setenv(featureflags.A2A.EnvVar, preview)
				var sends int
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					sends++
					assert.Equal(t, http.MethodPost, r.Method)
					assert.Equal(t, "0.3", r.Header.Get(a2aVersionHeader))
					var request struct {
						Method string         `json:"method"`
						Params map[string]any `json:"params"`
					}
					if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&request)) {
						return
					}
					assert.Equal(t, "message/send", request.Method)
					// Neither a long-running server nor a non-text server should
					// receive newly imposed execution/output preferences.
					if _, present := request.Params["configuration"]; present {
						http.Error(w, "unexpected blocking or output-mode configuration", http.StatusBadRequest)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, tc.response)
				}))
				defer server.Close()
				explicit := ""
				if preview == "true" {
					explicit = "0.3"
				}
				action := &InvokeAction{
					flags:      &invokeFlags{message: "hello", protocol: "a2a", a2aVersion: explicit},
					credential: responseTestCredential{},
					resolvedRemoteContext: &remoteContext{
						name: "agent", projectEndpoint: server.URL, apiVersion: "v1",
					},
				}
				output, err := captureStdout(t, func() error { return action.Run(t.Context()) })
				require.NoError(t, err)
				require.Equal(t, 1, sends, "one-shot invocation must not poll or resubmit")
				require.Contains(t, output, tc.want)
			})
		}
	}
}

func TestA2AFileWirePolicy(t *testing.T) {
	for _, tc := range []struct{ preview, explicit, wire string }{
		{"false", "", "0.3"}, {"true", "0.3", "0.3"}, {"true", "1.0", "1.0"},
	} {
		t.Run(tc.preview+"/"+tc.explicit, func(t *testing.T) {
			t.Setenv(featureflags.A2A.EnvVar, tc.preview)
			content := []byte(" \r\nopaque\x00\xff\n")
			path := filepath.Join(t.TempDir(), "request.bin")
			require.NoError(t, os.WriteFile(path, content, 0600))
			var sends int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sends++
				assert.Equal(t, tc.wire, r.Header.Get(a2aVersionHeader))
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.Equal(t, content, body)
				_, _ = io.WriteString(w, `{"jsonrpc":"2.0","result":{"parts":[{"text":"ok"}]}}`)
			}))
			defer server.Close()
			action := &InvokeAction{
				flags:      &invokeFlags{protocol: "a2a", inputFile: path, a2aVersion: tc.explicit},
				credential: responseTestCredential{},
				resolvedRemoteContext: &remoteContext{
					name: "agent", projectEndpoint: server.URL, apiVersion: "v1",
				},
			}
			_, err := captureStdout(t, func() error { return action.Run(t.Context()) })
			require.NoError(t, err)
			require.Equal(t, 1, sends)
		})
	}
}

func TestA2AHelpVisibility(t *testing.T) {
	for _, initial := range []string{"false", "true"} {
		for _, value := range []string{"false", "true"} {
			t.Run(initial+"/"+value, func(t *testing.T) {
				clearPreviewProcess(t)
				t.Setenv(featureflags.A2A.EnvVar, initial)
				root := NewRootCommand()
				command, _, err := root.Find([]string{"invoke"})
				require.NoError(t, err)
				require.Contains(t, command.Long, invokeA2AVersionHelp)
				require.Contains(t, command.Example, invokeA2APreviewExamples, "static metadata must be complete")
				require.NotContains(t, command.Example, invokeA2ALegacyExamples)
				require.Contains(t, command.Example, "Use the A2A 1.0 convenience default for a plain message")
				require.NotContains(t, command.Example, "a2a-request.json")
				require.False(t, command.Flags().Lookup("a2a-version").Hidden)

				// Help must use fresh process states, not construction-time states.
				t.Setenv(featureflags.A2A.EnvVar, value)
				var output strings.Builder
				root.SetOut(&output)
				root.SetErr(&output)
				root.SetArgs([]string{"invoke", "--help"})
				require.NoError(t, root.ExecuteContext(t.Context()))
				text := output.String()
				require.Contains(t, text, "JSON-RPC request with the a2a protocol")
				if value == "true" {
					require.Contains(t, text, `invoke -p a2a "Hello!"`)
					require.Contains(t, text, "--a2a-version")
					require.Contains(t, text, "1.0 is a convenience default")
					require.Contains(t, text, "Use the A2A 1.0 convenience default for a plain message")
					require.Contains(t, text, "a2a-request-1.0.json")
					require.NotContains(t, text, "Invoke a deployed agent over the a2a protocol")
					require.NotContains(t, text, "a2a-request.json")
				} else {
					require.NotContains(t, text, "--a2a-version")
					require.NotContains(t, text, "1.0")
					require.Contains(t, text, "Invoke a deployed agent over the a2a protocol")
					require.Contains(t, text, `invoke --protocol a2a "Hello!"`)
					require.Contains(t, text, "invoke --protocol a2a -f a2a-request.json")
				}
			})
		}
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

// Responses and errors.

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
			// A completed task can have generic status text alongside its answer.
			state := "completed"
			if version == "1.0" {
				state = "TASK_STATE_COMPLETED"
			}
			task := map[string]any{
				"status": map[string]any{
					"state":   state,
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

func TestExtractA2ATextPreservesNonCompletedStatus(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"0.3", "1.0"} {
		for _, state := range []string{
			"input-required", "auth-required", "failed", "canceled", "rejected", "submitted", "working", "",
		} {
			t.Run(version+"/"+state, func(t *testing.T) {
				t.Parallel()
				wireState := state
				if version == "1.0" && state != "" {
					wireState = "TASK_STATE_" + strings.ToUpper(strings.ReplaceAll(state, "-", "_"))
				}
				for _, statusText := range []string{"Follow-up or failure details", ""} {
					task := map[string]any{
						"status": map[string]any{
							"state":   wireState,
							"message": map[string]any{"parts": []any{map[string]any{"text": statusText}}},
						},
						"artifacts": []any{map[string]any{
							"parts": []any{map[string]any{"text": "Partial artifact"}},
						}},
					}
					result := task
					if version == "1.0" {
						result = map[string]any{"task": task}
					}
					body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "result": result})
					require.NoError(t, err)
					want := statusText
					if want == "" {
						want = "Partial artifact"
					}
					require.Equal(t, want, extractA2AText(body, version))
				}
			})
		}
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

// A2A hosted capabilities and target validation.

func TestValidateHostedA2ACompatibility(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		versions  []string
		wantError string
	}{
		{name: "no A2A declaration defers to Foundry"},
		{name: "major minor", versions: []string{"1.0"}},
		{name: "deployment version", versions: []string{"1.0.0"}},
		{name: "compatible patch", versions: []string{"1.0.7"}},
		{name: "duplicate normalized", versions: []string{"1.0", "1.0.0", "1.0.7"}},
		{name: "conflicting", versions: []string{"1.0.0", "0.3.0"}, wantError: "conflicting A2A versions"},
		{name: "conflicting unsupported", versions: []string{"0.2", "2.0"}, wantError: "conflicting A2A versions"},
		{name: "legacy", versions: []string{"0.3.0"}, wantError: "version 0.3 is not supported"},
		{name: "old", versions: []string{"0.2"}, wantError: "version 0.2 is not supported"},
		{name: "future minor", versions: []string{"1.1.0"}, wantError: "version 1.1 is not supported"},
		{name: "future major", versions: []string{"2.0.0"}, wantError: "version 2.0 is not supported"},
		{name: "empty", versions: []string{""}, wantError: "malformed A2A version"},
		{name: "malformed", versions: []string{"latest"}, wantError: "malformed A2A version"},
		{name: "negative patch", versions: []string{"1.0.-1"}, wantError: "malformed A2A version"},
		{name: "extra component", versions: []string{"1.0.0.0"}, wantError: "malformed A2A version"},
		{name: "leading zero", versions: []string{"01.0"}, wantError: "malformed A2A version"},
		{name: "signed", versions: []string{"+1.0"}, wantError: "malformed A2A version"},
		{name: "whitespace", versions: []string{" 1.0"}, wantError: "malformed A2A version"},
		{name: "valid then malformed", versions: []string{"1.0", "latest"}, wantError: "malformed A2A version"},
		{name: "malformed then valid", versions: []string{"latest", "1.0"}, wantError: "malformed A2A version"},
	} {
		for _, responses := range []bool{false, true} {
			for _, requested := range []string{"1.0", "0.3"} {
				t.Run(fmt.Sprintf("%s/responses=%t/requested=%s", tt.name, responses, requested), func(t *testing.T) {
					t.Parallel()
					protocols := []agent_yaml.ProtocolVersionRecord{{Protocol: "other", Version: "ignored"}}
					for _, version := range tt.versions {
						protocols = append(protocols, agent_yaml.ProtocolVersionRecord{Protocol: "a2a", Version: version})
					}
					if responses {
						protocols = append(protocols, agent_yaml.ProtocolVersionRecord{Protocol: "responses", Version: "ignored"})
					}
					wantError := tt.wantError
					if wantError == "" && len(tt.versions) > 0 && requested == "0.3" {
						wantError = "does not match the hosted-agent declaration"
					}
					err := validateHostedA2ACompatibility(protocols, requested)
					if wantError != "" {
						require.ErrorContains(t, err, wantError)
					} else {
						require.NoError(t, err)
					}
				})
			}
		}
	}
}

func TestNormalizeA2AWireVersion(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ declared, want string }{
		{"1.0", "1.0"}, {"1.0.7", "1.0"}, {"0.3.0", "0.3"}, {"1.1.0", "1.1"}, {"2.0", "2.0"},
	} {
		t.Run(tt.declared, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeA2AWireVersion(tt.declared)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHostedA2ARemoteTarget(t *testing.T) {
	t.Setenv(featureflags.A2A.EnvVar, "true")
	for _, tt := range []struct {
		name, explicit, wantError       string
		version, deployed               string
		wantDiagnostic                  string
		native                          any
		inputFile                       string
		fileBacked                      bool
		missingRef                      bool
		responsesOnly                   bool
		responses, prompt, noDefinition bool
		previewOff                      bool
	}{
		{name: "inline A2A hosted agent", native: "1.0.0"},
		{name: "preview off ignores local 1.0 declaration for wire default", native: "1.0.0", previewOff: true,
			wantDiagnostic: "requested A2A version 0.3 does not match"},
		{name: "file still requires explicit version", native: "1.0.0", inputFile: "missing.json",
			wantError: "requires an explicit --a2a-version"},
		{name: "unreadable request file remains blocking", native: "0.3", explicit: "1.0", inputFile: "missing.json",
			wantError: "failed to read input file"},
		{name: "A2A with Responses", native: "1.0.0", responses: true},
		{name: "Responses local mismatch is advisory", native: "1.0.0", responses: true, explicit: "0.3"},
		{name: "Responses local unsupported version is advisory", native: "0.3", responses: true},
		{name: "Responses local malformed version is advisory", native: "latest", responses: true},
		{name: "prompt definition bypass", prompt: true, explicit: "0.3"},
		{name: "missing definition bypass", noDefinition: true, explicit: "0.3"},
		{name: "missing local reference still invokes", missingRef: true},
		{name: "Responses-only defers to Foundry", responsesOnly: true},
		{name: "public A2A is not an implementation", responsesOnly: true, explicit: "0.3"},
		{name: "file-backed native", native: "1.0.0", fileBacked: true},
		{name: "numeric inline version is advisory", native: 0.3,
			wantDiagnostic: "could not inspect local A2A capabilities"},
		{name: "boolean inline version is advisory", native: true},
		{name: "numeric reference version is advisory", native: 0.3, fileBacked: true},
		{name: "local A2A 0.3 is advisory", native: "0.3.0"},
		{name: "local malformed version is advisory", native: "latest"},
		{name: "local explicit conflict is advisory", native: "1.0.0", explicit: "0.3"},
		{name: "edited local 0.3 current deployment omitted version", native: "0.3", explicit: "1.0", deployed: "7",
			wantDiagnostic: "local A2A compatibility finding (advisory"},
		{name: "edited local 0.3 explicit current deployment", native: "0.3", explicit: "1.0",
			version: "7", deployed: "7", wantDiagnostic: "local A2A compatibility finding (advisory"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.previewOff {
				t.Setenv(featureflags.A2A.EnvVar, "false")
			}
			dir := t.TempDir()
			properties := map[string]any{
				"kind": "hosted", "name": "deployed-agent",
				"protocols":     []any{map[string]any{"protocol": "a2a", "version": tt.native}},
				"agentEndpoint": map[string]any{"protocols": []any{"a2a"}},
			}
			if tt.responses {
				properties["protocols"] = []any{
					map[string]any{"protocol": "a2a", "version": tt.native},
					map[string]any{"protocol": "responses", "version": "2.0.0"},
				}
			}
			if tt.prompt {
				properties = map[string]any{"kind": "prompt", "name": "deployed-agent", "model": "test-model"}
			}
			if tt.noDefinition {
				properties = map[string]any{}
			}
			if tt.fileBacked {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "native.yaml"), fmt.Appendf(nil,
					"kind: hosted\nname: deployed-agent\nprotocols:\n  - protocol: a2a\n    version: %v\n", tt.native), 0600))
				properties = map[string]any{"$ref": "./native.yaml"}
			}
			if tt.missingRef {
				properties = map[string]any{"$ref": "./missing.yaml"}
			}
			if tt.responsesOnly {
				properties["protocols"] = []any{map[string]any{"protocol": "responses", "version": "2.0.0"}}
			}
			wireVersion := resolveA2AVersion("", !tt.previewOff)
			if tt.explicit != "" {
				wireVersion = tt.explicit
			}
			project := &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: dir,
				Services: map[string]*azdext.ServiceConfig{"target": {
					Name: "target", Host: AiAgentHost, AdditionalProperties: mustStruct(t, properties),
				}},
			}}
			address := newInvokeRemoteContextTestAzdServer(t, project, &testEnvironmentServiceServer{})
			t.Setenv("AZD_SERVER", address)
			client, err := azdext.NewAzdClient()
			require.NoError(t, err)
			defer client.Close()
			var diagnostics bytes.Buffer
			previousWriter := log.Writer()
			log.SetOutput(&diagnostics)
			t.Cleanup(func() { log.SetOutput(previousWriter) })
			var sessionCreates int
			previous := createInvokeVersionSession
			t.Cleanup(func() { createInvokeVersionSession = previous })
			createInvokeVersionSession = func(
				_ context.Context, endpoint, name, version, apiVersion string,
			) (*agent_api.AgentSessionResource, error) {
				sessionCreates++
				assert.NotEmpty(t, endpoint)
				assert.Equal(t, "deployed-agent", name)
				assert.Equal(t, "7", version)
				assert.Equal(t, "v1", apiVersion)
				return &agent_api.AgentSessionResource{AgentSessionID: "version-session"}, nil
			}
			var sends int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sends++
				assert.Equal(t, wireVersion, r.Header.Get("A2A-Version"))
				assert.Equal(t, "/agents/deployed-agent/endpoint/protocols/a2a", r.URL.Path)
				if tt.version != "" {
					assert.Equal(t, "version-session", r.URL.Query().Get("agent_session_id"))
				}
				var request a2aJSONRPCRequest
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				if wireVersion == "1.0" {
					assert.Equal(t, "SendMessage", request.Method)
				} else {
					assert.Equal(t, "message/send", request.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				if wireVersion == "0.3" {
					_, _ = io.WriteString(w, `{"jsonrpc":"2.0","result":`+
						`{"artifacts":[{"parts":[{"text":"native-mock-result"}]}]}}`)
				} else {
					_, _ = io.WriteString(w, `{"jsonrpc":"2.0","result":{"task":`+
						`{"artifacts":[{"parts":[{"text":"native-mock-result"}]}]}}}`)
				}
			}))
			defer server.Close()
			message := "hello"
			if tt.inputFile != "" {
				message = ""
			}
			action := &InvokeAction{
				flags: &invokeFlags{
					protocol: "a2a", message: message, a2aVersion: tt.explicit, inputFile: tt.inputFile,
				},
				credential: responseTestCredential{}, noPrompt: true,
				resolvedRemoteContext: &remoteContext{
					name: "deployed-agent", serviceName: "target", projectEndpoint: server.URL,
					apiVersion: "v1", azdClient: client, version: tt.version, deployedVersion: tt.deployed,
				},
			}
			output, err := captureStdout(t, func() error { return action.Run(t.Context()) })
			if tt.wantDiagnostic != "" {
				assert.Contains(t, diagnostics.String(), tt.wantDiagnostic)
			}
			if tt.version != "" {
				assert.Equal(t, 1, sessionCreates)
			} else {
				assert.Zero(t, sessionCreates)
			}
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				assert.Zero(t, sends)
			} else {
				require.NoError(t, err)
				assert.Equal(t, 1, sends)
				assert.Contains(t, output, "native-mock-result")
			}
		})
	}
}

func TestA2ATargetServiceLookupFailure(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled=%t", cancelled), func(t *testing.T) {
			client := newInvokeTestAzdClient(t, newInvokeUserConfigServer())
			action := &InvokeAction{flags: &invokeFlags{a2aVersion: "1.0"}, noPrompt: true}
			rc := &remoteContext{serviceName: "a2a-agent", azdClient: client}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if cancelled {
				cancel()
			}

			err := action.validateA2ATarget(ctx, rc, "1.0")
			if cancelled {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestA2ARemoteServiceLookupFailureStillInvokes(t *testing.T) {
	client := newInvokeTestAzdClient(t, newInvokeUserConfigServer())
	var sends int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends++
		assert.Equal(t, "/agents/deployed-agent/endpoint/protocols/a2a", r.URL.Path)
		assert.Equal(t, "1.0", r.Header.Get("A2A-Version"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","result":{"task":`+
			`{"artifacts":[{"parts":[{"text":"lookup-failure-result"}]}]}}}`)
	}))
	defer server.Close()
	action := &InvokeAction{
		flags:      &invokeFlags{protocol: "a2a", message: "hello", a2aVersion: "1.0"},
		credential: responseTestCredential{}, noPrompt: true,
		resolvedRemoteContext: &remoteContext{
			name: "deployed-agent", serviceName: "target", projectEndpoint: server.URL,
			apiVersion: "v1", azdClient: client,
		},
	}

	output, err := captureStdout(t, func() error { return action.a2aRemote(t.Context(), "1.0") })
	require.NoError(t, err)
	assert.Equal(t, 1, sends)
	assert.Contains(t, output, "lookup-failure-result")
}

func TestA2ATargetDoesNotApplyUnrelatedLocalDefinition(t *testing.T) {
	for _, tt := range []struct {
		name      string
		endpoint  bool
		noClient  bool
		noService bool
		version   string
		deployed  string
		session   string
	}{
		{name: "explicit endpoint ignores project definition", endpoint: true},
		{name: "explicit session ignores project definition", session: "existing-session", version: "2", deployed: "2"},
		{name: "no local client", noClient: true},
		{name: "no selected service", noService: true},
		{name: "historical agent version", version: "1", deployed: "2"},
		{name: "unknown deployed agent version", version: "1"},
		{name: "current version matches deployed is still advisory", version: "2", deployed: "2"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// A readable unsupported working-tree definition must never block remote invocation.
			project := &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: t.TempDir(),
				Services: map[string]*azdext.ServiceConfig{"a2a-agent": {
					Name: "a2a-agent", Host: AiAgentHost, AdditionalProperties: mustStruct(t, map[string]any{
						"kind": "hosted", "name": "deployed-agent",
						"protocols": []any{map[string]any{"protocol": "a2a", "version": "0.3"}},
					}),
				}},
			}}
			address := newInvokeRemoteContextTestAzdServer(t, project, &testEnvironmentServiceServer{})
			t.Setenv("AZD_SERVER", address)
			client, err := azdext.NewAzdClient()
			require.NoError(t, err)
			defer client.Close()
			action := &InvokeAction{flags: &invokeFlags{a2aVersion: "1.0", session: tt.session}, noPrompt: true}
			if tt.endpoint {
				action.endpoint = &parsedAgentEndpoint{}
			}
			rc := &remoteContext{
				serviceName: "a2a-agent", azdClient: client, version: tt.version, deployedVersion: tt.deployed,
			}
			if tt.noClient {
				rc.azdClient = nil
			}
			if tt.noService {
				rc.serviceName = ""
			}
			err = action.validateA2ATarget(t.Context(), rc, "1.0")
			require.NoError(t, err)
		})
	}
}

// Local guard.

// TestA2ALocal_NotSupported verifies A2A fails fast for local invocation.
func TestA2ALocal_NotSupported(t *testing.T) {
	t.Parallel()
	a := &InvokeAction{flags: &invokeFlags{local: true, message: "hi"}}
	if err := a.a2aLocal(t.Context()); err == nil {
		t.Fatal("expected an error for local a2a invocation, got nil")
	}
}
