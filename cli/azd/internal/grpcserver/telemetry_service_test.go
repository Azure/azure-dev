// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext/telemetry"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
)

func claimsContext(caps ...extensions.CapabilityType) context.Context {
	claims := &extensions.ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "test.extension"},
		Capabilities:     caps,
	}
	return extensions.WithClaimsContext(context.Background(), claims)
}

func serviceTargetClaims() context.Context {
	return claimsContext(extensions.ServiceTargetProviderCapability)
}

func validRequest(value string) *azdext.AddCommandUsageAttributeRequest {
	return &azdext.AddCommandUsageAttributeRequest{
		Key:   telemetry.AgentDeploymentModeAttribute,
		Value: value,
	}
}

func TestTelemetryService_MissingClaims(t *testing.T) {
	tracing.ResetCommandUsageForTest()
	t.Cleanup(tracing.ResetCommandUsageForTest)

	svc := NewTelemetryService()
	_, err := svc.AddCommandUsageAttribute(context.Background(), validRequest("code"))
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestTelemetryService_MissingCapability(t *testing.T) {
	tracing.ResetCommandUsageForTest()
	t.Cleanup(tracing.ResetCommandUsageForTest)

	scope := tracing.BeginCommandUsageScope("cmd.deploy")
	t.Cleanup(func() { _, _ = tracing.CloseCommandUsageScope(scope) })

	svc := NewTelemetryService()
	// Authenticated but without the service-target-provider capability.
	_, err := svc.AddCommandUsageAttribute(claimsContext(), validRequest("code"))
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestTelemetryService_InvalidArguments(t *testing.T) {
	tracing.ResetCommandUsageForTest()
	t.Cleanup(tracing.ResetCommandUsageForTest)

	svc := NewTelemetryService()
	ctx := serviceTargetClaims()

	cases := map[string]*azdext.AddCommandUsageAttributeRequest{
		"nil request":  nil,
		"empty key":    {Key: "", Value: "code"},
		"empty value":  {Key: telemetry.AgentDeploymentModeAttribute, Value: ""},
		"oversize key": {Key: strings.Repeat("k", maxTelemetryFieldLength+1), Value: "code"},
		"oversize value": {
			Key:   telemetry.AgentDeploymentModeAttribute,
			Value: strings.Repeat("v", maxTelemetryFieldLength+1),
		},
		"unknown key":  {Key: "some.other.key", Value: "code"},
		"invalid enum": validRequest("bogus"),
	}

	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := svc.AddCommandUsageAttribute(ctx, req)
			require.Equal(t, codes.InvalidArgument, status.Code(err), "case %q", name)
		})
	}
}

