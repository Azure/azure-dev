// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script as the azd stand-in")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := "#!/bin/sh\n" +
		"echo \"$@\" > '" + argsFile + "'\n" +
		"echo '{\"token\":\"t\",\"expiresOn\":\"2099-01-01T00:00:00Z\"}'\n"
	// The stand-in has to be executable.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "azd"), []byte(script), 0o700)) //nolint:gosec
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

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
