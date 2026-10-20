// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.projects/internal/onboarding"
	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type providerLifecycleARM struct {
	provider *FoundryProvisioningProvider
	journal  *lifecycleJournal
	objects  map[string]map[string]any
	stages   []bool
	events   []string
	deny     string
}

func newProviderLifecycleARM(t *testing.T) *providerLifecycleARM {
	t.Helper()
	p := byoContractProvider(t)
	p.virtualEnv = map[string]string{
		"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": "/subscriptions/customer/resourceGroups/registry-rg/" +
			"providers/Microsoft.ContainerRegistry/registries/supplied",
	}
	f := &providerLifecycleARM{provider: p, journal: &lifecycleJournal{}, objects: map[string]map[string]any{}}
	p.byoPipeline = &onboarding.Pipeline{
		Resources: byoResourceContract(func(context.Context, onboarding.Plan) (onboarding.Facts, onboarding.Report, error) {
			return onboarding.Facts{}, onboarding.Report{{ID: "I02", Status: onboarding.Pass, Mandatory: true}}, nil
		}),
		Access: byoAccessContract(func(context.Context, onboarding.Plan, onboarding.Facts) (onboarding.Report, error) {
			return onboarding.Report{{ID: "P09", Status: onboarding.Pass, Mandatory: true}}, nil
		}),
		Observe: func(onboarding.Report) { f.events = append(f.events, "report") },
		Prepare: byoPrepareContract(func(ctx context.Context, plan onboarding.Plan, _ onboarding.Facts) (
			onboarding.Prepared, error,
		) {
			f.events = append(f.events, "prepare")
			require.NoError(t, f.journal.Save(ctx, onboarding.PreparationState{
				Version: 2, Owner: plan.Owner, SubscriptionID: plan.SubscriptionID,
				ResourceGroup: plan.ResourceGroup, Location: plan.Location,
				Resources: map[string]onboarding.OwnedResource{}, Assignments: map[string]onboarding.OwnedAssignment{},
			}))
			return byoContractPrepared(plan), nil
		}),
	}
	p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
		Transport: byoHTTPContract(func(request *http.Request) (*http.Response, error) {
			path := strings.ToLower(request.URL.Path)
			respond := func(status int, value any) (*http.Response, error) {
				if value == nil {
					return contractLifecycleResponse(request, status, ""), nil
				}
				data, err := json.Marshal(value)
				require.NoError(t, err)
				return contractLifecycleResponse(request, status, string(data)), nil
			}
			if request.Method != http.MethodGet && strings.Contains(path, f.deny) && f.deny != "" {
				return respond(403, map[string]any{"error": map[string]any{"code": "AuthorizationFailed"}})
			}
			switch request.Method {
			case http.MethodGet:
				if strings.HasSuffix(path, "/roleassignments") || strings.HasSuffix(path, "/deployments") {
					return respond(200, map[string]any{"value": []any{}})
				}
				if strings.HasSuffix(path, "/projects") {
					var projects []any
					for key, object := range f.objects {
						if strings.Contains(key, "/projects/") {
							projects = append(projects, object)
						}
					}
					return respond(200, map[string]any{"value": projects})
				}
				if object, exists := f.objects[path]; exists {
					return respond(200, object)
				}
				return respond(404, map[string]any{"error": map[string]any{"code": "ResourceNotFound"}})
			case http.MethodPut:
				var body map[string]any
				require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
				body["id"] = request.URL.Path
				properties := lifecycleObject(t, body["properties"])
				state, err := f.journal.Load(request.Context())
				require.NoError(t, err)
				require.NotNil(t, state.Foundry)
				if strings.Contains(path, "/providers/microsoft.resources/deployments/") {
					require.Equal(t, request.URL.Path, state.Foundry.Attempts[len(state.Foundry.Attempts)-1].ID)
					parameters := lifecycleObject(t, properties["parameters"])
					ready, ok := lifecycleObject(t, parameters["byoRegistryAccessReady"])["value"].(bool)
					require.True(t, ok)
					f.stages = append(f.stages, ready)
					if ready {
						require.NotEmpty(t, state.Assignments, "actual project grant must be durable before connection stage")
					}
					f.events = append(f.events, "ARM")
					properties["provisioningState"], properties["correlationId"] = "Succeeded", request.URL.Path
					properties["templateHash"] = state.Foundry.Spec.TemplateHash
					spec := state.Foundry.Spec
					tags := map[string]any{"azd-byoaks-owner": state.Owner}
					f.objects[strings.ToLower(spec.AccountID)] = map[string]any{
						"id": spec.AccountID, "location": spec.Location, "tags": tags,
					}
					f.objects[strings.ToLower(spec.ProjectID)] = map[string]any{
						"id": spec.ProjectID, "tags": tags,
						"identity": map[string]any{"principalId": "00000000-0000-0000-0000-000000000004"},
					}
				} else {
					require.Contains(t, path, "/roleassignments/")
					require.Len(t, f.stages, 1)
					require.Equal(t, state.Foundry.ProjectPrincipalID, properties["principalId"])
					require.Equal(t, state.Owner, properties["description"])
					assignment, err := arm.ParseResourceID(request.URL.Path)
					require.NoError(t, err)
					require.Contains(t, state.Assignments, assignment.Name)
					properties["scope"] = state.Foundry.Spec.RegistryID
					f.events = append(f.events, "grant")
				}
				f.objects[path] = body
				return respond(200, body)
			case http.MethodDelete:
				require.NotContains(t, path, "/providers/microsoft.resources/deployments/")
				require.Contains(t, path, "/providers/", "whole resource-group deletion is forbidden")
				require.NotEqual(t, strings.ToLower(p.virtualEnv["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"]), path)
				delete(f.objects, path)
				for key := range f.objects {
					if strings.HasPrefix(key, path+"/") {
						delete(f.objects, key)
					}
				}
				f.events = append(f.events, "delete")
				return respond(200, nil)
			default:
				t.Fatalf("unexpected lifecycle request: %s %s", request.Method, request.URL.Path)
				return nil, nil
			}
		}),
	}}
	p.byoLifecycle = onboarding.NewLifecycle(p.credential, p.armClientOptions, f.journal)
	return f
}

