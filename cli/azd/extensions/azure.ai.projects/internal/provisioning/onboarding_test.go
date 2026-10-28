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
	"os/exec"
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
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const offlineClusterID = "/subscriptions/cluster-sub/resourceGroups/aks-rg/providers/" +
	"Microsoft.ContainerService/managedClusters/aks"
const offlineVNetID = "/subscriptions/network-sub/resourceGroups/net-rg/providers/Microsoft.Network/virtualNetworks/vnet"

type byoTestCredential struct{}

func (byoTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "offline-only", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type byoTestTransport func(*http.Request) (*http.Response, error)

func (f byoTestTransport) Do(request *http.Request) (*http.Response, error) { return f(request) }

type memoryBYOJournal struct {
	t          *testing.T
	state      *byoState
	saves      []byoState
	failSaveAt int
}

func (j *memoryBYOJournal) Load(context.Context) (*byoState, error) {
	if j.state == nil {
		return nil, nil
	}
	copy := cloneBYOJSON(j.t, *j.state)
	return &copy, nil
}

func (j *memoryBYOJournal) Save(_ context.Context, state byoState) error {
	j.saves = append(j.saves, cloneBYOJSON(j.t, state))
	if len(j.saves) == j.failSaveAt {
		return errors.New("local intent write failed")
	}
	copy := cloneBYOJSON(j.t, state)
	j.state = &copy
	return nil
}

func cloneBYOJSON[T any](t *testing.T, value T) T {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	var result T
	require.NoError(t, json.Unmarshal(raw, &result))
	return result
}

type byoTestChecker struct{ facts onboarding.Facts }

func (c byoTestChecker) Check(context.Context, onboarding.Plan) (onboarding.Facts, onboarding.Report, error) {
	return c.facts, onboarding.Report{{ID: "A01", Status: onboarding.Pass, Observed: "offline verified cluster"}}, nil
}

type byoARMFake struct {
	t          *testing.T
	provider   *FoundryProvisioningProvider
	journal    *memoryBYOJournal
	plan       onboarding.Plan
	resources  map[string]map[string]any
	denied     map[string]int
	deployment map[string]any
	template   map[string]any
	ownership  map[string]any
	requests   int
	writes     int
	failWrite  bool
}

func (f *byoARMFake) response(request *http.Request, status int, body any) *http.Response {
	raw, err := json.Marshal(body)
	require.NoError(f.t, err)
	return &http.Response{StatusCode: status, Request: request, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(string(raw)))}
}

func (f *byoARMFake) notFound(request *http.Request, status int) *http.Response {
	return f.response(request, status, map[string]any{
		"error": map[string]string{"code": http.StatusText(status), "message": "offline response"},
	})
}

func (f *byoARMFake) Do(request *http.Request) (*http.Response, error) {
	f.requests++
	path := request.URL.Path
	if status := f.denied[path]; status != 0 {
		return f.notFound(request, status), nil
	}
	if path == f.plan.DeploymentID+"/exportTemplate" {
		require.Equal(f.t, http.MethodPost, request.Method)
		return f.response(request, 200, map[string]any{"template": f.template}), nil
	}
	if request.Method == http.MethodPut {
		require.Equal(f.t, f.plan.DeploymentID, path, "only the single subscription deployment may be written")
		require.NotNil(f.t, f.journal.state, "the write-ahead intent must be persistent before the first PUT")
		require.Equal(f.t, "deploying", f.journal.state.Status)
		require.NotEmpty(f.t, f.journal.state.Origins)
		f.writes++
		if f.failWrite {
			return f.notFound(request, 500), nil
		}
		var submitted struct {
			Properties struct {
				Template   map[string]any `json:"template"`
				Parameters map[string]any `json:"parameters"`
			} `json:"properties"`
		}
		require.NoError(f.t, json.NewDecoder(request.Body).Decode(&submitted))
		f.template = submitted.Properties.Template
		f.deployment = map[string]any{"id": path, "name": f.provider.deploymentName(), "location": "westus",
			"properties": map[string]any{
				"provisioningState": "Succeeded", "parameters": submitted.Properties.Parameters,
				"outputs": map[string]any{"AZD_BYO_AKS_OWNERSHIP": map[string]any{"type": "object", "value": f.ownership}},
			}}
		for _, target := range byoTargets(f.plan) {
			if target.Source != "created" {
				continue
			}
			properties := map[string]any{}
			if target.ID == f.plan.Hosting.ID {
				properties["principalId"] = f.ownership["hostingPrincipalId"]
			} else if target.ID == f.plan.Workload.ID {
				properties["principalId"] = f.ownership["workloadPrincipalId"]
			}
			f.resources[target.ID] = map[string]any{"id": target.ID, "properties": properties,
				"tags": map[string]string{byoOwnerTag: f.plan.DeploymentID}}
			if target.ID == f.plan.Project.ID {
				f.resources[target.ID]["identity"] = map[string]any{
					"type": "SystemAssigned", "principalId": f.ownership["projectPrincipalId"],
				}
			}
		}
		return f.response(request, 200, f.deployment), nil
	}
	require.Equal(f.t, http.MethodGet, request.Method, "no provider registration, what-if, delete, or other write")
	if path == f.plan.DeploymentID {
		if f.deployment == nil {
			return f.notFound(request, 404), nil
		}
		return f.response(request, 200, f.deployment), nil
	}
	if resource, exists := f.resources[path]; exists {
		return f.response(request, 200, resource), nil
	}
	for _, target := range byoTargets(f.plan) {
		if path == target.ID {
			return f.notFound(request, 404), nil
		}
	}
	f.t.Fatalf("unexpected ARM request %s %s", request.Method, path)
	return nil, errors.New("unexpected offline request")
}

