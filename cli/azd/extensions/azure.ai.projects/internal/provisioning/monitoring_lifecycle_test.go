// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	core "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMonitoringReadErrorSanitizesNonAzureFailures(t *testing.T) {
	for _, cause := range []error{
		fmt.Errorf("cannot decode synthetic-secret-must-not-leak"),
		fmt.Errorf("synthetic-secret-must-not-leak: %w", context.Canceled),
		fmt.Errorf("synthetic-secret-must-not-leak: %w", context.DeadlineExceeded),
	} {
		err := monitoringReadError("read component", cause)
		assert.NotContains(t, err.Error(), "synthetic-secret-must-not-leak")
		assert.Contains(t, err.Error(), "read component")
		for _, cancellation := range []error{context.Canceled, context.DeadlineExceeded} {
			if errors.Is(cause, cancellation) {
				assert.ErrorIs(t, err, cancellation)
			}
		}
	}
}

func TestMonitoringOwnedAdjunctDestroy(t *testing.T) {
	const componentID = "/subscriptions/sub/resourceGroups/adjunct-rg/providers/Microsoft.Insights/components/owned"
	for _, test := range []struct {
		name             string
		mutate           func(*monitoringCloudFixture, map[string]any, map[string]any)
		wantError        string
		wantDeletes      int
		preserveExternal bool
	}{
		{name: "owned generated connection is removed before the group", wantDeletes: 2},
		{name: "missing component does not strand its owned connection", wantDeletes: 2,
			mutate: func(f *monitoringCloudFixture, _, _ map[string]any) {
				f.groupResources = nil
				f.ownedComponent = nil
			}},
		{name: "missing group does not strand its owned connection", wantDeletes: 2,
			mutate: func(f *monitoringCloudFixture, _, _ map[string]any) {
				f.groupStatus = http.StatusNotFound
				f.resourceListStatus = http.StatusNotFound
				f.groupResources = nil
				f.ownedComponent = nil
			}},
		{name: "group ownership read failure blocks all deletes", wantError: "SyntheticReadFailure",
			mutate: func(f *monitoringCloudFixture, _, _ map[string]any) {
				f.groupStatus = http.StatusServiceUnavailable
			}},
		{name: "absent connection is idempotent", wantDeletes: 1,
			mutate: func(f *monitoringCloudFixture, _, _ map[string]any) { f.slotConnection = nil }},
		{name: "legacy connection blocks destructive cleanup", wantError: "legacy monitoring connection remains",
			mutate: func(f *monitoringCloudFixture, _, _ map[string]any) {
				f.legacySlotConnection = f.slotConnection
				f.slotConnection = nil
			}},
		{name: "modified authentication is preserved", wantError: "modified",
			mutate: func(_ *monitoringCloudFixture, p, _ map[string]any) { p["authType"] = "AAD" }},
		{name: "modified destination is preserved", wantError: "modified",
			mutate: func(_ *monitoringCloudFixture, p, _ map[string]any) { p["target"] = monitoringTestComponent }},
		{name: "modified creator is preserved", wantError: "modified",
			mutate: func(_ *monitoringCloudFixture, _, m map[string]any) { m["azd-created-by"] = "user" }},
		{name: "modified environment is preserved", wantError: "modified",
			mutate: func(_ *monitoringCloudFixture, _, m map[string]any) { m["azd-environment"] = "other" }},
		{name: "modified project is preserved", wantError: "modified",
			mutate: func(_ *monitoringCloudFixture, _, m map[string]any) { m["azd-monitoring-project"] = "other" }},
		{name: "modified sharing is preserved", wantError: "modified",
			mutate: func(_ *monitoringCloudFixture, p, _ map[string]any) { p["isSharedToAll"] = true }},
		{name: "resource metadata must match", wantError: "modified",
			mutate: func(_ *monitoringCloudFixture, _, m map[string]any) { m["ResourceId"] = monitoringTestComponent }},
		{name: "external workspace is preserved", wantError: "workspace was modified",
			mutate: func(f *monitoringCloudFixture, _, _ map[string]any) {
				f.ownedComponent["properties"] = map[string]any{"WorkspaceResourceId": monitoringTestWorkspace}
			}},
		{name: "external compatible monitoring is not owned", wantDeletes: 1, preserveExternal: true,
			mutate: func(f *monitoringCloudFixture, p, m map[string]any) {
				f.groupResources = nil
				f.ownedComponent = nil
				p["target"] = monitoringTestComponent
				m["azd-created-by"] = "user"
			}},
		{name: "another project component blocks group cleanup", wantError: "another project or environment",
			mutate: func(f *monitoringCloudFixture, _, _ map[string]any) {
				f.ownedComponent["tags"] = map[string]any{
					"azd-created-by": "azure.ai.projects", "azd-environment": "test", "azd-monitoring-project": "other",
				}
			}},
		{name: "incomplete discovery blocks all deletes", wantError: "HTTP 403",
			mutate: func(f *monitoringCloudFixture, _, _ map[string]any) { f.resourceListStatus = http.StatusForbidden }},
		{name: "opted-out registry requires its cleanup binding", wantError: "without its create-mode binding",
			mutate: func(f *monitoringCloudFixture, _, _ map[string]any) {
				f.groupResources = append(f.groupResources, map[string]any{
					"id":   "/subscriptions/sub/resourceGroups/adjunct-rg/providers/Microsoft.ContainerRegistry/registries/retained",
					"type": "Microsoft.ContainerRegistry/registries",
				})
			}},
		{name: "connection deletion failure preserves the group", wantError: "HTTP 503", wantDeletes: 1,
			mutate: func(f *monitoringCloudFixture, _, _ map[string]any) {
				f.connectionDeleteStatus = http.StatusServiceUnavailable
			}},
	} {
		t.Run(test.name, func(t *testing.T) {
			metadata := map[string]any{
				"ResourceId": componentID, "azd-created-by": "azure.ai.projects",
				"azd-environment": "test", "azd-monitoring-project": monitoringTestProject,
			}
			properties := map[string]any{
				"category": "AppInsights", "authType": "ApiKey", "target": componentID, "metadata": metadata,
			}
			component := map[string]any{
				"id": componentID, "name": "owned", "type": "Microsoft.Insights/components",
				"tags": map[string]any{
					"azd-created-by": "azure.ai.projects", "azd-environment": "test",
					"azd-monitoring-project": monitoringTestProject,
				},
				"properties": map[string]any{"WorkspaceResourceId": "/subscriptions/sub/resourceGroups/adjunct-rg/" +
					"providers/Microsoft.OperationalInsights/workspaces/owned"},
			}
			fixture := &monitoringCloudFixture{
				groupResources: []any{component}, ownedComponent: component,
				slotConnection: map[string]any{
					"id":   monitoringTestProject + "/connections/" + monitoringTestConnectionName,
					"name": monitoringTestConnectionName, "properties": properties,
				},
			}
			if test.mutate != nil {
				test.mutate(fixture, properties, metadata)
			}
			p := newMonitoringCloudFixture(t, fixture)
			p.brownfieldEndpoint = "https://account.services.ai.azure.com/api/projects/project"
			p.existingProjectID = monitoringTestProject
			p.existingAcrMode = "none"
			p.foundryRGOwnerID = "/subscriptions/sub/resourceGroups/adjunct-rg"
			p.rgExplicit = true
			result, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				assert.NotContains(t, err.Error(), "synthetic-secret-must-not-leak")
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				if test.preserveExternal {
					assert.NotContains(t, result.InvalidatedEnvKeys, "AZURE_APPLICATION_INSIGHTS_RESOURCE_ID")
				} else {
					assert.Contains(t, result.InvalidatedEnvKeys, "AZURE_APPLICATION_INSIGHTS_RESOURCE_ID")
				}
				assert.NotContains(t, result.InvalidatedEnvKeys, "AZURE_AI_PROJECT_ID")
			}
			require.Len(t, fixture.deletions, test.wantDeletes)
			if test.wantError == "" {
				assert.True(t, strings.EqualFold(
					"/subscriptions/sub/resourceGroups/adjunct-rg", fixture.deletions[len(fixture.deletions)-1]))
			}
		})
	}
}