func TestBYOProviderActualSDKStagesAndPersistedCleanupAfterYAMLChange(t *testing.T) {
	f := newProviderLifecycleARM(t)
	p := f.provider
	_, err := p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	assert.Equal(t, []bool{false, true}, f.stages)
	assert.Equal(t, []string{"report", "prepare", "ARM", "grant", "ARM"}, f.events)
	p.virtualEnv[envKeyBYOJournal] = string(f.journal.data)
	p.synthResult.Hosting = nil
	p.brownfieldEndpoint = "https://customer.services.ai.azure.com/api/projects/supplied"
	p.subID, p.rgName = "changed-subscription", "changed-group"
	result, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
	require.NoError(t, err)
	assert.Contains(t, result.InvalidatedEnvKeys, "AZURE_AI_PROJECT_ID")
	assert.NotContains(t, result.InvalidatedEnvKeys, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
	assert.NotContains(t, result.InvalidatedEnvKeys, "AZURE_CONTAINER_REGISTRY_ENDPOINT")
	assert.NotContains(t, result.InvalidatedEnvKeys, envKeyBYOJournal)
	state, err := f.journal.Load(t.Context())
	require.NoError(t, err)
	assert.True(t, state.CleanupComplete)
	assert.True(t, state.Foundry.AccountDeleted)
	for _, assignment := range state.Assignments {
		assert.True(t, assignment.Deleted, "cross-RG actual project registry grant must be cleaned up")
	}
}

func TestBYOProviderActualSDKGrantFailureStopsFinalConnectionStage(t *testing.T) {
	f := newProviderLifecycleARM(t)
	f.deny = "/roleassignments/"
	_, err := f.provider.Deploy(t.Context(), func(string) {})
	require.Error(t, err)
	assert.Equal(t, []bool{false}, f.stages)
	service, ok := errors.AsType[*azdext.ServiceError](err)
	require.True(t, ok)
	assert.Equal(t, http.StatusForbidden, service.StatusCode)
	_, ok = errors.AsType[*azcore.ResponseError](err)
	require.True(t, ok, "retain original typed Azure cause")
	state, err := f.journal.Load(t.Context())
	require.NoError(t, err)
	assert.NotEmpty(t, state.Assignments, "failed actual grant retains its durable creation intent")
}

func TestBYOProviderActualSDKCleanupFailureIsStructuredAndResumable(t *testing.T) {
	f := newProviderLifecycleARM(t)
	_, err := f.provider.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	f.provider.virtualEnv[envKeyBYOJournal] = string(f.journal.data)
	f.deny = "/accounts/"
	_, err = f.provider.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
	require.Error(t, err)
	service, ok := errors.AsType[*azdext.ServiceError](err)
	require.True(t, ok)
	assert.Equal(t, http.StatusForbidden, service.StatusCode)
	_, ok = errors.AsType[*azcore.ResponseError](err)
	require.True(t, ok)
	state, err := f.journal.Load(t.Context())
	require.NoError(t, err)
	assert.True(t, state.CleanupStarted)
	assert.False(t, state.CleanupComplete)
	for _, assignment := range state.Assignments {
		assert.False(t, assignment.Deleted)
	}
	f.deny = ""
	f.provider.virtualEnv[envKeyBYOJournal] = string(f.journal.data)
	_, err = f.provider.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
	require.NoError(t, err)
}

func TestBYORejectsBicepThatDeclaresButIgnoresPrivateOwnershipInputs(t *testing.T) {
	compiler, err := exec.LookPath("bicep")
	if err != nil {
		t.Skip("Bicep CLI is required for native ownership-binding regression")
	}
	templates, err := fs.Sub(synthesis.TemplatesFS(), "templates")
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.CopyFS(dir, templates))
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	module := filepath.Join("modules", "resources.bicep")
	raw, err := root.ReadFile(module)
	require.NoError(t, err)
	const binding = "var foundryAccountName = empty(byoAccountName) ? " +
		"'${abbrs.cognitiveServicesAccounts}${resourceToken}' : byoAccountName"
	require.Contains(t, string(raw), binding)
	changed := strings.Replace(string(raw), binding, "var foundryAccountName = 'untracked-account'", 1)
	require.NoError(t, root.WriteFile(module, []byte(changed), 0o600))
	// #nosec G204 -- Trusted Bicep CLI; all arguments address a test-owned temporary fixture.
	compiled, err := exec.CommandContext(t.Context(), compiler, "build", filepath.Join(dir, "main.bicep"), "--stdout").Output()
	require.NoError(t, err)
	var template map[string]any
	require.NoError(t, json.Unmarshal(compiled, &template))
	parameters := lifecycleObject(t, template["parameters"])
	require.Contains(t, parameters, "byoOwner")
	require.Contains(t, parameters, "byoAccountName")
	p := byoContractProvider(t)
	p.onDiskSource = &templateSource{mode: templateModeBicep, armTemplate: template, sourcePath: dir}
	p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
		Transport: byoHTTPContract(func(*http.Request) (*http.Response, error) {
			t.Fatal("altered ownership binding must be rejected before Azure access or preparation")
			return nil, nil
		}),
	}}
	_, err = p.Deploy(t.Context(), func(string) {})
	require.ErrorContains(t, err, "standard ownership-bound template")
}
