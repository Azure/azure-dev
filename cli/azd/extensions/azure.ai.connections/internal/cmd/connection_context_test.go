// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// TestNewCredential covers both credential shapes: the default (home) tenant
// when no tenant is resolved, and a tenant-scoped credential for multi-tenant /
// guest users. The tenant-scoped branch is what fixes "Tenant provided in token
// does not match resource token".
func TestNewCredential(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		tenantID string
	}{
		{name: "default tenant", tenantID: ""},
		{name: "scoped tenant", tenantID: "11111111-1111-1111-1111-111111111111"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cred, err := newCredential(tc.tenantID)
			require.NoError(t, err)
			require.NotNil(t, cred)
		})
	}
}

func TestResolveEnvContextUsesSelectedEnvironment(t *testing.T) {
	t.Parallel()

	environment := &recordingEnvironmentContextReader{values: map[string]string{
		"AZURE_AI_PROJECT_ID":   "/subscriptions/sub/resourceGroups/rg/providers/provider/accounts/account/projects/project",
		"AZURE_SUBSCRIPTION_ID": "sub",
	}}
	account := &recordingTenantLookup{}

	resolved := resolveEnvContextWithClients(t.Context(), "staging", environment, account)

	assert.Equal(t, environment.values["AZURE_AI_PROJECT_ID"], resolved.projectID)
	assert.Equal(t, "tenant", resolved.tenantID)
	assert.Equal(t, 0, environment.currentCalls)
	assert.Equal(t, []string{"staging"}, environment.requestedEnvironments)
	assert.Zero(t, environment.getValueCalls)
	assert.Equal(t, 1, environment.getValuesCalls)
	assert.Equal(t, []string{"sub"}, account.subscriptions)
}

func TestResolveEnvContextDoesNotUseProcessFallback(t *testing.T) {
	t.Setenv("AZURE_AI_PROJECT_ID", "process-project")
	t.Setenv("AZURE_SUBSCRIPTION_ID", "process-subscription")
	for _, tt := range []struct {
		name          string
		values        map[string]string
		readErr       error
		wantProjectID string
		wantTenant    string
	}{
		{name: "neither value persisted"},
		{name: "project only", values: map[string]string{"AZURE_AI_PROJECT_ID": "persisted-project"},
			wantProjectID: "persisted-project"},
		{name: "subscription only", values: map[string]string{"AZURE_SUBSCRIPTION_ID": "persisted-sub"},
			wantTenant: "tenant"},
		{name: "empty persisted values", values: map[string]string{"AZURE_AI_PROJECT_ID": "", "AZURE_SUBSCRIPTION_ID": ""}},
		{name: "read fails", readErr: errors.New("unavailable")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			environment := &recordingEnvironmentContextReader{values: tt.values, valuesErr: tt.readErr}
			account := &recordingTenantLookup{}
			resolved := resolveEnvContextWithClients(t.Context(), "staging", environment, account)
			assert.Equal(t, tt.wantProjectID, resolved.projectID)
			assert.Equal(t, tt.wantTenant, resolved.tenantID)
			assert.Zero(t, environment.currentCalls)
			assert.Zero(t, environment.getValueCalls)
			assert.Equal(t, 1, environment.getValuesCalls)
			assert.Equal(t, []string{"staging"}, environment.requestedEnvironments)
			if tt.wantTenant == "" {
				assert.Empty(t, account.subscriptions)
			} else {
				assert.Equal(t, []string{"persisted-sub"}, account.subscriptions)
			}
		})
	}
}

func TestResolveEnvContextUsesCurrentEnvironmentWhenUnspecified(t *testing.T) {
	t.Parallel()
	environment := &recordingEnvironmentContextReader{values: map[string]string{
		"AZURE_AI_PROJECT_ID": "current-project", "AZURE_SUBSCRIPTION_ID": "current-sub",
	}}
	account := &recordingTenantLookup{}
	resolved := resolveEnvContextWithClients(t.Context(), "", environment, account)
	assert.Equal(t, "current-project", resolved.projectID)
	assert.Equal(t, "tenant", resolved.tenantID)
	assert.Equal(t, 1, environment.currentCalls)
	assert.Equal(t, 2, environment.getValueCalls)
	assert.Zero(t, environment.getValuesCalls)
	assert.Equal(t, []string{"default", "default"}, environment.requestedEnvironments)
	assert.Equal(t, []string{"current-sub"}, account.subscriptions)
}

