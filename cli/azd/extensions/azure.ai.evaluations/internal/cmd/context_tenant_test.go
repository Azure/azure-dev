// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"azureaieval/internal/foundry/projectctx"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type fakeEnvValues struct {
	values map[string]string
	err    error
	asked  []*azdext.GetEnvRequest
}

func (f *fakeEnvValues) GetValue(
	_ context.Context, in *azdext.GetEnvRequest, _ ...grpc.CallOption,
) (*azdext.KeyValueResponse, error) {
	f.asked = append(f.asked, in)
	if f.err != nil {
		return nil, f.err
	}
	return &azdext.KeyValueResponse{Key: in.Key, Value: f.values[in.EnvName+"/"+in.Key]}, nil
}

type fakeTenantLookup struct {
	tenants map[string]string
	err     error
	asked   []string
}

func (f *fakeTenantLookup) LookupTenant(
	_ context.Context, in *azdext.LookupTenantRequest, _ ...grpc.CallOption,
) (*azdext.LookupTenantResponse, error) {
	f.asked = append(f.asked, in.SubscriptionId)
	if f.err != nil {
		return nil, f.err
	}
	return &azdext.LookupTenantResponse{TenantId: f.tenants[in.SubscriptionId]}, nil
}

// The credential has to ask for the tenant of the environment's subscription.
// azd would pick it itself inside the project, but azidentity runs it from a
// fixed directory where no environment is found, so the home tenant was used
// and a Foundry project in another tenant rejected every token.
func TestResolveAzdTenantIDUsesTheEnvironmentSubscriptionTenant(t *testing.T) {
	env := &fakeEnvValues{values: map[string]string{"dev/AZURE_SUBSCRIPTION_ID": "sub-1"}}
	account := &fakeTenantLookup{tenants: map[string]string{"sub-1": "resource-tenant"}}

	tenant := resolveAzdTenantID(t.Context(), env, account, "dev")

	assert.Equal(t, "resource-tenant", tenant)
	require.Len(t, env.asked, 1)
	assert.Equal(t, "dev", env.asked[0].EnvName, "reads the selected environment, not the default one")
	assert.Equal(t, azdSubscriptionEnvKey, env.asked[0].Key)
	assert.Equal(t, []string{"sub-1"}, account.asked)
}

func TestResolveAzdTenantIDWithoutAnEnvironmentKeepsTheDefault(t *testing.T) {
	env := &fakeEnvValues{}
	account := &fakeTenantLookup{}

	assert.Empty(t, resolveAzdTenantID(t.Context(), env, account, ""))
	assert.Empty(t, env.asked, "standalone commands have no environment to read")
	assert.Empty(t, account.asked)
}

func TestResolveAzdTenantIDWithoutASubscriptionKeepsTheDefault(t *testing.T) {
	env := &fakeEnvValues{values: map[string]string{}}
	account := &fakeTenantLookup{}

	assert.Empty(t, resolveAzdTenantID(t.Context(), env, account, "dev"))
	assert.Empty(t, account.asked, "no subscription, nothing to look up")
}

// A lookup that fails must not fail the command: the home tenant is what was
// used before, and it is right for most users.
func TestResolveAzdTenantIDFallsBackWhenTheHostCannotAnswer(t *testing.T) {
	broken := errors.New("rpc error: code = Unavailable")

	assert.Empty(t, resolveAzdTenantID(t.Context(), &fakeEnvValues{err: broken}, &fakeTenantLookup{}, "dev"))

	env := &fakeEnvValues{values: map[string]string{"dev/AZURE_SUBSCRIPTION_ID": "sub-1"}}
	assert.Empty(t, resolveAzdTenantID(t.Context(), env, &fakeTenantLookup{err: broken}, "dev"))
}