func TestMonitoringSupportingResourceGroupOutputs(t *testing.T) {
	for _, mode := range []string{"create", "none", "already-connected"} {
		t.Run(mode, func(t *testing.T) {
			p := &FoundryProvisioningProvider{
				brownfieldEndpoint: "https://account.services.ai.azure.com/api/projects/project",
				subID:              "sub",
				rgName:             "adjunct-rg",
				foundryRGOwnerID:   "/subscriptions/sub/resourceGroups/adjunct-rg",
			}
			outputs := p.normalizeOutputs(map[string]*azdext.ProvisioningOutputParameter{
				"AZD_FOUNDRY_MONITORING_MODE": {Type: "string", Value: mode},
				envKeyFoundryRG:               {Type: "string", Value: ""},
			})
			require.Contains(t, outputs, envKeyFoundryRGOwner)
			assert.Equal(t, p.foundryRGOwnerID, outputs[envKeyFoundryRGOwner].Value)
			assert.Equal(t, p.rgName, outputs[envKeyFoundryRG].Value)
			assert.Equal(t, mode, outputs["AZD_FOUNDRY_MONITORING_MODE"].Value)
		})
	}
	t.Run("unowned monitoring creation does not fabricate ownership", func(t *testing.T) {
		p := &FoundryProvisioningProvider{
			brownfieldEndpoint: "https://account.services.ai.azure.com/api/projects/project",
			subID:              "sub",
			rgName:             "adjunct-rg",
		}
		outputs := p.normalizeOutputs(map[string]*azdext.ProvisioningOutputParameter{
			"AZD_FOUNDRY_MONITORING_MODE": {Type: "string", Value: "create"},
		})
		require.Contains(t, outputs, envKeyFoundryRGOwner)
		assert.Empty(t, outputs[envKeyFoundryRGOwner].Value)
	})
}

