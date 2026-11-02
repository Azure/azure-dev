// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func automaticFirewallBinding() (*deploymentBinding, *firewallIdentity) {
	root := "/subscriptions/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/resourceGroups/rg/providers/"
	serverID := root + "Microsoft.DBforPostgreSQL/flexibleServers/pg"
	appID := root + "Microsoft.App/containerApps/app"
	bootstrapID := serverID + "/firewallRules/" + authoredBootstrapRule
	runtimeID := serverID + "/firewallRules/aca-1-2-3-4"
	return &deploymentBinding{
		automatic: true,
		values: map[string]string{
			postgresServerIDEnv: serverID, containerAppIDEnv: appID, "DATABASE_OUTBOUND_IPS": `["1.2.3.4"]`,
		},
		outputs: currentDeploymentOutputs{
			"DATABASE_OUTBOUND_IPS": {Type: "string", Value: `["1.2.3.4"]`},
		},
		resources: map[string]bool{
			strings.ToLower(serverID): true, strings.ToLower(appID): true,
			strings.ToLower(bootstrapID): true, strings.ToLower(runtimeID): true,
		},
		app: &armappcontainers.ContainerApp{ID: new(appID), Properties: &armappcontainers.ContainerAppProperties{
			OutboundIPAddresses: []*string{new("1.2.3.4")},
		}},
	}, &firewallIdentity{
		subscriptionID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		resourceGroup:  "rg", postgresServerName: "pg", containerAppName: "app",
	}
}

func TestAutomaticFirewallRetainsOnlyVerifiedOwnedBroadBootstrap(t *testing.T) {
	for _, scenario := range []string{
		"success", "missing-runtime", "changed-runtime", "stale-outbound", "unowned-bootstrap",
		"unknown-broad", "changed-bootstrap",
	} {
		t.Run(scenario, func(t *testing.T) {
			binding, identity := automaticFirewallBinding()
			serverID := binding.values[postgresServerIDEnv]
			rule := func(name, start, end string) *armpostgresqlflexibleservers.FirewallRule {
				return &armpostgresqlflexibleservers.FirewallRule{
					ID: new(serverID + "/firewallRules/" + name), Name: new(name),
					Properties: &armpostgresqlflexibleservers.FirewallRuleProperties{
						StartIPAddress: new(start), EndIPAddress: new(end),
					},
				}
			}
			bootstrap := rule(authoredBootstrapRule, "0.0.0.0", "255.255.255.255")
			rules := []*armpostgresqlflexibleservers.FirewallRule{
				bootstrap, rule("aca-1-2-3-4", "1.2.3.4", "1.2.3.4"), rule("foreign", "5.6.7.8", "5.6.7.8"),
			}
			switch scenario {
			case "missing-runtime":
				rules = append(rules[:1], rules[2:]...)
			case "changed-runtime":
				rules[1].Properties.EndIPAddress = new("1.2.3.5")
			case "stale-outbound":
				binding.app.Properties.OutboundIPAddresses = []*string{new("1.2.3.5")}
			case "unowned-bootstrap":
				delete(binding.resources, strings.ToLower(*bootstrap.ID))
			case "unknown-broad":
				rules = append(rules, rule("unknown", "0.0.0.0", "255.255.255.255"))
			case "changed-bootstrap":
				bootstrap.Properties.EndIPAddress = new("1.2.3.4")
			}
			mutations := 0
			client, err := armpostgresqlflexibleservers.NewFirewallRulesClient(
				identity.subscriptionID, postgresPolicyCredential{}, &arm.ClientOptions{
					ClientOptions: policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1},
						Transport: postgresPolicyTransport(func(request *http.Request) (*http.Response, error) {
							if request.Method != http.MethodGet ||
								!strings.HasSuffix(request.URL.Path, "/firewallRules") {
								mutations++
								t.Fatalf("unexpected firewall operation: %s %s", request.Method, request.URL.Path)
							}
							data, err := json.Marshal(map[string]any{"value": rules})
							require.NoError(t, err)
							return &http.Response{StatusCode: http.StatusOK,
								Header: http.Header{"Content-Type": []string{"application/json"}},
								Body:   io.NopCloser(strings.NewReader(string(data))), Request: request}, nil
						}),
					},
				},
			)
			require.NoError(t, err)
			cleanup, prepareErr := prepareAutomaticFirewall(t.Context(), client, identity, binding)
			if scenario == "success" {
				require.NoError(t, prepareErr)
			} else {
				require.Error(t, prepareErr)
			}
			require.Nil(t, cleanup)
			require.Zero(t, mutations)
		})
	}
}

