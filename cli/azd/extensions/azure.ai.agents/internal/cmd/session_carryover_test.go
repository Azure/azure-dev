// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// azdextServiceConfigForTest builds a minimal hosted-agent service config for
// carry-over guard tests.
func azdextServiceConfigForTest(name string) azdext.ServiceConfig {
	return azdext.ServiceConfig{Name: name, Host: AiAgentHost}
}

func TestSessionCarryoverEnabled(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "unset defaults to enabled", value: "", want: true},
		{name: "1 disables", value: "1", want: false},
		{name: "true disables", value: "true", want: false},
		{name: "TRUE (case-insensitive) disables", value: "TRUE", want: false},
		{name: "yes disables", value: "yes", want: false},
		{name: "on disables", value: "on", want: false},
		{name: "padded true disables", value: "  true  ", want: false},
		{name: "0 stays enabled", value: "0", want: true},
		{name: "false stays enabled", value: "false", want: true},
		{name: "garbage stays enabled", value: "maybe", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(sessionCarryoverOptOutEnvVar, tc.value)
			if got := sessionCarryoverEnabled(); got != tc.want {
				t.Fatalf("sessionCarryoverEnabled() with %q = %v, want %v", tc.value, got, tc.want)
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

func TestCaptureSessionForCarryover_DisabledIsNoOp(t *testing.T) {
	t.Setenv(sessionCarryoverOptOutEnvVar, "1")

	// nil azdClient must not be dereferenced when carry-over is disabled.
	svc := azdextServiceConfigForTest("disabled-svc")
	captureSessionForCarryover(t.Context(), nil, &svc)

	if _, ok := stashedSession("disabled-svc"); ok {
		t.Fatalf("expected no session stashed when carry-over is disabled")
	}
}

func TestCaptureSessionForCarryover_NilServiceIsNoOp(t *testing.T) {
	t.Setenv(sessionCarryoverOptOutEnvVar, "")

	// nil service and nil client: must return without panic.
	captureSessionForCarryover(t.Context(), nil, nil)
}

func TestCarryOverSessionAfterDeploy_DisabledIsNoOp(t *testing.T) {
	t.Setenv(sessionCarryoverOptOutEnvVar, "1")

	svc := azdextServiceConfigForTest("disabled-carry")
	stashSessionForTest(t, svc.Name, "sess-123")

	// Disabled: must return before touching the (nil) client and must leave the
	// stash untouched.
	carryOverSessionAfterDeploy(t.Context(), nil, nil, &svc, "env")

	if got, ok := stashedSession(svc.Name); !ok || got != "sess-123" {
		t.Fatalf("expected stash untouched when disabled, got %q (present=%v)", got, ok)
	}
}

func TestCarryOverSessionAfterDeploy_NilAgentClientIsNoOp(t *testing.T) {
	t.Setenv(sessionCarryoverOptOutEnvVar, "")

	svc := azdextServiceConfigForTest("nil-client")
	stashSessionForTest(t, svc.Name, "sess-abc")

	// nil agentClient: guard must return before any RPC and leave the stash so a
	// later, well-formed call could still consume it.
	carryOverSessionAfterDeploy(t.Context(), nil, nil, &svc, "env")

	if got, ok := stashedSession(svc.Name); !ok || got != "sess-abc" {
		t.Fatalf("expected stash untouched when agentClient is nil, got %q (present=%v)", got, ok)
	}
}
