// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	provisioningtest "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/test"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
)

// offlineAuth stands in for "the user is logged in" so the login guard passes without contacting Entra.
type offlineAuth struct{}

type offlineCredential struct{}

func (offlineCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "offline", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func (offlineAuth) Cloud() *cloud.Cloud {
	c, _ := cloud.NewCloud(&cloud.Config{})
	return c
}

func (offlineAuth) Mode() (auth.AuthSource, error) { return auth.AzdBuiltIn, nil }

func (offlineAuth) CredentialForCurrentUser(
	context.Context, *auth.CredentialForCurrentUserOptions,
) (azcore.TokenCredential, error) {
	return offlineCredential{}, nil
}

// probeProvider is the provisioning test provider plus a write through the env manager it was handed.
type probeProvider struct {
	provisioning.Provider
	manager environment.Manager
	env     environment.ScopedEnvironment
}

func (p *probeProvider) Deploy(ctx context.Context) (*provisioning.DeployResult, error) {
	p.env.DotenvSet("WRITTEN_BY_PROVIDER", "true")
	if err := p.manager.Save(ctx, p.env.BackingEnv()); err != nil {
		return nil, err
	}

	return p.Provider.Deploy(ctx)
}

// Runs the real "azd provision" command, in process, against the real container, middleware, graph engine and
// environment manager. Only login, HTTP and the provider are faked.
func Test_ProvisionCommand_ProviderUsesRealEnvManager(t *testing.T) {
	container, root := newOfflineRoot(t)
	require.NoError(t, os.WriteFile("azure.yaml", []byte(`name: test
infra:
  provider: test
  layers:
    - name: a
      path: infra/a
    - name: b
      path: infra/b
`), 0o600))

	// Two layers: a single layer runs sequentially with the shared manager, which has no layer-local persistence.
	// Scoped, because command scopes re-register scoped bindings and would shadow a root-level instance.
	container.MustRegisterScoped(func() middleware.CurrentUserAuthManager { return offlineAuth{} })
	container.MustRegisterNamedTransient(string(provisioning.Test), func(
		manager environment.Manager,
		scoped environment.ScopedEnvironment,
		console input.Console,
		prompter prompt.Prompter,
	) provisioning.Provider {
		return &probeProvider{
			Provider: provisioningtest.NewTestProvider(manager, scoped, console, prompter),
			manager:  manager,
			env:      scoped,
		}
	})

	runInFakeCommand(t, container, func(scope *ioc.NestedContainer) {
		createDevEnvironment(t, scope)
	})

	root.SetArgs([]string{"provision", "--no-prompt", "-e", "dev"})
	require.NoError(t, root.ExecuteContext(t.Context()))

	runInFakeCommand(t, container, func(scope *ioc.NestedContainer) {
		var manager environment.Manager
		require.NoError(t, scope.Resolve(&manager))
		persisted, err := manager.Get(t.Context(), "dev")
		require.NoError(t, err)

		require.Equal(t, "true", persisted.Getenv("WRITTEN_BY_PROVIDER"))
	})
}
