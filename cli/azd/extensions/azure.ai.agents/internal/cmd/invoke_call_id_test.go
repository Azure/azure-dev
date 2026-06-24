// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
)

// TestLocalInvoke_CallIDHeader verifies that --call-id is sent as the
// x-agent-foundry-call-id header on local invocations for both protocols.
func TestLocalInvoke_CallIDHeader(t *testing.T) {
	okBody, _ := json.Marshal(map[string]any{
		"output": []any{map[string]any{"content": []any{map[string]any{"type": "output_text", "text": "hi"}}}},
	})

	cases := []struct {
		name         string
		protocol     string
		callID       string
		userIdentity string
		wantCallID   bool
		wantUserID   bool
	}{
		{"responses_with_call_id", "responses", "call-123", "", true, false},
		{"responses_without_call_id", "responses", "", "", false, false},
		{"invocations_with_call_id", "invocations", "call-456", "", true, false},
		{"invocations_without_call_id", "invocations", "", "", false, false},
		{"responses_with_user_identity", "responses", "", "user-123", false, true},
		{"invocations_with_user_identity", "invocations", "", "user-456", false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotHeader string
			var headerPresent bool
			var gotUserHeader string
			var userHeaderPresent bool
			var requestCount int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/openapi") {
					w.WriteHeader(404)
					return
				}
				requestCount++
				gotHeader = r.Header.Get(agent_api.AgentFoundryCallIDHeader)
				_, headerPresent = r.Header[http.CanonicalHeaderKey(agent_api.AgentFoundryCallIDHeader)]
				gotUserHeader = r.Header.Get(agent_api.AgentUserIDHeader)
				_, userHeaderPresent = r.Header[http.CanonicalHeaderKey(agent_api.AgentUserIDHeader)]
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(200)
				fmt.Fprint(w, string(okBody))
			}))
			defer srv.Close()

			action := &InvokeAction{
				flags: &invokeFlags{
					message:  "hi",
					port:     testPort(t, srv.URL),
					local:    true,
					protocol: tc.protocol,
					callID:   tc.callID,
					userIdentityFlags: userIdentityFlags{
						userIdentity: tc.userIdentity,
					},
				},
				noPrompt: true,
			}

			var err error
			withCapturedStdout(t, func() {
				if tc.protocol == "responses" {
					err = action.responsesLocal(t.Context())
				} else {
					err = action.invocationsLocal(t.Context())
				}
			})
			if err != nil {
				t.Fatalf("local invoke failed: %v", err)
			}
			if requestCount == 0 {
				t.Fatal("expected at least one request to local server")
			}

			assertLocalHeader(
				t,
				agent_api.AgentFoundryCallIDHeader,
				gotHeader,
				tc.callID,
				headerPresent,
				tc.wantCallID,
			)
			assertLocalHeader(
				t,
				agent_api.AgentUserIDHeader,
				gotUserHeader,
				tc.userIdentity,
				userHeaderPresent,
				tc.wantUserID,
			)
		})
	}
}

func assertLocalHeader(t *testing.T, header, got, want string, present bool, shouldBeSet bool) {
	t.Helper()

	if shouldBeSet {
		if got != want {
			t.Errorf("header %s = %q, want %q", header, got, want)
		}
		return
	}

	if present {
		t.Errorf("header %s should not be set, got %q", header, got)
	}
}
