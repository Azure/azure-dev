// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"azure.ai.projects/internal/onboarding"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type lifecycleJournal struct{ data []byte }

func (j *lifecycleJournal) Load(context.Context) (onboarding.PreparationState, error) {
	var state onboarding.PreparationState
	if len(j.data) == 0 {
		return state, nil
	}
	return state, json.Unmarshal(j.data, &state)
}

func (j *lifecycleJournal) Save(_ context.Context, state onboarding.PreparationState) error {
	data, err := json.Marshal(state)
	if err == nil {
		j.data = data
	}
	return err
}

type lifecycleNetwork struct{}

func (lifecycleNetwork) VerifyPreparedNetwork(
	context.Context, onboarding.Plan, onboarding.Prepared,
) (onboarding.Report, error) {
	return onboarding.Report{{ID: "R06", Status: onboarding.Pass, Mandatory: true, Observed: "offline network fixture"}}, nil
}

func contractLifecycleResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), Request: request,
	}
}

func lifecycleObject(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	require.True(t, ok, "expected an object, got %T", value)
	return object
}

func TestBYOSelectedOperationsFollowEffectiveOnDiskInputs(t *testing.T) {
	p := byoContractProvider(t)
	const principal = "00000000-0000-0000-0000-000000000044"
	p.onDiskSource = &templateSource{
		mode: templateModeBicep, armTemplate: p.armTemplate, sourcePath: "offline-main.bicep",
		parameters: map[string]any{
			"principalId":   map[string]any{"value": principal},
			"principalType": map[string]any{"value": "ServicePrincipal"},
			"deployments":   map[string]any{"value": []any{map[string]any{"name": "selected-model"}}},
		},
	}
	var checked bool
	p.byoPipeline = &onboarding.Pipeline{
		Resources: byoResourceContract(func(context.Context, onboarding.Plan) (onboarding.Facts, onboarding.Report, error) {
			return onboarding.Facts{}, nil, nil
		}),
		Access: byoAccessContract(func(_ context.Context, plan onboarding.Plan, _ onboarding.Facts) (onboarding.Report, error) {
			checked = true
			var models, connections, developer bool
			for _, op := range plan.Operations {
				models = models || op.Action == "Microsoft.CognitiveServices/accounts/deployments/write"
				connections = connections || op.Action == "Microsoft.CognitiveServices/accounts/projects/connections/write"
				if op.RoleID == "a97b65f3-24c7-4388-baec-2e87135dc908" {
					developer = true
					assert.Equal(t, principal, op.PrincipalID)
					assert.Equal(t, "ServicePrincipal", op.PrincipalType)
				}
			}
			assert.True(t, models)
			assert.True(t, connections)
			assert.False(t, developer, "the standard template no longer performs a developer grant")
			return nil, nil
		}),
		Prepare: byoPrepareContract(func(_ context.Context, plan onboarding.Plan, _ onboarding.Facts) (
			onboarding.Prepared, error,
		) {
			require.True(t, checked)
			return byoContractPrepared(plan), nil
		}),
	}
	require.NoError(t, p.prepareBYO(t.Context(), func(string) {}, false))
}

