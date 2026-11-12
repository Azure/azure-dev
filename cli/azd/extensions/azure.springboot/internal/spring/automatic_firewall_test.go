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

func TestAutomaticFirewallDeletesOnlyVerifiedBroadBootstrapAndConfirmsAbsence(t *testing.T) {
	for _, scenario := range []string{
		"success", "missing-runtime", "changed-runtime", "stale-outbound", "unowned-bootstrap",
		"unknown-broad", "changed-bootstrap", "delete-denied", "absence-unconfirmed",
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
			deletions, reads, removed := 0, 0, false
			client, err := armpostgresqlflexibleservers.NewFirewallRulesClient(
				identity.subscriptionID, postgresPolicyCredential{}, &arm.ClientOptions{
					ClientOptions: policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1},
						Transport: postgresPolicyTransport(func(request *http.Request) (*http.Response, error) {
							status, body := http.StatusOK, `{}`
							if request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/firewallRules") {
								data, err := json.Marshal(map[string]any{"value": rules})
								require.NoError(t, err)
								body = string(data)
							} else {
								require.True(t, strings.HasSuffix(request.URL.Path, "/firewallRules/"+authoredBootstrapRule))
								switch request.Method {
								case http.MethodGet:
									reads++
									if removed && scenario != "absence-unconfirmed" {
										status, body = http.StatusNotFound, `{"error":{"code":"ResourceNotFound"}}`
									} else {
										data, err := json.Marshal(bootstrap)
										require.NoError(t, err)
										body = string(data)
									}
								case http.MethodDelete:
									deletions++
									removed = true
									status = http.StatusNoContent
									if scenario == "delete-denied" {
										status, body = http.StatusForbidden, `{"error":{"code":"AuthorizationFailed"}}`
									}
								default:
									t.Fatalf("unexpected firewall operation: %s", request.Method)
								}
							}
							return &http.Response{StatusCode: status,
								Header: http.Header{"Content-Type": []string{"application/json"}},
								Body:   io.NopCloser(strings.NewReader(body)), Request: request}, nil
						}),
					},
				},
			)
			require.NoError(t, err)
			cleanup, prepareErr := prepareAutomaticFirewall(t.Context(), client, identity, binding)
			switch scenario {
			case "success", "changed-bootstrap", "delete-denied", "absence-unconfirmed":
				require.NoError(t, prepareErr)
				require.Equal(t, []string{"1.2.3.4"}, binding.outboundIPs)
			default:
				require.Error(t, prepareErr)
				require.Empty(t, binding.outboundIPs)
			}
			if scenario == "unowned-bootstrap" {
				require.Nil(t, cleanup)
				require.Zero(t, deletions)
				return
			}
			require.NotNil(t, cleanup)
			cleanupErr := cleanup(t.Context())
			switch scenario {
			case "changed-bootstrap":
				require.Error(t, cleanupErr)
				require.Zero(t, deletions)
			case "delete-denied", "absence-unconfirmed":
				require.Error(t, cleanupErr)
				require.Equal(t, 1, deletions)
			default:
				require.NoError(t, cleanupErr)
				require.Equal(t, 1, deletions)
				require.Equal(t, 2, reads)
			}
		})
	}
}

func TestAutomaticPostProvisionCleanupRunsBeforeCompletionAndSurvivesHandlerFailureOrCancellation(t *testing.T) {
	scenarios := []string{"success", "repeated-success", "inference-failure", "unresolved-final",
		"cleanup-failure", "cancellation"}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			project, service := automaticPostgresProject(t)
			binding, _ := automaticFirewallBinding()
			root := "/subscriptions/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/resourceGroups/rg/providers/"
			identityID := root + "Microsoft.ManagedIdentity/userAssignedIdentities/app"
			binding.values[postgresHostEnv] = "actual.example"
			binding.values[serviceIdentityIDEnv] = identityID
			binding.values[serviceIdentityClientIDEnv] = testSpringClientID
			binding.values[databaseAdministratorNameEnv] = "developer@example.test"
			binding.values[databaseAdministratorObjectIDEnv] = testSpringClientID
			binding.values[databaseUserAccessTenantIDEnv] = testSpringClientID
			if scenario == "repeated-success" || scenario == "cleanup-failure" {
				binding.values[databaseAccessConfiguredEnv] = automaticMIProvisioningVersion
			}
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
						require.Equal(t, []string{databaseAccessConfiguredEnv}, store.writes)
						require.Equal(t, "pending", store.data[databaseAccessConfiguredEnv])
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
			if scenario == "inference-failure" || scenario == "unresolved-final" {
				require.Zero(t, cleanups, "invalid datasource selection must not begin network preparation")
			} else {
				require.Equal(t, 1, cleanups)
			}
			if scenario == "success" || scenario == "repeated-success" {
				require.NoError(t, err)
				require.Equal(t, 1, sqlCalls)
				require.Equal(t, automaticMIProvisioningVersion, store.data[databaseAccessConfiguredEnv])
				store.writes = nil
				runtime.prepareNetwork = func(
					context.Context, *deploymentBinding,
				) (func(context.Context) error, error) {
					return nil, nil
				}
				runtime.configureAccess = func(context.Context, string, resolvedDatabase, map[string]string) error {
					t.Fatal("automatic managed-identity deploy must not run administrator SQL")
					return nil
				}
				runtime.recoverAccess = func(context.Context, string, resolvedDatabase, map[string]string) error {
					t.Fatal("deploy must not recover or mutate temporary administrator registrations")
					return nil
				}
				deployArgs := &azdext.ServiceEventArgs{Project: project, Service: service}
				require.NoError(t, runtime.PreDeploy(t.Context(), deployArgs))
				require.Empty(t, store.writes)
				for _, incomplete := range []string{"pending", "", databaseAccessPolicyVersion} {
					store.data[databaseAccessConfiguredEnv] = incomplete
					require.ErrorContains(t, runtime.PreDeploy(t.Context(), deployArgs), "access is incomplete")
				}
			} else {
				require.Error(t, err)
				if scenario == "inference-failure" || scenario == "unresolved-final" {
					require.Empty(t, store.writes)
					require.Zero(t, sqlCalls)
				} else {
					require.Equal(t, []string{databaseAccessConfiguredEnv}, store.writes)
					require.Equal(t, "pending", store.data[databaseAccessConfiguredEnv])
				}
				if scenario == "cleanup-failure" {
					require.ErrorIs(t, err, cleanupFailure)
				}
			}
		})
	}
}
