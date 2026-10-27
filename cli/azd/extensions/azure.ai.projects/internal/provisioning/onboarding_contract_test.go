// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"azure.ai.projects/internal/onboarding"
	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type byoResourceContract func(context.Context, onboarding.Plan) (onboarding.Facts, onboarding.Report, error)

func (f byoResourceContract) Check(
	ctx context.Context, plan onboarding.Plan,
) (onboarding.Facts, onboarding.Report, error) {
	return f(ctx, plan)
}

type byoAccessContract func(context.Context, onboarding.Plan, onboarding.Facts) (onboarding.Report, error)

func (f byoAccessContract) Check(
	ctx context.Context, plan onboarding.Plan, facts onboarding.Facts,
) (onboarding.Report, error) {
	return f(ctx, plan, facts)
}

type byoHTTPContract func(*http.Request) (*http.Response, error)

func (f byoHTTPContract) Do(request *http.Request) (*http.Response, error) { return f(request) }

type byoCredentialContract struct{}

func (byoCredentialContract) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "offline-contract-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

const byoProviderCluster = "/subscriptions/aks-sub/resourceGroups/aks-rg/providers/" +
	"Microsoft.ContainerService/managedClusters/customer"

const byoProviderVNet = "/subscriptions/network-sub/resourceGroups/network-rg/providers/" +
	"Microsoft.Network/virtualNetworks/test"

const byoProviderNetwork = "    network:\n" +
	"      agentSubnet: {vnet: " + byoProviderVNet + ", name: agents}\n" +
	"      peSubnet: {vnet: " + byoProviderVNet + ", name: pe}\n"

func byoContractProvider(t *testing.T) *FoundryProvisioningProvider {
	t.Helper()
	result, err := synthesis.Synthesize(synthesis.Input{
		RawAzureYAML: []byte("services:\n  project:\n    host: azure.ai.project\n    agentHosting:\n" +
			"      clusterResourceId: " + byoProviderCluster + "\n" + byoProviderNetwork),
		ServiceName: "project", AcceptedHosts: []string{"azure.ai.project"},
	})
	require.NoError(t, err)
	template, err := synthesis.ARMTemplate()
	require.NoError(t, err)
	var armTemplate map[string]any
	require.NoError(t, json.Unmarshal(template, &armTemplate))
	return &FoundryProvisioningProvider{
		azdClient: newResolveEnvTestClient(t, &resolveEnvStubEnvServer{
			envName: "contract", get: map[string]string{},
		}, &resolveEnvStubPromptServer{}),
		synthResult: result, armTemplate: armTemplate, projectPath: t.TempDir(),
		envName: "contract", subID: "foundry-sub", rgName: "foundry-rg", location: "eastus",
		foundryName: "contract-project", credential: byoCredentialContract{},
		tenantID:     "tenant",
		byoLifecycle: byoLifecycleContract{},
		byoBindings: func(_ context.Context, plan onboarding.Plan, facts onboarding.Facts) (onboarding.DeploymentData, error) {
			return onboarding.DeploymentData{
				ClusterID: plan.Cluster.ID, TenantID: "tenant", Hosting: plan.Hosting, Workload: plan.Workload,
				StorageID: plan.Storage.ID, StorageSupplied: plan.Storage.Supplied,
				StorageCustomerSupplied: plan.Storage.Supplied,
				RegistryID:              plan.Registry.ID, RegistrySupplied: plan.Registry.Supplied,
				RegistryABAC: facts.RegistryABAC, KubeletObjectID: facts.KubeletObjectID,
				StorageSKU: onboarding.StorageSKU(plan.Location),
				FederatedIdentityRoleDefinitionID: "/subscriptions/" + plan.SubscriptionID +
					"/providers/Microsoft.Authorization/roleDefinitions/00000000-0000-0000-0000-000000000555",
			}, nil
		},
	}
}

func byoContractPipeline(events *[]string, status onboarding.Status) *onboarding.Pipeline {
	return &onboarding.Pipeline{
		Resources: byoResourceContract(func(context.Context, onboarding.Plan) (onboarding.Facts, onboarding.Report, error) {
			*events = append(*events, "resources")
			return onboarding.Facts{
					KubeletObjectID: "00000000-0000-0000-0000-000000000003",
				}, onboarding.Report{
					{ID: "I02", Status: status, Mandatory: true, Missing: "referenced resource evidence"},
				}, nil
		}),
		Access: byoAccessContract(func(context.Context, onboarding.Plan, onboarding.Facts) (onboarding.Report, error) {
			*events = append(*events, "authorization")
			return onboarding.Report{{ID: "P02", Status: onboarding.Pass, Mandatory: true}}, nil
		}),
	}
}