func TestBYOPreparedGroupTagsProtectOwnershipAndExplicitChoices(t *testing.T) {
	for _, test := range []struct {
		name     string
		observed map[string]string
		explicit map[string]string
		want     map[string]string
		conflict bool
	}{
		{
			name: "explicit case-insensitive customer change",
			observed: map[string]string{
				"customer-billing": "old", "untouched": "preserve", "azd-byoaks-owner": "owned",
			},
			explicit: map[string]string{"Customer-Billing": "new"},
			want: map[string]string{
				"Customer-Billing": "new", "untouched": "preserve", "azd-byoaks-owner": "owned",
			},
		},
		{
			name:     "same owner alias preserves original key",
			observed: map[string]string{"azd-byoaks-owner": "owned"},
			explicit: map[string]string{"AZD-BYOAKS-OWNER": "owned"},
			want:     map[string]string{"azd-byoaks-owner": "owned"},
		},
		{
			name: "changed ownership", observed: map[string]string{"azd-byoaks-owner": "owned"},
			explicit: map[string]string{"azd-byoaks-owner": "other"}, conflict: true,
		},
		{
			name: "changed case-insensitive ownership", observed: map[string]string{"azd-byoaks-owner": "owned"},
			explicit: map[string]string{"AZD-BYOAKS-OWNER": "other"}, conflict: true,
		},
		{
			name: "empty ownership", observed: map[string]string{"azd-byoaks-owner": "owned"},
			explicit: map[string]string{"azd-byoaks-owner": ""}, conflict: true,
		},
		{
			name: "cannot adopt an unowned group", observed: map[string]string{"customer": "preserve"},
			explicit: map[string]string{"azd-byoaks-owner": "owned"}, conflict: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := byoContractProvider(t)
			p.synthResult.Parameters["tags"] = test.observed
			parameters := map[string]any{"tags": map[string]any{"value": test.explicit}}
			err := p.bindPreparedGroupTags(parameters)
			if test.conflict {
				require.ErrorContains(t, err, "private azd-byoaks-owner")
				assert.Equal(t, test.explicit, parameters["tags"].(map[string]any)["value"])
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, parameters["tags"].(map[string]any)["value"])
		})
	}
}