func offlineBYOProvider(t *testing.T) (*FoundryProvisioningProvider, *memoryBYOJournal, *byoARMFake) {
	t.Helper()
	result, err := synthesis.Synthesize(synthesis.Input{ServiceName: "project", RawAzureYAML: []byte(`
services:
  project:
    host: azure.ai.project
    agentHosting: {clusterResourceId: ` + offlineClusterID + `}
    network:
      agentSubnet: {vnet: ` + offlineVNetID + `, name: agents}
      peSubnet: {vnet: ` + offlineVNetID + `, name: endpoints}
`)})
	require.NoError(t, err)
	raw, err := synthesis.ARMTemplate()
	require.NoError(t, err)
	var template map[string]any
	require.NoError(t, json.Unmarshal(raw, &template))
	facts := onboarding.Facts{KubeletObjectID: "11111111-1111-4111-8111-111111111111",
		FederatedIdentityRoleDefinitionID: "/subscriptions/target-sub/providers/Microsoft.Authorization/" +
			"roleDefinitions/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	journal := &memoryBYOJournal{t: t}
	p := &FoundryProvisioningProvider{
		projectPath: t.TempDir(), envName: "test", subID: "target-sub", rgName: "target-rg", location: "westus",
		foundryName: "project", synthResult: result, armTemplate: template, credential: byoTestCredential{},
		byoJournal: journal, byoChecker: byoTestChecker{facts}, virtualEnv: map[string]string{},
	}
	names := byoResolvedNames{
		AccountID: "/subscriptions/target-sub/resourceGroups/target-rg/providers/" +
			"Microsoft.CognitiveServices/accounts/cog-native",
		RegistryName: "crnative1234567", ResourceGroupSupplied: true,
		DeploymentID: "/subscriptions/target-sub/providers/Microsoft.Resources/deployments/" + p.deploymentName(),
	}
	p.byoNames = func(context.Context, *templateSource) (byoResolvedNames, error) { return names, nil }
	plan, err := onboarding.BuildPlan(onboarding.Input{
		ClusterID: offlineClusterID, SubscriptionID: p.subID, ResourceGroup: p.rgName, Location: p.location,
		Environment: p.envName, AccountID: names.AccountID, ProjectName: p.foundryName,
		RegistryName: names.RegistryName, DeploymentID: names.DeploymentID, ResourceGroupSupplied: true,
		Network: onboarding.Network{VNetID: offlineVNetID, AgentSubnetName: "agents", PESubnetName: "endpoints"},
	})
	require.NoError(t, err)
	fake := &byoARMFake{t: t, provider: p, journal: journal, plan: plan,
		resources: map[string]map[string]any{}, denied: map[string]int{}}
	for _, target := range byoTargets(plan) {
		if target.Source == "supplied" {
			fake.resources[target.ID] = map[string]any{"id": target.ID}
		}
	}
	fake.ownership = offlineBYOOwnership(plan, facts)
	p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
		Transport: fake, Retry: policy.RetryOptions{MaxRetries: -1},
	}}
	return p, journal, fake
}

