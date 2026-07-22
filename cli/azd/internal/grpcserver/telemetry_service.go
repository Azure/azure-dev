// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"slices"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext/telemetry"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// maxTelemetryFieldLength bounds the accepted key and value length. This is a
// defensive limit against oversized input, not a privacy control. The value
// allowlist is the privacy control.
const maxTelemetryFieldLength = 128

// commandUsageFieldPolicy is the host-owned policy for one extension-settable
// command usage attribute.
type commandUsageFieldPolicy struct {
	key                  fields.AttributeKey
	allowedValues        map[string]struct{}
	eligibleEvents       map[string]struct{}
	requiredCapabilities map[extensions.CapabilityType]struct{}
}

// extensionUsageFields is the allowlist of command usage attributes an
// authenticated extension may contribute. The host owns every field's key,
// classification, allowed values, eligible command events, and required
// capabilities. No extension identifier is hardcoded: eligibility is expressed
// through signed extension capabilities and the fixed value set.
var extensionUsageFields = map[string]commandUsageFieldPolicy{
	telemetry.AgentDeploymentModeAttribute: {
		key: fields.AgentDeploymentModeKey,
		allowedValues: map[string]struct{}{
			string(telemetry.AgentDeploymentModeCode):      {},
			string(telemetry.AgentDeploymentModeContainer): {},
			string(telemetry.AgentDeploymentModeByoImage):  {},
		},
		eligibleEvents: map[string]struct{}{
			events.GetCommandEventName("azd deploy"): {},
			events.GetCommandEventName("azd up"):     {},
		},
		requiredCapabilities: map[extensions.CapabilityType]struct{}{
			extensions.ServiceTargetProviderCapability: {},
		},
	},
}

// telemetryService implements azdext.TelemetryServiceServer.
type telemetryService struct {
	azdext.UnimplementedTelemetryServiceServer
	fields map[string]commandUsageFieldPolicy
}

// NewTelemetryService creates the telemetry gRPC service. It holds no injected
// state; the handler reaches the process-global command usage store through the
// tracing package. Returning the interface type lets the IoC container satisfy
// the azdext.TelemetryServiceServer parameter on NewServer without an adapter.
func NewTelemetryService() azdext.TelemetryServiceServer {
	return newTelemetryService(extensionUsageFields)
}

func newTelemetryService(fieldPolicies map[string]commandUsageFieldPolicy) *telemetryService {
	return &telemetryService{fields: fieldPolicies}
}

// AddCommandUsageAttribute validates an authenticated extension's request
// against the host allowlist and, when valid, appends the value to the current
// command usage scope. It fails closed: unknown keys, missing capabilities,
// invalid policies, and disallowed values are all rejected before anything is
// recorded. Rejected caller text is never echoed into the returned error.
func (s *telemetryService) AddCommandUsageAttribute(
	ctx context.Context,
	req *azdext.AddCommandUsageAttributeRequest,
) (*azdext.AddCommandUsageAttributeResponse, error) {
	claims, err := extensions.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "validated extension claims are required")
	}

	if req == nil ||
		req.Key == "" || req.Value == "" ||
		len(req.Key) > maxTelemetryFieldLength || len(req.Value) > maxTelemetryFieldLength {
		return nil, status.Error(codes.InvalidArgument, "telemetry key and value are required")
	}

	policy, ok := s.fields[req.Key]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "telemetry key is not registered")
	}

	if !hasRequiredCapability(claims, policy.requiredCapabilities) {
		return nil, status.Error(codes.PermissionDenied, "extension lacks the required capability")
	}

	if len(policy.allowedValues) == 0 || len(policy.eligibleEvents) == 0 {
		return nil, status.Error(codes.Internal, "telemetry field policy is invalid")
	}

	if _, ok := policy.allowedValues[req.Value]; !ok {
		return nil, status.Error(codes.InvalidArgument, "telemetry value is not allowed")
	}

	accepted := tracing.TryAppendCommandUsageUnique(policy.eligibleEvents, policy.key.Key, req.Value)

	return &azdext.AddCommandUsageAttributeResponse{Accepted: accepted}, nil
}

// hasRequiredCapability reports whether the signed extension claims carry every
// required capability. Capabilities are part of the host-signed token, so they
// are trustworthy without a separate manager lookup.
func hasRequiredCapability(
	claims *extensions.ExtensionClaims,
	required map[extensions.CapabilityType]struct{},
) bool {
	for capability := range required {
		if !slices.Contains(claims.Capabilities, capability) {
			return false
		}
	}

	return true
}