// Guards the other half of the wiring: the resolved tenant has to reach
// `azd auth token`. A stand-in azd on PATH records the arguments it gets.
func TestNewAzdTokenCredentialPassesTheTenantToAzd(t *testing.T) {
	argsFile := installAzdStandIn(t)

	cases := map[string]struct {
		tenant string
		want   string
	}{
		"resolved tenant": {
			tenant: "11111111-1111-1111-1111-111111111111",
			want:   "--tenant-id 11111111-1111-1111-1111-111111111111",
		},
		"no tenant": {tenant: "", want: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cred, err := newAzdTokenCredential(tc.tenant)
			require.NoError(t, err)

			_, err = cred.GetToken(t.Context(), policy.TokenRequestOptions{
				Scopes: []string{"https://ai.azure.com/.default"},
			})
			require.NoError(t, err)

			args, err := os.ReadFile(argsFile)
			require.NoError(t, err)
			if tc.want == "" {
				assert.NotContains(t, string(args), "--tenant-id")
			} else {
				assert.Contains(t, strings.TrimSpace(string(args)), tc.want)
			}
		})
	}
}

type fakeDotenv struct {
	values map[string]map[string]string
	err    error
}

func (f *fakeDotenv) GetValues(
	_ context.Context, in *azdext.GetEnvironmentRequest, _ ...grpc.CallOption,
) (*azdext.KeyValueListResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	resp := &azdext.KeyValueListResponse{}
	for k, v := range f.values[in.Name] {
		resp.KeyValues = append(resp.KeyValues, &azdext.KeyValue{Key: k, Value: v})
	}
	return resp, nil
}

const tenantTestEndpoint = "https://example.services.ai.azure.com/api/projects/p"

// Only an endpoint persisted in the azd environment is known to belong to that
// environment's subscription. One from --project-endpoint, global config or
// the host environment can sit in another tenant (often the home tenant), so
// those keep azd's default rather than being forced onto the environment's.
func TestTenantEnvNameOnlyForEnvironmentSourcedEndpoints(t *testing.T) {
	persisted := &fakeDotenv{values: map[string]map[string]string{
		"dev": {projectEndpointEnvKey: tenantTestEndpoint + "/"},
	}}
	cases := []struct {
		source projectctx.EndpointSource
		want   string
	}{
		{projectctx.SourceAzdEnv, "dev"},
		{projectctx.SourceFlag, ""},
		{projectctx.SourceGlobalConfig, ""},
		{projectctx.SourceFoundryEnv, ""},
	}
	for _, tc := range cases {
		t.Run(string(tc.source), func(t *testing.T) {
			resolved := &projectctx.Resolved{
				Endpoint:   tenantTestEndpoint,
				Source:     tc.source,
				AzdEnvName: "dev",
			}
			assert.Equal(t, tc.want, tenantEnvName(t.Context(), persisted, resolved))
		})
	}
	assert.Empty(t, tenantEnvName(t.Context(), persisted, nil))
}

// azd's GetValue falls back to the process environment, so an endpoint
// exported in the shell resolves as SourceAzdEnv even when the environment
// never stored it. That endpoint can sit in the home tenant, so it must not
// get the environment subscription's tenant forced onto it.
func TestTenantEnvNameRequiresTheEndpointPersistedInTheEnvironment(t *testing.T) {
	resolved := &projectctx.Resolved{
		Endpoint:   tenantTestEndpoint,
		Source:     projectctx.SourceAzdEnv,
		AzdEnvName: "dev",
	}

	cases := map[string]struct {
		env  *fakeDotenv
		want string
	}{
		"persisted under FOUNDRY_PROJECT_ENDPOINT": {
			env:  &fakeDotenv{values: map[string]map[string]string{"dev": {projectEndpointEnvKey: tenantTestEndpoint}}},
			want: "dev",
		},
		"persisted under AZURE_AI_PROJECT_ENDPOINT": {
			env: &fakeDotenv{values: map[string]map[string]string{
				"dev": {azureAIProjectEndpointEnvKey: tenantTestEndpoint},
			}},
			want: "dev",
		},
		"only in the process environment": {
			env:  &fakeDotenv{values: map[string]map[string]string{"dev": {"AZURE_SUBSCRIPTION_ID": "sub-1"}}},
			want: "",
		},
		"environment stores a different endpoint": {
			env: &fakeDotenv{values: map[string]map[string]string{
				"dev": {azureAIProjectEndpointEnvKey: "https://other.services.ai.azure.com/api/projects/q"},
			}},
			want: "",
		},
		"values cannot be read": {
			env:  &fakeDotenv{err: errors.New("rpc error: code = Unavailable")},
			want: "",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, tenantEnvName(t.Context(), tc.env, resolved))
		})
	}
}
