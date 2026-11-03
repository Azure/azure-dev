// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"testing"
	"time"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAgentInvokeSelectedWireContract(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name        string
		protocol    string
		longRunning bool
		noWait      bool
		want        map[string]string
	}{
		{"responses foreground", "responses", false, false,
			map[string]string{
				"agent.invoke.protocol": "responses", "agent.invoke.long_running": "false",
				"agent.invoke.no_wait": "false",
			}},
		{"responses attached", "responses", true, false,
			map[string]string{
				"agent.invoke.protocol": "responses", "agent.invoke.long_running": "true",
				"agent.invoke.no_wait": "false",
			}},
		{"responses detached", "responses", true, true,
			map[string]string{
				"agent.invoke.protocol": "responses", "agent.invoke.long_running": "true",
				"agent.invoke.no_wait": "true",
			}},
		{"invocations", "invocations", false, false,
			map[string]string{
				"agent.invoke.protocol": "invocations", "agent.invoke.long_running": "false",
				"agent.invoke.no_wait": "false",
			}},
		{"a2a", "a2a", false, false,
			map[string]string{
				"agent.invoke.protocol": "a2a", "agent.invoke.long_running": "false",
				"agent.invoke.no_wait": "false",
			}},
		{"future invocable protocol", "future_protocol", false, false,
			map[string]string{
				"agent.invoke.protocol": "future_protocol", "agent.invoke.long_running": "false",
				"agent.invoke.no_wait": "false",
			}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			event := AgentInvokeSelected(tt.protocol, tt.longRunning, tt.noWait)
			if event.Name != "agent.invoke.selected" {
				t.Fatalf("event name = %q, want agent.invoke.selected", event.Name)
			}
			if !maps.Equal(event.Attributes, tt.want) {
				t.Fatalf("event attributes = %#v, want %#v", event.Attributes, tt.want)
			}
		})
	}
}

func TestLocalClientRouteSelectedWireContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		route     LocalClientRoute
		wantValue string
	}{
		{
			name:      "inspector",
			route:     LocalClientRouteInspector,
			wantValue: "inspector",
		},
		{
			name:      "playground",
			route:     LocalClientRoutePlayground,
			wantValue: "playground",
		},
		{
			name:      "suppressed",
			route:     LocalClientRouteSuppressed,
			wantValue: "suppressed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			event := LocalClientRouteSelected(tt.route)
			if event.Name != "local_client.route.selected" {
				t.Fatalf("event name = %q, want %q", event.Name, "local_client.route.selected")
			}

			wantAttributes := map[string]string{"route": tt.wantValue}
			if !maps.Equal(event.Attributes, wantAttributes) {
				t.Fatalf("event attributes = %#v, want %#v", event.Attributes, wantAttributes)
			}
		})
	}
}

func TestAgentSnapshotDeployCompleted(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		err     error
		outcome string
		code    string
	}{
		{"success", nil, "succeeded", "none"},
		{"failure", errors.New("customer-secret"), "failed", "other"},
		{"deadline", fmt.Errorf("customer-secret: %w", context.DeadlineExceeded), "timeout", "deadline_exceeded"},
		{"cancellation", fmt.Errorf("customer-secret: %w", context.Canceled), "cancelled", "cancelled"},
		{"structured cancellation", exterrors.Cancelled("customer-secret"), "cancelled", "cancelled"},
		{"RPC cancellation", status.Error(codes.Canceled, "customer-secret"), "cancelled", "cancelled"},
		{"RPC deadline", status.Error(codes.DeadlineExceeded, "customer-secret"), "timeout", "deadline_exceeded"},
		{"activation timeout", &azdext.ServiceError{ErrorCode: "create_agent.timeout"},
			"timeout", "create_agent.timeout"},
		{"snapshot failure", &azdext.ServiceError{ErrorCode: "create_agent.SnapshotCreationError"},
			"failed", "create_agent.SnapshotCreationError"},
		{"provisioning failure", &azdext.ServiceError{ErrorCode: "create_agent.ProvisioningError"},
			"failed", "create_agent.ProvisioningError"},
		{"image failure", &azdext.ServiceError{ErrorCode: "create_agent.ImageError"}, "failed", "create_agent.ImageError"},
		{"code failure", &azdext.ServiceError{ErrorCode: "create_agent.CodeError"}, "failed", "create_agent.CodeError"},
		{"unknown service code", &azdext.ServiceError{ErrorCode: "customer-secret", Message: "customer-secret"},
			"failed", "other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			event := AgentSnapshotDeployCompleted(tc.err, 1250*time.Millisecond)
			require.Equal(t, "agent.snapshot.deploy.completed", event.Name)
			require.Equal(t, map[string]string{
				"agent.memory_snapshot": "true", "agent.deploy.outcome": tc.outcome,
				"agent.deploy.error_code": tc.code, "agent.deploy.duration_ms": "1250",
			}, event.Attributes)
			for value := range maps.Values(event.Attributes) {
				require.NotContains(t, value, "customer-secret")
			}
		})
	}
	require.Equal(t, "0", AgentSnapshotDeployCompleted(nil, -time.Second).Attributes["agent.deploy.duration_ms"])
}