func offlineBYOOwnership(plan onboarding.Plan, facts onboarding.Facts) map[string]any {
	hosting, workload, project := "22222222-2222-4222-8222-222222222222",
		"33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444"
	var grants []any
	for index, grant := range []struct{ scope, principal, role string }{
		{plan.Cluster.ID, hosting, "acdd72a7-3385-48ef-bd42-f606fba81ae7"},
		{plan.Cluster.ID, hosting, "85cb6faf-e071-4c9b-8136-154b5a04f717"},
		{plan.Workload.ID, hosting, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"},
		{plan.Storage.ID, workload, "ba92f5b4-2d11-453d-a403-e96b0029c9fe"},
		{plan.Registry.ID, project, "7f951dda-4ed3-4680-a7ca-43fe172d538d"},
		{plan.Registry.ID, facts.KubeletObjectID, "7f951dda-4ed3-4680-a7ca-43fe172d538d"},
	} {
		grants = append(grants, map[string]any{
			"id": grant.scope + "/providers/Microsoft.Authorization/roleAssignments/" +
				fmt.Sprintf("10000000-0000-0000-0000-%012d", index),
			"scope": grant.scope, "principalId": grant.principal,
			"roleDefinitionId": "/subscriptions/target-sub/providers/Microsoft.Authorization/roleDefinitions/" + grant.role,
		})
	}
	return map[string]any{
		"deploymentId": plan.DeploymentID, "storageAccountResourceId": plan.Storage.ID,
		"registryResourceId":        plan.Registry.ID,
		"hostingIdentityResourceId": plan.Hosting.ID, "workloadIdentityResourceId": plan.Workload.ID,
		"hostingPrincipalId": hosting, "workloadPrincipalId": workload, "projectPrincipalId": project,
		"kubeletPrincipalId": facts.KubeletObjectID,
		"ownedStorage":       true, "ownedRegistry": true, "ownedHostingIdentity": true, "ownedWorkloadIdentity": true,
		"roleAssignments": grants,
	}
}

func TestBYOPreviewDoesNotMutateAzureOrEnvironment(t *testing.T) {
	p, journal, fake := offlineBYOProvider(t)
	var progress []string
	result, err := p.Preview(t.Context(), func(text string) { progress = append(progress, text) })
	require.NoError(t, err)
	require.NotNil(t, result.Preview)
	assert.Contains(t, result.Preview.Summary, "No resources are written")
	assert.NotEmpty(t, progress)
	assert.NotZero(t, fake.requests)
	assert.Zero(t, fake.writes)
	assert.Empty(t, journal.saves)
	assert.Empty(t, p.virtualEnv)
}

func TestBYODeploySavesOriginalIntentBeforeWriteAndKeepsItOnReprovision(t *testing.T) {
	p, journal, fake := offlineBYOProvider(t)
	result, err := p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	require.NotNil(t, result.Deployment)
	require.Len(t, journal.saves, 2)
	assert.Equal(t, "deploying", journal.saves[0].Status)
	assert.Equal(t, "succeeded", journal.saves[1].Status)
	assert.NotEmpty(t, journal.saves[1].Ownership)
	assert.Equal(t, 1, fake.writes)
	for _, origin := range journal.state.Origins {
		assert.Equal(t, origin.Source == "created", origin.InitiallyAbsent)
	}
	original := cloneBYOJSON(t, journal.state.Origins)
	_, err = p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	assert.Equal(t, 2, fake.writes)
	assert.Equal(t, original, journal.state.Origins, "later existence must not replace the first absence/source observation")
}

func TestBYOSaveAndCloudFailuresAreFailClosed(t *testing.T) {
	for _, test := range []struct {
		name       string
		failSaveAt int
		failCloud  bool
		writes     int
		expected   string
	}{
		{"initial intent save", 1, false, 0, "before any Azure writes"},
		{"cloud failure", 0, true, 1, "offline response"},
		{"cloud success local completion failure", 2, false, 1, "Azure deployment succeeded"},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, journal, fake := offlineBYOProvider(t)
			journal.failSaveAt, fake.failWrite = test.failSaveAt, test.failCloud
			_, err := p.Deploy(t.Context(), func(string) {})
			require.ErrorContains(t, err, test.expected)
			assert.Equal(t, test.writes, fake.writes)
			if test.writes != 0 {
				require.NotNil(t, journal.state)
				assert.Equal(t, "deploying", journal.state.Status)
				requests := fake.requests
				_, err = p.Deploy(t.Context(), func(string) {})
				require.ErrorContains(t, err, "unresolved")
				assert.Equal(t, requests, fake.requests, "unresolved attempts cannot perform more Azure operations")
			}
		})
	}
}

