// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package postgres

import (
	"azurespringboot/internal/deploy"
	"azurespringboot/internal/springmodel"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
	"github.com/stretchr/testify/require"
)

func automaticFirewallBinding() (*deploy.DeploymentBinding, *firewallIdentity) {
	root := "/subscriptions/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/resourceGroups/rg/providers/"
	serverID := root + "Microsoft.DBforPostgreSQL/flexibleServers/pg"
	appID := root + "Microsoft.App/containerApps/app"
	bootstrapID := serverID + "/firewallRules/" + authoredBootstrapRule
	runtimeID := serverID + "/firewallRules/aca-1-2-3-4"
	return &deploy.DeploymentBinding{
			Automatic: true,
			Values: map[string]string{
				springmodel.PostgresServerIDEnv: serverID, springmodel.ContainerAppIDEnv: appID,
				"DATABASE_OUTBOUND_IPS": `["1.2.3.4"]`,
			},
			Outputs: deploy.CurrentDeploymentOutputs{
				"DATABASE_OUTBOUND_IPS": {Type: "string", Value: `["1.2.3.4"]`},
			},
			Resources: map[string]bool{
				strings.ToLower(serverID): true, strings.ToLower(appID): true,
				strings.ToLower(bootstrapID): true, strings.ToLower(runtimeID): true,
			},
			App: &armappcontainers.ContainerApp{ID: new(appID), Properties: &armappcontainers.ContainerAppProperties{
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
			serverID := binding.Values[springmodel.PostgresServerIDEnv]
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
				binding.App.Properties.OutboundIPAddresses = []*string{new("1.2.3.5")}
			case "unowned-bootstrap":
				delete(binding.Resources, strings.ToLower(*bootstrap.ID))
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
				require.Equal(t, []string{"1.2.3.4"}, binding.OutboundIPs)
			default:
				require.Error(t, prepareErr)
				require.Empty(t, binding.OutboundIPs)
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

func TestBroadFirewallRecognitionDoesNotTreatAzureServicesRuleAsPublicIPv4(t *testing.T) {
	for _, test := range []struct {
		start, end string
		want       bool
	}{
		{start: "0.0.0.0", end: "255.255.255.255", want: true},
		{start: "0.0.0.0", end: "0.0.0.0"},
		{start: "1.0.0.0", end: "255.255.255.255"},
		{start: "203.0.113.1", end: "203.0.113.1"},
	} {
		properties := &armpostgresqlflexibleservers.FirewallRuleProperties{
			StartIPAddress: new(test.start), EndIPAddress: new(test.end),
		}
		require.Equal(t, test.want, isAllPublicIPv4Rule(properties))
	}
	require.False(t, isAllPublicIPv4Rule(nil))
}