func TestBYORealPreparationFinalARMTagsAndRepreparation(t *testing.T) {
	for _, existingGroup := range []bool{false, true} {
		for _, onDisk := range []bool{false, true} {
			t.Run(fmt.Sprintf("existing-group=%t/on-disk=%t", existingGroup, onDisk), func(t *testing.T) {
				p := byoContractProvider(t)
				groupID := "/subscriptions/" + p.subID + "/resourceGroups/" + p.rgName
				resources := map[string]map[string]any{}
				if existingGroup {
					resources[strings.ToLower(groupID)] = map[string]any{
						"id": groupID, "tags": map[string]any{"customer-billing": "preserve"},
					}
				}
				if onDisk {
					p.onDiskSource = &templateSource{
						mode: templateModeBicep, armTemplate: p.armTemplate, sourcePath: "offline-main.bicep",
						parameters: map[string]any{
							"tags": map[string]any{"value": map[string]any{
								"on-disk-addition": "keep", "customer-billing": "explicit-change",
							}},
						},
					}
				}
				var plan onboarding.Plan
				var preparedWrites, deployments int
				journal := &lifecycleJournal{}
				transport := byoHTTPContract(func(request *http.Request) (*http.Response, error) {
					key := strings.ToLower(request.URL.Path)
					respond := func(status int, body any) (*http.Response, error) {
						data, err := json.Marshal(body)
						require.NoError(t, err)
						return contractLifecycleResponse(request, status, string(data)), nil
					}
					if strings.Contains(key, "/roledefinitions") {
						return respond(200, map[string]any{"value": []any{map[string]any{
							"id": "/providers/Microsoft.Authorization/roleDefinitions/00000000-0000-0000-0000-000000000555",
							"properties": map[string]any{
								"roleName": "Managed Identity Federated Identity Credential Contributor", "type": "BuiltInRole",
							},
						}}})
					}
					if request.Method == http.MethodGet {
						if strings.HasSuffix(key, "/roleassignments") {
							var assignments []any
							for id, resource := range resources {
								if strings.Contains(id, "/roleassignments/") {
									assignments = append(assignments, resource)
								}
							}
							return respond(200, map[string]any{"value": assignments})
						}
						if resource, found := resources[key]; found {
							return respond(200, resource)
						}
						return respond(404, map[string]any{"error": map[string]any{"code": "ResourceNotFound"}})
					}
					require.Equal(t, http.MethodPut, request.Method)
					var body map[string]any
					require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
					if strings.Contains(key, "/providers/microsoft.resources/deployments/") {
						deployments++
						properties := lifecycleObject(t, body["properties"])
						template := lifecycleObject(t, properties["template"])
						templateResources := lifecycleObject(t, template["resources"])
						var found bool
						for _, raw := range templateResources {
							resource := lifecycleObject(t, raw)
							if resource["type"] == "Microsoft.Resources/resourceGroups" {
								require.Equal(t, "[parameters('tags')]", resource["tags"])
								found = true
							}
						}
						require.True(t, found, "exercise the real embedded resource-group PUT contract")
						parameters := lifecycleObject(t, properties["parameters"])
						tags := lifecycleObject(t, lifecycleObject(t, parameters["tags"])["value"])
						if onDisk {
							assert.Equal(t, "explicit-change", tags["customer-billing"])
						} else {
							assert.Equal(t, "preserve", tags["customer-billing"])
						}
						if existingGroup {
							assert.NotContains(t, tags, "azd-byoaks-owner", "never adopt the customer group")
						} else {
							assert.Equal(t, plan.Owner, tags["azd-byoaks-owner"])
						}

						if onDisk {
							assert.Equal(t, "keep", tags["on-disk-addition"])
						}
						resources[strings.ToLower(groupID)]["tags"] = tags
						return respond(200, map[string]any{"id": request.URL.Path,
							"properties": map[string]any{"provisioningState": "Succeeded", "outputs": map[string]any{}}})
					}
					preparedWrites++
					body["id"] = request.URL.Path
					switch {
					case strings.Contains(key, "/roleassignments/"):
						properties := lifecycleObject(t, body["properties"])
						properties["scope"] = request.URL.Path[:strings.Index(key, "/providers/microsoft.authorization/")]
					case strings.Contains(key, "/userassignedidentities/"):
						principal, client := "00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000011"
						if strings.EqualFold(request.URL.Path, plan.Workload.ID) {
							principal, client = "00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000022"
						}
						body["properties"] = map[string]any{
							"principalId": principal, "clientId": client, "tenantId": "tenant",
						}
					case strings.Contains(key, "/storageaccounts/"):
						lifecycleObject(t, body["properties"])["provisioningState"] = "Succeeded"
					case strings.Contains(key, "/registries/"):
						properties := lifecycleObject(t, body["properties"])
						properties["provisioningState"], properties["loginServer"] = "Succeeded", "owned.azurecr.io"
					default:
						require.Equal(t, strings.ToLower(groupID), key)
						lifecycleObject(t, body["tags"])["customer-billing"] = "preserve"
					}
					resources[key] = body
					return respond(200, body)
				})
				options := &arm.ClientOptions{DisableRPRegistration: true, ClientOptions: policy.ClientOptions{
					Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1},
				}}
				p.armClientOptions = options
				p.byoPipeline = &onboarding.Pipeline{
					Resources: byoResourceContract(func(_ context.Context, input onboarding.Plan) (
						onboarding.Facts, onboarding.Report, error,
					) {
						plan = input
						// Keep this preparation contract focused on RG ownership; network planning has separate SDK tests.
						plan.AgentSubnet = onboarding.Resource{ID: byoContractPrepared(input).AgentSubnetID, Supplied: true}
						return onboarding.Facts{TenantID: "tenant",
							KubeletObjectID: "00000000-0000-0000-0000-000000000003"}, nil, nil
					}),
					Access: byoAccessContract(func(context.Context, onboarding.Plan, onboarding.Facts) (onboarding.Report, error) {
						return nil, nil
					}),
					Prepare: byoPrepareContract(func(ctx context.Context, _ onboarding.Plan, facts onboarding.Facts) (
						onboarding.Prepared, error,
					) {
						return onboarding.NewPreparerWithVerifier(
							p.credential, options, journal, lifecycleNetwork{}).Prepare(ctx, plan, facts)
					}),
				}
				_, err := p.Deploy(t.Context(), func(string) {})
				require.NoError(t, err)
				writesAfterFirst := preparedWrites
				_, err = p.Deploy(t.Context(), func(string) {})
				require.NoError(t, err)
				assert.Equal(t, writesAfterFirst, preparedWrites, "second preparation must reuse the exact owned resources/grants")
				assert.Equal(t, 4, deployments)
			})
		}
	}
}