func TestBYORefusesAdoptionAndUnverifiedAbsence(t *testing.T) {
	for _, test := range []struct {
		name     string
		status   int
		expected string
	}{
		{"existing storage", 200, "refusing adoption"},
		{"storage read denied", 403, "initial absence"},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, journal, fake := offlineBYOProvider(t)
			if test.status == 200 {
				fake.resources[fake.plan.Storage.ID] = map[string]any{"id": fake.plan.Storage.ID}
			} else {
				fake.denied[fake.plan.Storage.ID] = test.status
			}
			_, err := p.Deploy(t.Context(), func(string) {})
			require.ErrorContains(t, err, test.expected)
			assert.Zero(t, fake.writes)
			assert.Empty(t, journal.saves)
		})
	}
	t.Run("deployment already exists", func(t *testing.T) {
		p, journal, fake := offlineBYOProvider(t)
		fake.deployment = map[string]any{"id": fake.plan.DeploymentID}
		_, err := p.Deploy(t.Context(), func(string) {})
		require.ErrorContains(t, err, "refusing to overwrite")
		assert.Empty(t, journal.saves)
		assert.Zero(t, fake.writes)
	})
}

func TestBYOReprovisionRejectsChangedEvidenceWithoutAnotherWrite(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*byoARMFake, *memoryBYOJournal)
	}{
		{"original source", func(_ *byoARMFake, j *memoryBYOJournal) { j.state.Origins[0].Source = "created" }},
		{"missing ownership tag", func(f *byoARMFake, _ *memoryBYOJournal) {
			delete(f.resources[f.plan.Storage.ID], "tags")
		}},
		{"replaced principal", func(f *byoARMFake, _ *memoryBYOJournal) {
			f.resources[f.plan.Hosting.ID]["properties"] = map[string]any{
				"principalId": "55555555-5555-4555-8555-555555555555"}
		}},
		{"modified template", func(f *byoARMFake, _ *memoryBYOJournal) { f.template["contentVersion"] = "2.0.0.0" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, journal, fake := offlineBYOProvider(t)
			_, err := p.Deploy(t.Context(), func(string) {})
			require.NoError(t, err)
			test.change(fake, journal)
			_, err = p.Deploy(t.Context(), func(string) {})
			require.Error(t, err)
			assert.Equal(t, 1, fake.writes)
			assert.Len(t, journal.saves, 2)
		})
	}
}

func TestBYORejectsLocalConflictsBeforeNamingOrAuthentication(t *testing.T) {
	for _, test := range []struct {
		key   string
		value any
	}{
		{"includeAcr", false}, {"agentHosting", map[string]any{"enabled": true, "storageAccountResourceId": "supplied"}},
		{"tags", map[string]string{byoOwnerTag: "another-deployment"}},
	} {
		t.Run(test.key, func(t *testing.T) {
			p, journal, fake := offlineBYOProvider(t)
			p.credential = nil
			p.byoNames = func(context.Context, *templateSource) (byoResolvedNames, error) {
				t.Fatal("local conflicts must be rejected before naming/credential creation")
				return byoResolvedNames{}, nil
			}
			source, err := p.resolveTemplate(t.Context(), func(string) {})
			require.NoError(t, err)
			source.parameters[test.key] = map[string]any{"value": test.value}
			_, err = p.prepareBYO(t.Context(), source, func(string) {})
			require.Error(t, err)
			assert.Zero(t, fake.requests)
			assert.Empty(t, journal.saves)
		})
	}
	t.Run("supplied registry environment output", func(t *testing.T) {
		p, journal, fake := offlineBYOProvider(t)
		p.virtualEnv["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = fake.plan.Registry.ID
		p.credential = nil
		p.byoNames = func(context.Context, *templateSource) (byoResolvedNames, error) {
			t.Fatal("a supplied registry must be rejected before naming/credential creation")
			return byoResolvedNames{}, nil
		}
		_, err := p.Deploy(t.Context(), func(string) {})
		require.ErrorContains(t, err, "cannot select an existing registry")
		assert.Zero(t, fake.requests)
		assert.Empty(t, journal.saves)
	})
}

func TestBYOInitializeRejectsInvalidInputsBeforePromptsOrPersistence(t *testing.T) {
	for _, onDisk := range []bool{false, true} {
		root := t.TempDir()
		config := "services:\n  project:\n    host: azure.ai.project\n" +
			"    agentHosting: {clusterResourceId: " + offlineClusterID + ", storageAccountResourceId: ''}\n"
		path := filepath.Join(root, "azure.yaml")
		require.NoError(t, os.WriteFile(path, []byte(config), 0o600))
		if onDisk {
			require.NoError(t, os.Mkdir(filepath.Join(root, "infra"), 0o750))
			require.NoError(t, os.WriteFile(filepath.Join(root, "infra", "main.bicep"), []byte("// generated"), 0o600))
		}
		env := &resolveEnvStubEnvServer{envName: "test"}
		prompt := &resolveEnvStubPromptServer{}
		p := &FoundryProvisioningProvider{azdClient: newResolveEnvTestClient(t, env, prompt)}
		err := p.Initialize(t.Context(), root, &azdext.ProvisioningOptions{Provider: FoundryProviderName})
		require.ErrorContains(t, err, "storageAccountResourceId: unsupported property")
		assert.Empty(t, env.set)
		assert.Zero(t, prompt.subscriptionN)
		assert.Zero(t, prompt.locationN)
		assert.Nil(t, p.credential)
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, config, string(raw))
	}
}