func TestResolveEnvContextStandaloneProcessFallback(t *testing.T) {
	t.Setenv("AZURE_AI_PROJECT_ID", "process-project")
	t.Setenv("AZURE_SUBSCRIPTION_ID", "process-sub")
	for _, tt := range []struct {
		name          string
		values        map[string]string
		wantProjectID string
		wantSubID     string
	}{
		{name: "neither value persisted", wantProjectID: "process-project", wantSubID: "process-sub"},
		{name: "project persisted", values: map[string]string{"AZURE_AI_PROJECT_ID": "persisted-project"},
			wantProjectID: "persisted-project", wantSubID: "process-sub"},
		{name: "subscription persisted", values: map[string]string{"AZURE_SUBSCRIPTION_ID": "persisted-sub"},
			wantProjectID: "process-project", wantSubID: "persisted-sub"},
		{name: "persisted values win", values: map[string]string{
			"AZURE_AI_PROJECT_ID": "persisted-project", "AZURE_SUBSCRIPTION_ID": "persisted-sub",
		}, wantProjectID: "persisted-project", wantSubID: "persisted-sub"},
		{name: "empty persisted values win", values: map[string]string{
			"AZURE_AI_PROJECT_ID": "", "AZURE_SUBSCRIPTION_ID": "",
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			environment := &recordingEnvironmentContextReader{values: tt.values}
			account := &recordingTenantLookup{}

			resolved := resolveEnvContextWithClients(t.Context(), "", environment, account)

			assert.Equal(t, tt.wantProjectID, resolved.projectID)
			assert.Equal(t, 1, environment.currentCalls)
			assert.Equal(t, 2, environment.getValueCalls)
			assert.Zero(t, environment.getValuesCalls)
			assert.Equal(t, []string{"default", "default"}, environment.requestedEnvironments)
			if tt.wantSubID == "" {
				assert.Empty(t, resolved.tenantID)
				assert.Empty(t, account.subscriptions)
			} else {
				assert.Equal(t, "tenant", resolved.tenantID)
				assert.Equal(t, []string{tt.wantSubID}, account.subscriptions)
			}
		})
	}
}

func TestResolveEnvContextStandaloneOptionalValues(t *testing.T) {
	t.Setenv("AZURE_AI_PROJECT_ID", "")
	t.Setenv("AZURE_SUBSCRIPTION_ID", "")
	for _, tt := range []struct {
		name          string
		values        map[string]string
		valueErrors   map[string]error
		readErr       error
		wantProjectID string
		wantSubID     string
	}{
		{name: "both missing"},
		{name: "project only", values: map[string]string{"AZURE_AI_PROJECT_ID": "current-project"},
			wantProjectID: "current-project"},
		{name: "subscription only", values: map[string]string{"AZURE_SUBSCRIPTION_ID": "current-sub"},
			wantSubID: "current-sub"},
		{name: "project read fails", values: map[string]string{
			"AZURE_AI_PROJECT_ID": "ignored-project", "AZURE_SUBSCRIPTION_ID": "current-sub",
		}, valueErrors: map[string]error{"AZURE_AI_PROJECT_ID": errors.New("unavailable")},
			wantSubID: "current-sub"},
		{name: "subscription read fails", values: map[string]string{
			"AZURE_AI_PROJECT_ID": "current-project", "AZURE_SUBSCRIPTION_ID": "ignored-sub",
		}, valueErrors: map[string]error{"AZURE_SUBSCRIPTION_ID": errors.New("unavailable")},
			wantProjectID: "current-project"},
		{name: "both reads fail", values: map[string]string{
			"AZURE_AI_PROJECT_ID": "ignored-project", "AZURE_SUBSCRIPTION_ID": "ignored-sub",
		}, readErr: errors.New("unavailable")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			environment := &recordingEnvironmentContextReader{
				values: tt.values, valueErrors: tt.valueErrors, valuesErr: tt.readErr,
			}
			account := &recordingTenantLookup{}

			resolved := resolveEnvContextWithClients(t.Context(), "", environment, account)

			assert.Equal(t, tt.wantProjectID, resolved.projectID)
			assert.Equal(t, 1, environment.currentCalls)
			assert.Equal(t, 2, environment.getValueCalls)
			assert.Zero(t, environment.getValuesCalls)
			assert.Equal(t, []string{"default", "default"}, environment.requestedEnvironments)
			if tt.wantSubID == "" {
				assert.Empty(t, resolved.tenantID)
				assert.Empty(t, account.subscriptions)
			} else {
				assert.Equal(t, "tenant", resolved.tenantID)
				assert.Equal(t, []string{tt.wantSubID}, account.subscriptions)
			}
		})
	}
}