func TestAutomaticPostProvisionCleanupPrecedesSuccessAndSurvivesFailureOrCancellation(t *testing.T) {
	scenarios := []string{"success", "inference-failure", "unresolved-final", "cleanup-failure", "cancellation"}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			project, _ := automaticPostgresProject(t)
			binding, _ := automaticFirewallBinding()
			root := "/subscriptions/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/resourceGroups/rg/providers/"
			identityID := root + "Microsoft.ManagedIdentity/userAssignedIdentities/app"
			binding.values[postgresHostEnv] = "actual.example"
			binding.values[serviceIdentityIDEnv] = identityID
			binding.values[serviceIdentityClientIDEnv] = testSpringClientID
			binding.values[databaseAdministratorNameEnv] = "developer@example.test"
			binding.values[databaseAdministratorObjectIDEnv] = testSpringClientID
			binding.values[databaseUserAccessTenantIDEnv] = testSpringClientID
			binding.resources[strings.ToLower(identityID)] = true
			binding.resources[strings.ToLower(binding.values[postgresServerIDEnv]+"/databases/petclinic")] = true
			binding.app.Identity = &armappcontainers.ManagedServiceIdentity{
				UserAssignedIdentities: map[string]*armappcontainers.UserAssignedIdentity{
					identityID: {ClientID: new(testSpringClientID)},
				},
			}
			url := "jdbc:postgresql://actual.example:5432/petclinic"
			if scenario == "inference-failure" {
				url = "invalid"
			} else if scenario == "unresolved-final" {
				url = "${UNRESOLVED_DATASOURCE}"
			}
			binding.app.Properties.Template = &armappcontainers.Template{Containers: []*armappcontainers.Container{{
				Env: []*armappcontainers.EnvironmentVar{
					{Name: new("POSTGRES_URL"), Value: new(url)},
					{Name: new("AZURE_CLIENT_ID"), Value: new(testSpringClientID)},
				},
			}}}
			store := &fakeEnvironmentStore{name: "dev", data: binding.values}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cleanups, sqlCalls := 0, 0
			cleanupFailure := errors.New("cleanup failed")
			runtime := &Runtime{
				environments: store, outputs: &fakeDeploymentOutputVerifier{bound: binding},
				prepareNetwork: func(context.Context, *deploymentBinding) (func(context.Context) error, error) {
					return func(cleanupCtx context.Context) error {
						cleanups++
						require.NoError(t, cleanupCtx.Err())
						_, bounded := cleanupCtx.Deadline()
						require.True(t, bounded)
						require.Empty(t, store.writes)
						if scenario == "cleanup-failure" {
							return cleanupFailure
						}
						return nil
					}, nil
				},
				configureAccess: func(context.Context, string, resolvedDatabase, map[string]string) error {
					sqlCalls++
					if scenario == "cancellation" {
						cancel()
						return ctx.Err()
					}
					return nil
				},
				reconcile: func(context.Context, map[string]string) error { return nil },
			}
			err := runtime.PostProvision(ctx, &azdext.ProjectEventArgs{Project: project})
			require.Equal(t, 1, cleanups)
			if scenario == "success" {
				require.NoError(t, err)
				require.Equal(t, 1, sqlCalls)
				require.Equal(t, databaseAccessPolicyVersion, store.data[databaseAccessConfiguredEnv])
			} else {
				require.Error(t, err)
				require.Empty(t, store.writes)
				if scenario == "inference-failure" || scenario == "unresolved-final" {
					require.Zero(t, sqlCalls)
				}
				if scenario == "cleanup-failure" {
					require.ErrorIs(t, err, cleanupFailure)
				}
			}
		})
	}
}