func TestBYODownAndModeSwitchNeverReachGenericDeletion(t *testing.T) {
	for _, hosting := range []bool{false, true} {
		p, journal, fake := offlineBYOProvider(t)
		if !hosting {
			source, err := p.resolveTemplate(t.Context(), func(string) {})
			require.NoError(t, err)
			journal.state, err = p.prepareBYO(t.Context(), source, func(string) {})
			require.NoError(t, err)
			p.synthResult.Hosting = nil
			require.ErrorContains(t, p.guardBYOMode(t.Context()), "changing to another hosting mode")
		}
		p.credential = nil
		requests := fake.requests
		result, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{}, func(string) {})
		require.ErrorContains(t, err, "azd down is not supported")
		assert.Nil(t, result)
		assert.Equal(t, requests, fake.requests)
		assert.Zero(t, fake.writes)
	}
	p := &FoundryProvisioningProvider{virtualEnv: map[string]string{envKeyBYOJournal: "{malformed"}}
	_, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{}, func(string) {})
	require.ErrorContains(t, err, "invalid "+envKeyBYOJournal)
}

func TestBYOCompletionRequiresAllGrants(t *testing.T) {
	p, journal, fake := offlineBYOProvider(t)
	fake.ownership["roleAssignments"] = []any{}
	_, err := p.Deploy(t.Context(), func(string) {})
	require.ErrorContains(t, err, "requires all six role assignments")
	require.NotNil(t, journal.state)
	assert.Equal(t, "deploying", journal.state.Status)
	assert.Len(t, journal.saves, 1)
	assert.Equal(t, 1, fake.writes)
}

func TestBYOTemplateDigestAllowsOnlyExportNormalization(t *testing.T) {
	p, _, _ := offlineBYOProvider(t)
	expected, err := byoTemplateDigest(p.armTemplate)
	require.NoError(t, err)
	copy := cloneBYOJSON(t, p.armTemplate)
	delete(copy, "metadata")
	parameters, ok := copy["parameters"].(map[string]any)
	require.True(t, ok)
	location, ok := parameters["location"].(map[string]any)
	require.True(t, ok)
	location["type"] = "String"
	actual, err := byoTemplateDigest(copy)
	require.NoError(t, err)
	assert.Equal(t, expected, actual)
	location["defaultValue"] = "different-region"
	actual, err = byoTemplateDigest(copy)
	require.NoError(t, err)
	assert.NotEqual(t, expected, actual)
}