func TestResolveEnvContextStandaloneWithoutCurrentEnvironment(t *testing.T) {
	t.Setenv("AZURE_AI_PROJECT_ID", "process-project")
	t.Setenv("AZURE_SUBSCRIPTION_ID", "process-sub")
	for _, tt := range []struct {
		name       string
		currentErr error
		noCurrent  bool
	}{
		{name: "current lookup fails", currentErr: errors.New("unavailable")},
		{name: "no current environment", noCurrent: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			environment := &recordingEnvironmentContextReader{currentErr: tt.currentErr, noCurrent: tt.noCurrent}
			account := &recordingTenantLookup{}

			resolved := resolveEnvContextWithClients(t.Context(), "", environment, account)

			assert.Equal(t, envContext{}, resolved)
			assert.Equal(t, 1, environment.currentCalls)
			assert.Zero(t, environment.getValueCalls)
			assert.Zero(t, environment.getValuesCalls)
			assert.Empty(t, environment.requestedEnvironments)
			assert.Empty(t, account.subscriptions)
		})
	}
}

func TestResolveEnvContextWithoutDaemon(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	t.Setenv("AZURE_AI_PROJECT_ID", "process-project")
	t.Setenv("AZURE_SUBSCRIPTION_ID", "process-sub")

	assert.Equal(t, envContext{}, resolveEnvContext(t.Context(), ""))
	assert.Equal(t, envContext{}, resolveEnvContext(t.Context(), "staging"))
}

type recordingEnvironmentContextReader struct {
	values                map[string]string
	valuesErr             error
	valueErrors           map[string]error
	currentErr            error
	noCurrent             bool
	currentCalls          int
	getValueCalls         int
	getValuesCalls        int
	requestedEnvironments []string
}

func (r *recordingEnvironmentContextReader) GetCurrent(
	context.Context,
	*azdext.EmptyRequest,
	...grpc.CallOption,
) (*azdext.EnvironmentResponse, error) {
	r.currentCalls++
	if r.currentErr != nil {
		return nil, r.currentErr
	}
	if r.noCurrent {
		return &azdext.EnvironmentResponse{}, nil
	}
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: "default"}}, nil
}

func (r *recordingEnvironmentContextReader) GetValue(
	_ context.Context,
	request *azdext.GetEnvRequest,
	_ ...grpc.CallOption,
) (*azdext.KeyValueResponse, error) {
	r.getValueCalls++
	r.requestedEnvironments = append(r.requestedEnvironments, request.GetEnvName())
	// Model daemon GetValue: missing persisted keys fall back to the process.
	value, exists := r.values[request.GetKey()]
	if !exists {
		value = os.Getenv(request.GetKey())
	}
	if err := r.valueErrors[request.GetKey()]; err != nil {
		return &azdext.KeyValueResponse{Value: value}, err
	}
	return &azdext.KeyValueResponse{Value: value}, r.valuesErr
}

func (r *recordingEnvironmentContextReader) GetValues(
	_ context.Context,
	request *azdext.GetEnvironmentRequest,
	_ ...grpc.CallOption,
) (*azdext.KeyValueListResponse, error) {
	r.getValuesCalls++
	r.requestedEnvironments = append(r.requestedEnvironments, request.GetName())
	if r.valuesErr != nil {
		return nil, r.valuesErr
	}
	response := &azdext.KeyValueListResponse{}
	for key, value := range r.values {
		response.KeyValues = append(response.KeyValues, &azdext.KeyValue{Key: key, Value: value})
	}
	return response, nil
}

type recordingTenantLookup struct {
	subscriptions []string
}

func (r *recordingTenantLookup) LookupTenant(
	_ context.Context,
	request *azdext.LookupTenantRequest,
	_ ...grpc.CallOption,
) (*azdext.LookupTenantResponse, error) {
	r.subscriptions = append(r.subscriptions, request.GetSubscriptionId())
	return &azdext.LookupTenantResponse{TenantId: "tenant"}, nil
}