func TestTelemetryService_InvalidPolicyIsInternal(t *testing.T) {
	tracing.ResetCommandUsageForTest()
	t.Cleanup(tracing.ResetCommandUsageForTest)

	ctx := serviceTargetClaims()
	capSet := map[extensions.CapabilityType]struct{}{
		extensions.ServiceTargetProviderCapability: {},
	}

	t.Run("empty allowed values", func(t *testing.T) {
		svc := newTelemetryService(map[string]commandUsageFieldPolicy{
			telemetry.AgentDeploymentModeAttribute: {
				key:                  fields.AgentDeploymentModeKey,
				allowedValues:        map[string]struct{}{},
				eligibleEvents:       map[string]struct{}{"cmd.deploy": {}},
				requiredCapabilities: capSet,
			},
		})
		_, err := svc.AddCommandUsageAttribute(ctx, validRequest("code"))
		require.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("empty eligible events", func(t *testing.T) {
		svc := newTelemetryService(map[string]commandUsageFieldPolicy{
			telemetry.AgentDeploymentModeAttribute: {
				key:                  fields.AgentDeploymentModeKey,
				allowedValues:        map[string]struct{}{"code": {}},
				eligibleEvents:       map[string]struct{}{},
				requiredCapabilities: capSet,
			},
		})
		_, err := svc.AddCommandUsageAttribute(ctx, validRequest("code"))
		require.Equal(t, codes.Internal, status.Code(err))
	})
}

func TestTelemetryService_EligibleScopesAccept(t *testing.T) {
	for _, eventName := range []string{"cmd.deploy", "cmd.up"} {
		t.Run(eventName, func(t *testing.T) {
			tracing.ResetCommandUsageForTest()
			t.Cleanup(tracing.ResetCommandUsageForTest)

			scope := tracing.BeginCommandUsageScope(eventName)
			svc := NewTelemetryService()

			resp, err := svc.AddCommandUsageAttribute(serviceTargetClaims(), validRequest("code"))
			require.NoError(t, err)
			require.True(t, resp.Accepted)

			attrs, err := tracing.CloseCommandUsageScope(scope)
			require.NoError(t, err)
			require.Len(t, attrs, 1)
			require.Equal(t, telemetry.AgentDeploymentModeAttribute, string(attrs[0].Key))
			require.Equal(t, []string{"code"}, attrs[0].Value.AsStringSlice())
		})
	}
}

func TestTelemetryService_IneligibleScopeNotAccepted(t *testing.T) {
	tracing.ResetCommandUsageForTest()
	t.Cleanup(tracing.ResetCommandUsageForTest)

	scope := tracing.BeginCommandUsageScope("cmd.package")
	svc := NewTelemetryService()

	resp, err := svc.AddCommandUsageAttribute(serviceTargetClaims(), validRequest("code"))
	require.NoError(t, err)
	require.False(t, resp.Accepted)

	attrs, err := tracing.CloseCommandUsageScope(scope)
	require.NoError(t, err)
	require.Empty(t, attrs)
}

func TestTelemetryService_NoActiveScopeNotAccepted(t *testing.T) {
	tracing.ResetCommandUsageForTest()
	t.Cleanup(tracing.ResetCommandUsageForTest)

	svc := NewTelemetryService()
	resp, err := svc.AddCommandUsageAttribute(serviceTargetClaims(), validRequest("code"))
	require.NoError(t, err)
	require.False(t, resp.Accepted)
}

func TestTelemetryService_DuplicateCollapses(t *testing.T) {
	tracing.ResetCommandUsageForTest()
	t.Cleanup(tracing.ResetCommandUsageForTest)

	scope := tracing.BeginCommandUsageScope("cmd.deploy")
	svc := NewTelemetryService()
	ctx := serviceTargetClaims()

	for i := 0; i < 3; i++ {
		resp, err := svc.AddCommandUsageAttribute(ctx, validRequest("code"))
		require.NoError(t, err)
		require.True(t, resp.Accepted)
	}

	attrs, err := tracing.CloseCommandUsageScope(scope)
	require.NoError(t, err)
	require.Len(t, attrs, 1)
	require.Equal(t, []string{"code"}, attrs[0].Value.AsStringSlice())
}

func TestTelemetryService_ConcurrentReports(t *testing.T) {
	tracing.ResetCommandUsageForTest()
	t.Cleanup(tracing.ResetCommandUsageForTest)

	scope := tracing.BeginCommandUsageScope("cmd.up")
	svc := NewTelemetryService()
	ctx := serviceTargetClaims()

	modes := []string{"code", "container", "byo_image"}
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		value := modes[i%len(modes)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.AddCommandUsageAttribute(ctx, validRequest(value))
			require.NoError(t, err)
		}()
	}
	wg.Wait()

	attrs, err := tracing.CloseCommandUsageScope(scope)
	require.NoError(t, err)
	require.Len(t, attrs, 1)
	require.ElementsMatch(t, modes, attrs[0].Value.AsStringSlice())
}

// TestExtensionUsageFieldsInvariants guards the production allowlist so a future
// field cannot accidentally open a free-form or unauthenticated path.
func TestExtensionUsageFieldsInvariants(t *testing.T) {
	t.Parallel()

	require.NotEmpty(t, extensionUsageFields)

	for registryKey, policy := range extensionUsageFields {
		require.NotEmpty(t, registryKey)
		require.Equal(t, registryKey, string(policy.key.Key),
			"policy key must match its registry key")
		require.NotEmpty(t, string(policy.key.Classification), "classification must be set")
		require.NotEmpty(t, string(policy.key.Purpose), "purpose must be set")

		require.NotEmpty(t, policy.allowedValues, "allowed values must be set")
		for value := range policy.allowedValues {
			require.NotEmpty(t, value, "allowed value must not be empty")
		}

		require.NotEmpty(t, policy.eligibleEvents, "eligible events must be set")
		for event := range policy.eligibleEvents {
			require.NotEmpty(t, event, "eligible event must not be empty")
		}

		require.NotEmpty(t, policy.requiredCapabilities, "required capabilities must be set")
	}
}