func TestMonitoringOnlyDeploymentTracksAdjunctOwnership(t *testing.T) {
	const groupID = "/subscriptions/sub/resourceGroups/adjunct-rg"
	for _, test := range []struct {
		name        string
		status      int
		persistFail bool
	}{
		{name: "successful ordinary deployment"},
		{name: "failed initial response retains partial ownership", status: http.StatusInternalServerError},
		{name: "ownership persistence failure is visible", status: http.StatusInternalServerError, persistFail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := &monitoringCloudFixture{
				deploymentStatus: test.status,
				plannedChanges: []any{
					map[string]any{"resourceId": groupID + "/providers/Microsoft.Insights/components/new", "changeType": "Create"},
					map[string]any{
						"resourceId": groupID + "/providers/Microsoft.OperationalInsights/workspaces/new", "changeType": "Create",
					},
				},
				deploymentResources: []any{map[string]any{"id": groupID}},
				deploymentOutputs: map[string]any{
					envKeyFoundryRG:               map[string]any{"type": "String", "value": "adjunct-rg"},
					"AZD_FOUNDRY_MONITORING_MODE": map[string]any{"type": "String", "value": "create"},
				},
			}
			env := &monitoringOwnershipEnvServer{resolveEnvStubEnvServer: resolveEnvStubEnvServer{
				envName: "test", get: map[string]string{},
			}, fail: test.persistFail}
			p := newMonitoringCloudFixture(t, fixture)
			p.azdClient = newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{})
			p.rgName = "stale-cached-group"
			p.brownfieldEndpoint = "https://account.services.ai.azure.com/api/projects/project"
			p.existingProjectID = monitoringTestProject
			p.onDiskSource = monitoringTestSource("create")
			p.onDiskSource.mode = templateModeBicep
			p.onDiskSource.parameters["resourceGroupName"] = map[string]any{"value": "adjunct-rg"}
			reads := 0
			p.resourceGroupState = func(context.Context) (map[string]*string, bool, error) {
				reads++
				assert.Equal(t, "adjunct-rg", p.rgName, "the submitted graph owns the resource-group parameter")
				return map[string]*string{"azd-env-name": new("test")}, reads > 1, nil
			}
			result, err := p.Deploy(t.Context(), func(string) {})
			require.Len(t, fixture.writes, 1)
			if test.status == 0 {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, groupID, result.Deployment.Outputs[envKeyFoundryRGOwner].Value)
			} else {
				require.Error(t, err)
				assert.Nil(t, result)
				assert.Equal(t, 2, reads, "recover ownership even if the SDK's initial deployment response fails")
				if test.persistFail {
					assert.Contains(t, err.Error(), "ownership could not be recorded")
					assert.Contains(t, err.Error(), "synthetic persistence failure")
					local, ok := errors.AsType[*azdext.LocalError](err)
					require.True(t, ok)
					assert.NotContains(t, local.Message, "synthetic-secret-must-not-leak")
				} else {
					assert.Equal(t, groupID, env.set[envKeyFoundryRGOwner])
					assert.Equal(t, "adjunct-rg", env.set[envKeyFoundryRG])
				}
			}
		})
	}
}

