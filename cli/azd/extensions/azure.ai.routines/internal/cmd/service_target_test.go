// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestParseRoutineServiceConfig_ServiceLevel(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"description": "nightly summary",
		"enabled":     true,
		"triggers": map[string]any{
			"default": map[string]any{"type": "recurring", "cron_expression": "0 9 * * *"},
		},
		"action": map[string]any{"type": "invoke_agent_responses_api", "agent_name": "summarizer"},
	})
	require.NoError(t, err)

	body, err := parseRoutineServiceConfig(&azdext.ServiceConfig{
		Name:                 "nightly",
		Host:                 aiRoutineHost,
		AdditionalProperties: props,
	})
	require.NoError(t, err)
	assert.Equal(t, "nightly summary", body.Description)
	require.NotNil(t, body.Enabled)
	assert.True(t, *body.Enabled)
	require.Contains(t, body.Triggers, "default")
	assert.Equal(t, "recurring", body.Triggers["default"].Type)
	assert.Equal(t, "0 9 * * *", body.Triggers["default"].CronExpression)
	require.NotNil(t, body.Action)
	assert.Equal(t, "summarizer", body.Action.AgentName)
}

// TestParseRoutineServiceConfig_ConfigFallback verifies routines written before
// the per-resource service split (config-nested shape) still parse.
func TestParseRoutineServiceConfig_ConfigFallback(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{"description": "legacy"})
	require.NoError(t, err)

	body, err := parseRoutineServiceConfig(&azdext.ServiceConfig{
		Name:   "legacy",
		Host:   aiRoutineHost,
		Config: props,
	})
	require.NoError(t, err)
	assert.Equal(t, "legacy", body.Description)
}

func TestExpandRoutineValue(t *testing.T) {
	t.Parallel()

	serviceConfig := &azdext.ServiceConfig{
		Environment: map[string]string{"DIGEST_TOPIC": "weekly changes"},
	}
	environment, err := (&routineServiceTarget{}).environmentValues(
		t.Context(),
		serviceConfig,
	)
	require.NoError(t, err)
	input := map[string]any{
		"topic":  "${DIGEST_TOPIC}",
		"secret": "${{connections.search.credentials.key}}",
	}

	assert.Equal(t, map[string]any{
		"topic":  "weekly changes",
		"secret": "${{connections.search.credentials.key}}",
	}, expandRoutineValue(input, environment))
}

// fakeServiceConfigReader reports a fixed env-declared result.
type fakeServiceConfigReader struct {
	found bool
}

func (f fakeServiceConfigReader) GetServiceConfigValue(
	context.Context,
	*azdext.GetServiceConfigValueRequest,
	...grpc.CallOption,
) (*azdext.GetServiceConfigValueResponse, error) {
	return &azdext.GetServiceConfigValueResponse{Found: f.found}, nil
}

func TestRoutineEnvironmentValuesEmptyDeclaredIsolates(t *testing.T) {
	t.Parallel()

	target := &routineServiceTarget{
		projectClient: fakeServiceConfigReader{found: true},
	}
	env, err := target.environmentValues(
		t.Context(),
		&azdext.ServiceConfig{Name: "nightly-digest"},
	)
	require.NoError(t, err)
	require.Empty(t, env)
}

func TestResolveRoutineServiceTenant(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		environment     *stubRoutineEnvironment
		account         *stubRoutineAccount
		wantTenant      string
		wantErr         string
		wantEnvironment string
		wantSubID       string
	}{
		{
			name: "uses user access tenant for active environment subscription",
			environment: &stubRoutineEnvironment{
				name:           "dev",
				subscriptionID: "subscription-id",
			},
			account:         &stubRoutineAccount{tenantID: "user-access-tenant"},
			wantTenant:      "user-access-tenant",
			wantEnvironment: "dev",
			wantSubID:       "subscription-id",
		},
		{
			name:        "fails when subscription is missing",
			environment: &stubRoutineEnvironment{name: "dev"},
			account:     &stubRoutineAccount{},
			wantErr:     "AZURE_SUBSCRIPTION_ID is required",
		},
		{
			name: "propagates tenant lookup failure",
			environment: &stubRoutineEnvironment{
				name:           "dev",
				subscriptionID: "subscription-id",
			},
			account: &stubRoutineAccount{err: errors.New("lookup failed")},
			wantErr: "resolving user access tenant: lookup failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tenantID, err := resolveRoutineServiceTenant(
				t.Context(),
				tt.environment,
				tt.account,
			)

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantTenant, tenantID)
			assert.Equal(t, tt.wantEnvironment, tt.environment.gotEnvironment)
			assert.Equal(t, tt.wantSubID, tt.account.gotSubscriptionID)
		})
	}
}

type stubRoutineEnvironment struct {
	name           string
	subscriptionID string
	currentErr     error
	valueErr       error
	gotEnvironment string
}

func (s *stubRoutineEnvironment) GetCurrent(
	context.Context,
	*azdext.EmptyRequest,
	...grpc.CallOption,
) (*azdext.EnvironmentResponse, error) {
	if s.currentErr != nil {
		return nil, s.currentErr
	}
	return &azdext.EnvironmentResponse{
		Environment: &azdext.Environment{Name: s.name},
	}, nil
}

func (s *stubRoutineEnvironment) GetValue(
	_ context.Context,
	request *azdext.GetEnvRequest,
	_ ...grpc.CallOption,
) (*azdext.KeyValueResponse, error) {
	s.gotEnvironment = request.GetEnvName()
	if s.valueErr != nil {
		return nil, s.valueErr
	}
	return &azdext.KeyValueResponse{Value: s.subscriptionID}, nil
}

type stubRoutineAccount struct {
	tenantID          string
	err               error
	gotSubscriptionID string
}

func (s *stubRoutineAccount) LookupTenant(
	_ context.Context,
	request *azdext.LookupTenantRequest,
	_ ...grpc.CallOption,
) (*azdext.LookupTenantResponse, error) {
	s.gotSubscriptionID = request.GetSubscriptionId()
	if s.err != nil {
		return nil, s.err
	}
	return &azdext.LookupTenantResponse{TenantId: s.tenantID}, nil
}
