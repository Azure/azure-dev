// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package accesshost

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/shared/foundry/access"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

const endpoint = "https://account.services.ai.azure.com/api/projects/project"

type testEnvironment struct{ seen []string }

func (*testEnvironment) CurrentName(context.Context) (string, error) { return "default", nil }
func (e *testEnvironment) Value(_ context.Context, name, key string) (string, error) {
	e.seen = append(e.seen, name)
	values := map[string]string{
		"AZURE_AI_PROJECT_ID": "project-resource-id", "FOUNDRY_PROJECT_ENDPOINT": endpoint,
	}
	return values[key], nil
}

type testEnsurer struct {
	t      *testing.T
	policy access.GrantPolicy
	err    error
}

func (e testEnsurer) Ensure(_ context.Context, request access.Request) (access.Result, error) {
	require.Equal(e.t, e.policy, request.Policy)
	require.Equal(e.t, endpoint, request.Target.Endpoint)
	require.Equal(e.t, "toolboxes.read", request.Operation)
	return access.Result{State: access.Allowed}, e.err
}

func TestPreparePolicyAndEnvironment(t *testing.T) {
	for _, auto := range []bool{false, true} {
		env := &testEnvironment{}
		want := access.CheckOnly
		if auto {
			want = access.GrantFoundryUser
		}
		_, err := prepare(t.Context(), env, PrepareOptions{
			Endpoint: endpoint, EnvironmentName: "staging", Collection: "toolboxes", AutoGrant: auto,
		}, func() (ensurer, error) { return testEnsurer{t: t, policy: want}, nil })
		require.NoError(t, err)
		for _, name := range env.seen {
			require.Equal(t, "staging", name)
		}
	}
}

func TestPrepareTranslatesWarningsWithoutLeakingCauses(t *testing.T) {
	shared := &access.Error{Kind: access.Identity, Code: access.IdentityUnavailable,
		Message: "identity unavailable", Suggestion: "verify login", Cause: errors.New("sig=secret")}
	var warning string
	_, err := prepare(t.Context(), &testEnvironment{}, PrepareOptions{
		Endpoint: endpoint, Collection: "toolboxes", AutoGrant: true,
		Warn: func(message string) { warning = message },
	}, func() (ensurer, error) {
		return testEnsurer{t: t, policy: access.GrantFoundryUser, err: shared}, nil
	})
	require.NoError(t, err, "access failures must remain warning-only")
	require.Contains(t, warning, "verify login")
	require.NotContains(t, warning, "secret")
	local, ok := errors.AsType[*azdext.LocalError](translate(shared))
	require.True(t, ok)
	require.Equal(t, string(access.IdentityUnavailable), local.Code)
	require.Equal(t, azdext.LocalErrorCategoryUser, local.Category)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = prepare(ctx, &testEnvironment{}, PrepareOptions{Collection: "toolboxes"}, func() (ensurer, error) {
		t.Fatal("cancelled commands must not construct clients")
		return nil, nil
	})
	require.ErrorIs(t, err, context.Canceled)
}

type testAccount struct {
	azdext.AccountServiceClient
	principalType azdext.PrincipalType
}

func (a testAccount) GetCurrentPrincipal(
	_ context.Context, request *azdext.GetCurrentPrincipalRequest, _ ...grpc.CallOption,
) (*azdext.GetCurrentPrincipalResponse, error) {
	if request.SubscriptionId != "subscription" {
		return nil, errors.New("wrong subscription")
	}
	return &azdext.GetCurrentPrincipalResponse{ObjectId: "resource-tenant-object-id", PrincipalType: a.principalType}, nil
}

func (testAccount) LookupTenant(
	context.Context, *azdext.LookupTenantRequest, ...grpc.CallOption,
) (*azdext.LookupTenantResponse, error) {
	return &azdext.LookupTenantResponse{TenantId: "user-access-tenant"}, nil
}

func TestHostIdentityAdapter(t *testing.T) {
	for _, tt := range []struct {
		hostType azdext.PrincipalType
		want     string
	}{
		{azdext.PrincipalType_PRINCIPAL_TYPE_USER, "User"},
		{azdext.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL, "ServicePrincipal"},
	} {
		adapter := identity{testAccount{principalType: tt.hostType}}
		principal, err := adapter.ResolvePrincipal(t.Context(), "subscription")
		require.NoError(t, err)
		require.Equal(t, access.Principal{ObjectID: "resource-tenant-object-id", Type: tt.want}, principal)
		tenant, err := adapter.ResolveTenant(t.Context(), "subscription")
		require.NoError(t, err)
		require.Equal(t, "user-access-tenant", tenant)
	}
}

func TestProcessSkipSetting(t *testing.T) {
	t.Setenv("AZD_AGENT_SKIP_ROLE_ASSIGNMENTS", "true")
	var credential azcore.TokenCredential
	credential, err := Prepare(t.Context(), PrepareOptions{Collection: "toolboxes", AutoGrant: true})
	require.NoError(t, err)
	require.Nil(t, credential, "process opt-out must bypass host and cloud calls")
}