func TestMonitoringDeploymentRejectsChangedOwnedAdjunctGroup(t *testing.T) {
	const selectedGroup = "/subscriptions/sub/resourceGroups/different-group"
	fixture := &monitoringCloudFixture{
		plannedChanges: []any{
			map[string]any{
				"resourceId": selectedGroup + "/providers/Microsoft.Insights/components/new", "changeType": "Create",
			},
			map[string]any{
				"resourceId": selectedGroup + "/providers/Microsoft.OperationalInsights/workspaces/new", "changeType": "Create",
			},
		},
	}
	p := newMonitoringCloudFixture(t, fixture)
	p.brownfieldEndpoint = "https://account.services.ai.azure.com/api/projects/project"
	p.existingProjectID = monitoringTestProject
	p.foundryRGOwnerID = "/subscriptions/sub/resourceGroups/adjunct-rg"
	p.onDiskSource = monitoringTestSource("create")
	p.onDiskSource.mode = templateModeBicep
	p.onDiskSource.parameters["resourceGroupName"] = map[string]any{"value": "different-group"}
	p.resourceGroupState = func(context.Context) (map[string]*string, bool, error) {
		t.Error("conflicting ownership must be rejected before attempting to acquire another group")
		return nil, false, nil
	}

	result, err := p.Deploy(t.Context(), func(string) {})

	require.ErrorContains(t, err, "authored monitoring graph selects a different owned supporting resource group")
	assert.Nil(t, result)
	assert.Empty(t, fixture.writes)
	assert.Empty(t, fixture.deletions)
	assert.Equal(t, "adjunct-rg", p.rgName)
	assert.Equal(t, "/subscriptions/sub/resourceGroups/adjunct-rg", p.foundryRGOwnerID)
}

type monitoringOwnershipEnvServer struct {
	resolveEnvStubEnvServer
	fail    bool
	persist func(context.Context, string, string) error
}

func (s *monitoringOwnershipEnvServer) SetValue(
	ctx context.Context, request *azdext.SetEnvRequest,
) (*azdext.EmptyResponse, error) {
	if s.fail {
		return nil, fmt.Errorf("synthetic persistence failure")
	}
	if s.persist != nil {
		if err := s.persist(ctx, request.Key, request.Value); err != nil {
			return nil, err
		}
	}
	return s.resolveEnvStubEnvServer.SetValue(ctx, request)
}