func TestBYOProviderPreflightStopsAllWriteStages(t *testing.T) {
	for _, status := range []onboarding.Status{onboarding.Fail, onboarding.Unverified} {
		for _, preview := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/preview=%t", status, preview), func(t *testing.T) {
				p := byoContractProvider(t)
				var events []string
				p.byoPipeline = byoContractPipeline(&events, status)
				p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
					Transport: byoHTTPContract(func(*http.Request) (*http.Response, error) {
						t.Fatal("ARM deployment ran despite mandatory preflight failure")
						return nil, nil
					}),
				}}
				var err error
				if preview {
					_, err = p.Preview(t.Context(), func(string) {})
				} else {
					_, err = p.Deploy(t.Context(), func(string) {})
				}
				require.Error(t, err)
				assert.Equal(t, []string{"resources", "authorization"}, events)
				assert.NotContains(t, fmt.Sprint(p.synthResult.Parameters), "hosting-object")
			})
		}
	}
}

func TestBYOProviderPreviewDoesNotCompleteFutureParameters(t *testing.T) {
	p := byoContractProvider(t)
	var events []string
	p.byoPipeline = byoContractPipeline(&events, onboarding.Pass)
	before, err := json.Marshal(p.synthResult.Parameters)
	require.NoError(t, err)
	result, err := p.Preview(t.Context(), func(string) {})
	require.NoError(t, err)
	assert.Contains(t, result.Preview.Summary, "read-only")
	after, err := json.Marshal(p.synthResult.Parameters)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
	assert.Equal(t, []string{"resources", "authorization"}, events)
}

func TestBYOProviderShowsStorageRecommendationAfterPreflight(t *testing.T) {
	const warning = "Supplied Storage account uses Standard_LRS without zone redundancy. Zone redundancy is recommended."
	for _, status := range []onboarding.Status{onboarding.Pass, onboarding.Fail} {
		t.Run(string(status), func(t *testing.T) {
			p := byoContractProvider(t)
			var events []string
			p.byoPipeline = byoContractPipeline(&events, onboarding.Pass)
			p.byoPipeline.Resources = byoResourceContract(func(
				context.Context, onboarding.Plan,
			) (onboarding.Facts, onboarding.Report, error) {
				events = append(events, "resources")
				return onboarding.Facts{KubeletObjectID: "00000000-0000-0000-0000-000000000003"},
					onboarding.Report{{ID: "S01", Status: status, Mandatory: true, Warning: warning}}, nil
			})
			var output strings.Builder
			err := p.planBYO(t.Context(), func(message string) {
				assert.Equal(t, []string{"resources", "authorization"}, events,
					"recommendations are emitted after the complete read-only preflight")
				output.WriteString(message)
			}, false)
			assert.Equal(t, status != onboarding.Pass, err != nil)
			assert.Contains(t, output.String(), "WARNING: S01")
			assert.Contains(t, output.String(), warning)
			assert.Less(t, strings.Index(output.String(), "P02 PASS"), strings.Index(output.String(), "WARNING:"))
			if status == onboarding.Pass {
				assert.Contains(t, p.synthResult.Parameters, "byoOnboarding",
					"a warning does not prevent preparing the single ARM submission")
			}
		})
	}
}

func TestBYOProviderRealSDKResolutionFailureCannotWrite(t *testing.T) {
	for _, preview := range []bool{false, true} {
		t.Run(fmt.Sprintf("preview=%t", preview), func(t *testing.T) {
			p := byoContractProvider(t)
			var requests int
			var output strings.Builder
			p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
				Transport: byoHTTPContract(func(request *http.Request) (*http.Response, error) {
					requests++
					require.Equal(t, http.MethodGet, request.Method)
					require.Equal(t, "/subscriptions/foundry-sub/resourcegroups/foundry-rg",
						strings.ToLower(request.URL.Path))
					return &http.Response{
						StatusCode: http.StatusForbidden,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body: io.NopCloser(strings.NewReader(
							`{"error":{"code":"AuthorizationFailed","message":"offline parent read denied"}}`)),
						Request: request,
					}, nil
				}),
			}}
			progress := func(message string) { output.WriteString(message) }
			var err error
			if preview {
				_, err = p.Preview(t.Context(), progress)
			} else {
				_, err = p.Deploy(t.Context(), progress)
			}
			require.Error(t, err)
			serviceError, ok := errors.AsType[*azcore.ResponseError](err)
			require.True(t, ok, "original Azure response error must remain available")
			assert.Equal(t, http.StatusForbidden, serviceError.StatusCode)
			assert.Equal(t, "AuthorizationFailed", serviceError.ErrorCode)
			assert.Contains(t, output.String(), "I03 UNVERIFIED")
			assert.Contains(t, output.String(), "offline parent read denied")
			assert.Equal(t, 1, requests)
			assert.Empty(t, p.virtualEnv[envKeyBYOJournal])
		})
	}
}