func TestBYONativeNamingPreservesActualScopeAndUsesNativeRegistry(t *testing.T) {
	p, _, _ := offlineBYOProvider(t)
	p.byoNames = nil
	p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
		Retry: policy.RetryOptions{MaxRetries: -1},
		Transport: byoTestTransport(func(request *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodGet, request.Method)
			id := "/subscriptions/TARGET-SUB"
			if strings.Contains(request.URL.Path, "/resourcegroups/") {
				id += "/resourceGroups/Target-RG"
			}
			body := `{"id":"` + id + `"}`
			return &http.Response{StatusCode: 200, Request: request,
				Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		}),
	}}
	var captured byoNamingScope
	var temporary string
	p.bicepCliInstance = &stubCompiler{buildParam: func(
		_ context.Context, file string, env []string,
	) (bicep.BuildResult, error) {
		temporary = filepath.Dir(file)
		raw, err := os.ReadFile(filepath.Join(temporary, "scope.json"))
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &captured))
		assert.Contains(t, env, "BICEP_PARAMETERS_OVERRIDES={}")
		envelope, err := json.Marshal(map[string]string{
			"parametersJson": minimalARMParametersFile(t, map[string]any{"resourceToken": "aaaaaaaaaaaaa"}),
		})
		require.NoError(t, err)
		return bicep.BuildResult{Compiled: string(envelope)}, nil
	}}
	source, err := p.resolveTemplate(t.Context(), func(string) {})
	require.NoError(t, err)
	source.parameters["location"] = map[string]any{"value": "WestUS"}
	source.parameters["resourceTokenSalt"] = map[string]any{"value": " Exact Salt "}
	names, err := p.resolveBYONames(t.Context(), source)
	require.NoError(t, err)
	assert.Equal(t, "/subscriptions/TARGET-SUB", captured.SubscriptionID)
	assert.Equal(t, "/subscriptions/TARGET-SUB/resourceGroups/Target-RG", captured.ResourceGroupID)
	assert.Equal(t, "WestUS", captured.Location)
	assert.Equal(t, " Exact Salt ", captured.Salt)
	assert.Equal(t, "craaaaaaaaaaaaa", names.RegistryName)
	assert.Contains(t, names.AccountID, "/accounts/cog-aaaaaaaaaaaaa")
	assert.True(t, names.ResourceGroupSupplied)
	_, err = os.Stat(temporary)
	assert.True(t, errors.Is(err, os.ErrNotExist), "temporary naming inputs must be removed")
}

func TestBYONativeUniqueStringWithBicep(t *testing.T) {
	path := os.Getenv("AZD_BICEP_TOOL_PATH")
	if path == "" {
		t.Skip("set AZD_BICEP_TOOL_PATH to run the native uniqueString contract")
	}
	compiler := &stubCompiler{buildParam: func(
		ctx context.Context, file string, env []string,
	) (bicep.BuildResult, error) {
		//nolint:gosec // The explicit local test Bicep override is validated by the synthesis suite.
		command := exec.CommandContext(ctx, path, "build-params", file, "--stdout")
		command.Env = append(os.Environ(), env...)
		raw, err := command.CombinedOutput()
		if err != nil {
			return bicep.BuildResult{}, fmt.Errorf("Bicep evaluation failed: %w: %s", err, raw)
		}
		return bicep.BuildResult{Compiled: string(raw)}, nil
	}}
	scope := byoNamingScope{SubscriptionID: "/subscriptions/Test",
		ResourceGroupID: "/subscriptions/Test/resourceGroups/Group", Location: "westus"}
	first, err := evaluateBYOResourceToken(t.Context(), compiler, scope)
	require.NoError(t, err)
	again, err := evaluateBYOResourceToken(t.Context(), compiler, scope)
	require.NoError(t, err)
	assert.Equal(t, first, again)
	scope.Salt = "test"
	salted, err := evaluateBYOResourceToken(t.Context(), compiler, scope)
	require.NoError(t, err)
	assert.NotEqual(t, first, salted)
}

func TestBYOEnvironmentJournalSurvivesProviderRestart(t *testing.T) {
	p, _, fake := offlineBYOProvider(t)
	source, err := p.resolveTemplate(t.Context(), func(string) {})
	require.NoError(t, err)
	intent, err := p.prepareBYO(t.Context(), source, func(string) {})
	require.NoError(t, err)
	server := &resolveEnvStubEnvServer{envName: p.envName}
	p.azdClient = newResolveEnvTestClient(t, server, &resolveEnvStubPromptServer{})
	journal := environmentBYOJournal{p}
	require.NoError(t, journal.Save(t.Context(), *intent))
	assert.NotEmpty(t, server.set[envKeyBYOJournal])
	fresh := &FoundryProvisioningProvider{azdClient: newResolveEnvTestClient(t,
		&resolveEnvStubEnvServer{get: server.set}, &resolveEnvStubPromptServer{}), envName: p.envName}
	reloaded, err := (environmentBYOJournal{fresh}).Load(t.Context())
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	assert.Equal(t, fake.plan, reloaded.Plan)
	assert.Equal(t, intent.Origins, reloaded.Origins)
	require.ErrorContains(t, fresh.refuseBYODestroy(t.Context()), "azd down is not supported")
}