func TestMonitoringOwnedAdjunctPersistsAcrossProviders(t *testing.T) {
	const (
		groupID     = "/subscriptions/sub/resourceGroups/adjunct-rg"
		componentID = groupID + "/providers/Microsoft.Insights/components/new"
		endpoint    = "https://account.services.ai.azure.com/api/projects/project"
	)
	for _, status := range []int{0, http.StatusInternalServerError} {
		t.Run(fmt.Sprintf("deployment status %d", status), func(t *testing.T) {
			root := t.TempDir()
			azdContext := azdcontext.NewAzdContextWithDirectory(root)
			store := environment.NewLocalFileDataStore(
				azdContext, config.NewFileConfigManager(config.NewManager()))
			manager, err := environment.NewManager(nil, azdContext, nil, store, nil)
			require.NoError(t, err)
			env := environment.New("test")
			for key, value := range map[string]string{
				envKeySubscriptionID: "sub", envKeyLocation: "westus3",
				"AZURE_AI_PROJECT_ID": monitoringTestProject, "FOUNDRY_PROJECT_ENDPOINT": endpoint,
			} {
				env.DotenvSet(key, value)
			}
			require.NoError(t, manager.Save(t.Context(), env))
			envServer := &monitoringOwnershipEnvServer{
				resolveEnvStubEnvServer: resolveEnvStubEnvServer{envName: "test", get: env.Dotenv()},
				persist: func(ctx context.Context, key, value string) error {
					current, err := store.Get(ctx, "test")
					if err != nil {
						return err
					}
					current.DotenvSet(key, value)
					return manager.Save(ctx, current)
				},
			}
			component := map[string]any{
				"id": componentID, "type": "Microsoft.Insights/components",
				"tags": map[string]any{
					"azd-created-by": "azure.ai.projects", "azd-environment": "test",
					"azd-monitoring-project": monitoringTestProject,
				},
				"properties": map[string]any{
					"WorkspaceResourceId": groupID + "/providers/Microsoft.OperationalInsights/workspaces/new",
				},
			}
			fixture := &monitoringCloudFixture{
				deploymentStatus:    status,
				deploymentResources: []any{map[string]any{"id": groupID}},
				deploymentOutputs: map[string]any{
					envKeyFoundryRG:               map[string]any{"type": "String", "value": "adjunct-rg"},
					"AZD_FOUNDRY_MONITORING_MODE": map[string]any{"type": "String", "value": "create"},
				},
				plannedChanges: []any{
					map[string]any{"resourceId": componentID, "changeType": "Create"},
					map[string]any{
						"resourceId": groupID + "/providers/Microsoft.OperationalInsights/workspaces/new",
						"changeType": "Create",
					},
				},
			}
			first := newMonitoringCloudFixture(t, fixture)
			first.projectPath = root
			first.azdClient = newResolveEnvTestClient(t, envServer, &resolveEnvStubPromptServer{})
			first.brownfieldEndpoint = endpoint
			first.existingProjectID = monitoringTestProject
			first.onDiskSource = monitoringTestSource("create")
			first.onDiskSource.mode = templateModeBicep
			first.onDiskSource.parameters["resourceGroupName"] = map[string]any{"value": "adjunct-rg"}
			reads := 0
			first.resourceGroupState = func(context.Context) (map[string]*string, bool, error) {
				reads++
				return map[string]*string{"azd-env-name": new("test")}, reads > 1, nil
			}
			deployed, err := first.Deploy(t.Context(), func(string) {})
			require.Len(t, fixture.writes, 1)
			if status == 0 {
				require.NoError(t, err)
				outputs := map[string]core.OutputParameter{}
				for key, value := range deployed.Deployment.Outputs {
					outputs[key] = core.OutputParameter{Type: "string", Value: value.Value}
				}
				require.NoError(t, core.UpdateEnvironment(t.Context(), outputs, env, manager))
			} else {
				require.Error(t, err)
			}
			persisted, err := store.Get(t.Context(), "test")
			require.NoError(t, err)
			require.Equal(t, groupID, persisted.Dotenv()[envKeyFoundryRGOwner])
			require.Equal(t, "adjunct-rg", persisted.Dotenv()[envKeyFoundryRG])
			fixture.mu.Lock()
			fixture.groupResources = []any{component}
			fixture.ownedComponent = component
			fixture.mu.Unlock()
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(
				"services:\n  project:\n    host: azure.ai.project\n    endpoint: "+endpoint+
					"\n    monitoring: {enabled: false}\n"), 0o600))

			second := newMonitoringCloudFixture(t, fixture)
			second.azdClient = newResolveEnvTestClient(t, &resolveEnvStubEnvServer{
				envName: "test", get: persisted.Dotenv(),
			}, &resolveEnvStubPromptServer{})
			require.NoError(t, second.Initialize(t.Context(), root,
				&azdext.ProvisioningOptions{Provider: FoundryProviderName}))
			require.False(t, second.existingProjectReuseOnly)
			require.Equal(t, groupID, second.foundryRGOwnerID)
			result, err := second.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, fixture.deletions, 1)
			assert.True(t, strings.EqualFold(groupID, fixture.deletions[0]))
			assert.NotContains(t, result.InvalidatedEnvKeys, "AZURE_AI_PROJECT_ID")
		})
	}
}

func TestMonitoringExplicitEnvironmentDoesNotDeleteDefaultOwnership(t *testing.T) {
	t.Setenv("AZD_ENVIRONMENT", "prod")
	const endpoint = "https://account.services.ai.azure.com/api/projects/project"
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(
		"services:\n  project:\n    host: azure.ai.project\n    endpoint: "+endpoint+
			"\n    monitoring: {enabled: false}\n"), 0o600))
	defaultEnv := &resolveEnvStubEnvServer{get: map[string]string{
		envKeySubscriptionID: "sub", envKeyLocation: "westus3",
		"AZURE_AI_PROJECT_ID": monitoringTestProject, "FOUNDRY_PROJECT_ENDPOINT": endpoint,
		envKeyFoundryRG: "adjunct-rg", envKeyFoundryRGOwner: "/subscriptions/sub/resourceGroups/adjunct-rg",
	}}
	selectedEnv := &resolveEnvStubEnvServer{get: map[string]string{
		envKeySubscriptionID: "sub", envKeyLocation: "westus3",
		"AZURE_AI_PROJECT_ID": monitoringTestProject, "FOUNDRY_PROJECT_ENDPOINT": endpoint,
	}}
	env := &resolveEnvStubEnvServer{
		envName: "test", environments: map[string]*resolveEnvStubEnvServer{
			"test": defaultEnv, "prod": selectedEnv,
		},
	}
	fixture := &monitoringCloudFixture{}
	p := newMonitoringCloudFixture(t, fixture)
	p.azdClient = newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{})
	require.NoError(t, p.Initialize(t.Context(), root,
		&azdext.ProvisioningOptions{Provider: FoundryProviderName}))

	result, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "prod", p.envName)
	assert.True(t, p.existingProjectReuseOnly)
	assert.Empty(t, p.foundryRGOwnerID)
	assert.Empty(t, result.InvalidatedEnvKeys)
	assert.Empty(t, fixture.deletions, "the selected environment does not own the default environment's group")
	assert.Empty(t, fixture.writes)
	assert.Empty(t, defaultEnv.set)
}