func TestBYOProviderPreflightThenSingleDeployment(t *testing.T) {
	p := byoContractProvider(t)
	var events []string
	var progressOutput strings.Builder
	p.byoPipeline = byoContractPipeline(&events, onboarding.Pass)
	p.armClientOptions = &arm.ClientOptions{
		DisableRPRegistration: true,
		ClientOptions: policy.ClientOptions{Transport: byoHTTPContract(func(request *http.Request) (*http.Response, error) {
			events = append(events, "ARM")
			require.Contains(t, progressOutput.String(), "I02 PASS")
			require.Contains(t, progressOutput.String(), "P02 PASS")
			require.Equal(t, http.MethodPut, request.Method)
			require.Contains(t, request.URL.Path, "/subscriptions/foundry-sub/providers/Microsoft.Resources/deployments/")
			var deployment struct {
				Properties struct {
					Parameters map[string]struct {
						Value json.RawMessage `json:"value"`
					} `json:"parameters"`
				} `json:"properties"`
			}
			require.NoError(t, json.NewDecoder(request.Body).Decode(&deployment))
			var hosting map[string]any
			require.NoError(t, json.Unmarshal(deployment.Properties.Parameters["agentHosting"].Value, &hosting))
			assert.Equal(t, "ManagedCluster", hosting["hostingType"])
			assert.Equal(t, "aks", hosting["name"])
			assert.Equal(t, byoProviderCluster, hosting["clusterResourceId"])
			assert.Empty(t, hosting["hostingManagementIdentityResourceId"])
			assert.Empty(t, hosting["workloadIdentityResourceId"])
			assert.NotContains(t, deployment.Properties.Parameters, "byoRegistryAccessReady")
			require.Contains(t, deployment.Properties.Parameters, "byoOnboarding")
			body := `{"id":"` + request.URL.Path + `","properties":{"provisioningState":"Succeeded","outputs":{}}}`
			return &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(strings.NewReader(body)), Request: request,
			}, nil
		})},
	}
	_, err := p.Deploy(t.Context(), func(message string) { progressOutput.WriteString(message) })
	require.NoError(t, err)
	assert.Equal(t, []string{"resources", "authorization", "ARM"}, events)
}

func TestBYOProviderCancellationAfterPreflightSkipsDeployment(t *testing.T) {
	p := byoContractProvider(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var events []string
	p.byoPipeline = byoContractPipeline(&events, onboarding.Pass)
	p.byoPipeline.Access = byoAccessContract(func(context.Context, onboarding.Plan, onboarding.Facts) (onboarding.Report, error) {
		events = append(events, "authorization")
		cancel()
		return nil, nil
	})
	_, err := p.Deploy(ctx, func(string) {})
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, []string{"resources", "authorization"}, events)
}

func TestBYOProviderOnDiskParametersCannotOverridePlannedDependencies(t *testing.T) {
	p := byoContractProvider(t)
	var events []string
	p.byoPipeline = byoContractPipeline(&events, onboarding.Pass)
	require.NoError(t, p.planBYO(t.Context(), func(string) {}, false))
	p.onDiskSource = &templateSource{
		armTemplate: p.armTemplate,
		parameters: map[string]any{
			"agentHosting":                map[string]any{"value": "untrusted-identities"},
			"containerRegistryResourceId": map[string]any{"value": "untrusted-registry"},
			"containerRegistryABAC":       map[string]any{"value": true},
			"includeAcr":                  map[string]any{"value": false},
		},
	}
	source, err := p.resolveTemplate(t.Context(), func(string) {})
	require.NoError(t, err)
	for _, key := range []string{"agentHosting", "containerRegistryResourceId", "containerRegistryABAC", "includeAcr"} {
		assert.Equal(t, p.armParameters()[key], source.parameters[key], key)
	}
}

func TestBYOProviderInitializeRejectsExplicitEmptyIdentities(t *testing.T) {
	for _, field := range []string{"hostingManagementIdentityResourceId", "workloadIdentityResourceId"} {
		for _, value := range []string{`""`, "null"} {
			t.Run(field+"/"+value, func(t *testing.T) {
				projectPath := t.TempDir()
				config := "services:\n  project:\n    host: azure.ai.project\n    agentHosting:\n" +
					"      clusterResourceId: " + byoProviderCluster + "\n      " + field + ": " + value + "\n"
				require.NoError(t, os.WriteFile(filepath.Join(projectPath, "azure.yaml"), []byte(config), 0o600))
				client := newValidateTestClient(t, &validateStubProjectServer{}, &validateStubEnvServer{
					envName: "contract",
					get:     map[string]string{envKeySubscriptionID: "foundry-sub", envKeyLocation: "eastus"},
				})
				p := &FoundryProvisioningProvider{azdClient: client}
				err := p.Initialize(t.Context(), projectPath, &azdext.ProvisioningOptions{Provider: FoundryProviderName})
				require.ErrorContains(t, err, field)
				assert.Nil(t, p.credential)
			})
		}
	}
}

func TestBYOJournalReadAfterWriteUsesNewPersistedState(t *testing.T) {
	client := newValidateTestClient(t, &validateStubProjectServer{}, &resolveEnvStubEnvServer{envName: "contract"})
	p := &FoundryProvisioningProvider{
		azdClient: client, envName: "contract", virtualEnv: map[string]string{envKeyBYOJournal: `{"owner":"old"}`},
	}
	journal := environmentJournal{p}
	require.NoError(t, journal.Save(t.Context(), onboarding.PreparationState{Owner: "new"}))
	state, err := journal.Load(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "new", state.Owner)
}
