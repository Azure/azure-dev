// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/protobuf/types/known/structpb"
)

// hostedAgentService builds a minimal hosted-agent service config.
func hostedAgentService(t *testing.T, name string) *azdext.ServiceConfig {
	t.Helper()
	props, err := structpb.NewStruct(map[string]any{
		"kind": "hosted",
	})
	if err != nil {
		t.Fatalf("failed to build service props: %v", err)
	}
	return &azdext.ServiceConfig{
		Name:                 name,
		Host:                 AiAgentHost,
		AdditionalProperties: props,
	}
}

func TestSessionCarryoverEnabled(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "yes", "on", " true "} {
		t.Run("enabled/"+v, func(t *testing.T) {
			t.Setenv(sessionCarryoverEnvVar, v)
			if !sessionCarryoverEnabled() {
				t.Fatalf("expected carry-over enabled when %s=%q", sessionCarryoverEnvVar, v)
			}
		})
	}

	for _, v := range []string{"", "0", "false", "no", "off", "maybe"} {
		t.Run("disabled/"+v, func(t *testing.T) {
			t.Setenv(sessionCarryoverEnvVar, v)
			if sessionCarryoverEnabled() {
				t.Fatalf("expected carry-over disabled when %s=%q", sessionCarryoverEnvVar, v)
			}
		})
	}
}

// stashSessionForTest seeds the package-level carry-over stash and registers
// cleanup so tests never leak state into each other.
func stashSessionForTest(t *testing.T, service, sessionID string) {
	t.Helper()
	pendingSessionCarryover.Lock()
	pendingSessionCarryover.byService[service] = sessionID
	pendingSessionCarryover.Unlock()
	t.Cleanup(func() {
		pendingSessionCarryover.Lock()
		delete(pendingSessionCarryover.byService, service)
		pendingSessionCarryover.Unlock()
	})
}

func stashedSession(service string) (string, bool) {
	pendingSessionCarryover.Lock()
	defer pendingSessionCarryover.Unlock()
	v, ok := pendingSessionCarryover.byService[service]
	return v, ok
}

func TestCaptureSessionForCarryover_NotOptedInIsNoOp(t *testing.T) {
	// Env var unset -> disabled. nil azdClient must not be dereferenced.
	svc := &azdext.ServiceConfig{Name: "capture-disabled", Host: AiAgentHost}
	captureSessionForCarryover(t.Context(), nil, svc)

	if _, ok := stashedSession(svc.Name); ok {
		t.Fatalf("expected no session stashed when service has not opted in")
	}
}

func TestCaptureSessionForCarryover_NilServiceIsNoOp(t *testing.T) {
	// nil service and nil client: must return without panic.
	captureSessionForCarryover(t.Context(), nil, nil)
}

func TestCarryOverSessionAfterDeploy_NotOptedInIsNoOp(t *testing.T) {
	svc := &azdext.ServiceConfig{Name: "carry-disabled", Host: AiAgentHost}
	stashSessionForTest(t, svc.Name, "sess-123")

	// Not opted in: must return before touching the (nil) client and must leave
	// the stash untouched.
	carryOverSessionAfterDeploy(t.Context(), nil, nil, svc, "env")

	if got, ok := stashedSession(svc.Name); !ok || got != "sess-123" {
		t.Fatalf("expected stash untouched when not opted in, got %q (present=%v)", got, ok)
	}
}

func TestCarryOverSessionAfterDeploy_NilAgentClientIsNoOp(t *testing.T) {
	t.Setenv(sessionCarryoverEnvVar, "true")
	svc := hostedAgentService(t, "nil-client")
	stashSessionForTest(t, svc.Name, "sess-abc")

	// Opted in but nil agentClient: guard must return before any RPC and leave
	// the stash so a later, well-formed call could still consume it.
	carryOverSessionAfterDeploy(t.Context(), nil, nil, svc, "env")

	if got, ok := stashedSession(svc.Name); !ok || got != "sess-abc" {
		t.Fatalf("expected stash untouched when agentClient is nil, got %q (present=%v)", got, ok)
	}
}

// respErr builds an azcore.ResponseError with the given HTTP status and Foundry
// error code, mirroring what StopSession surfaces from the service.
func respErr(status int, code string) error {
	return &azcore.ResponseError{
		StatusCode: status,
		ErrorCode:  code,
		RawResponse: &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader("")),
		},
	}
}

func TestClassifyStopSessionErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want stopSessionOutcome
	}{
		{
			name: "nil error -> proceed",
			err:  nil,
			want: stopOutcomeProceed,
		},
		{
			name: "409 session_already_stopped -> proceed",
			err:  respErr(http.StatusConflict, "session_already_stopped"),
			want: stopOutcomeProceed,
		},
		{
			name: "404 not found -> skip",
			err:  respErr(http.StatusNotFound, "session_not_found"),
			want: stopOutcomeSkip,
		},
		{
			name: "409 with a different code -> skip",
			err:  respErr(http.StatusConflict, "some_other_conflict"),
			want: stopOutcomeSkip,
		},
		{
			name: "500 server error -> skip",
			err:  respErr(http.StatusInternalServerError, ""),
			want: stopOutcomeSkip,
		},
		{
			name: "non-response error -> skip",
			err:  errors.New("connection reset"),
			want: stopOutcomeSkip,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyStopSessionErr(tc.err); got != tc.want {
				t.Fatalf("classifyStopSessionErr() = %v, want %v", got, tc.want)
			}
		})
	}
}