func TestMonitoringPartialOwnershipPersistsToExplicitEnvironment(t *testing.T) {
	t.Setenv("AZD_ENVIRONMENT", "prod")
	const groupID = "/subscriptions/sub/resourceGroups/adjunct-rg"
	defaultEnv := &resolveEnvStubEnvServer{get: map[string]string{}}
	selectedEnv := &resolveEnvStubEnvServer{get: map[string]string{}}
	env := &resolveEnvStubEnvServer{
		envName: "test", environments: map[string]*resolveEnvStubEnvServer{
			"test": defaultEnv, "prod": selectedEnv,
		},
	}
	fixture := &monitoringCloudFixture{
		deploymentStatus: http.StatusInternalServerError,
		plannedChanges: []any{
			map[string]any{"resourceId": groupID + "/providers/Microsoft.Insights/components/new", "changeType": "Create"},
			map[string]any{
				"resourceId": groupID + "/providers/Microsoft.OperationalInsights/workspaces/new", "changeType": "Create",
			},
		},
	}
	p := newMonitoringCloudFixture(t, fixture)
	p.azdClient = newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{})
	require.NoError(t, p.resolveEnvName(t.Context()))
	p.brownfieldEndpoint = "https://account.services.ai.azure.com/api/projects/project"
	p.existingProjectID = monitoringTestProject
	p.onDiskSource = monitoringTestSource("create")
	p.onDiskSource.mode = templateModeBicep
	p.onDiskSource.parameters["resourceGroupName"] = map[string]any{"value": "adjunct-rg"}
	reads := 0
	p.resourceGroupState = func(context.Context) (map[string]*string, bool, error) {
		reads++
		return map[string]*string{"azd-env-name": new("prod")}, reads > 1, nil
	}

	result, err := p.Deploy(t.Context(), func(string) {})

	require.Error(t, err)
	assert.Nil(t, result)
	require.Len(t, fixture.writes, 1, "deployment error: %v", err)
	assert.Equal(t, groupID, selectedEnv.set[envKeyFoundryRGOwner])
	assert.Equal(t, "adjunct-rg", selectedEnv.set[envKeyFoundryRG])
	assert.Empty(t, defaultEnv.set)
}

func TestMonitoringOptOutRetainsSupportingResourceGroupBinding(t *testing.T) {
	for _, test := range []struct {
		name      string
		owner     string
		readError bool
		wantReuse bool
	}{
		{name: "owned group remains accessible", owner: "/subscriptions/sub/resourceGroups/adjunct-rg"},
		{name: "unowned existing project stays reuse only", wantReuse: true},
		{name: "failed ownership read is not absence", readError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			const endpoint = "https://account.services.ai.azure.com/api/projects/project"
			raw := "services:\n  project:\n    host: azure.ai.project\n    endpoint: " + endpoint +
				"\n    monitoring:\n      enabled: false\n"
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(raw), 0600))
			env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{
				envKeySubscriptionID: "sub", envKeyLocation: "westus3",
				envKeyFoundryRG: "adjunct-rg", envKeyFoundryRGOwner: test.owner,
				"AZURE_AI_PROJECT_ID":      monitoringTestProject,
				"FOUNDRY_PROJECT_ENDPOINT": endpoint,
			}}
			if test.readError {
				env.getErr = map[string]error{envKeyFoundryRGOwner: fmt.Errorf("synthetic environment failure")}
			}
			p := &FoundryProvisioningProvider{
				azdClient: newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{}),
			}
			err := p.Initialize(t.Context(), root, &azdext.ProvisioningOptions{Provider: FoundryProviderName})
			if test.readError {
				require.ErrorContains(t, err, "synthetic environment failure")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.wantReuse, p.existingProjectReuseOnly)
			assert.Equal(t, test.owner, p.foundryRGOwnerID)
			if !test.wantReuse {
				assert.Equal(t, monitoringTestProject, p.existingProjectID)
				assert.Equal(t, "adjunct-rg", p.rgName)
				assert.True(t, p.rgExplicit)
			}
		})
	}
}
