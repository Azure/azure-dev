// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"testing"

	"azure.resources/internal/add"
	"azure.resources/internal/resources"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func readAddTestProject(t *testing.T, source, layer string) *addProject {
	t.Helper()
	var raw map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(source), &raw))
	project, err := parseProject(raw, layer)
	require.NoError(t, err)
	return project
}

func TestParseProjectFindsResourcesDefaultWithoutChangingSelectedLayer(t *testing.T) {
	// Exercise both [data, platform] and [platform, data] so discovery cannot depend on layer order.
	for _, resourcesIndex := range []int{0, 1} {
		t.Run(fmt.Sprintf("resources at index %d", resourcesIndex), func(t *testing.T) {
			// data owns the resources config; platform is a separate Bicep layer.
			data := map[string]any{
				"name":     "data",
				"provider": "resources",
				"config": map[string]any{
					"cache": map[string]any{"type": "db.redis"},
				},
			}
			platform := map[string]any{"name": "platform", "provider": "bicep", "path": "infra/platform"}
			layers := []any{data, platform}
			if resourcesIndex == 1 {
				layers = []any{platform, data}
			}
			project := map[string]any{
				"infra": map[string]any{"layers": layers},
			}

			// Explicitly select platform, even though data is the existing resources layer.
			parsed, err := parseProject(project, "platform")
			require.NoError(t, err)

			// Discover data for defaulting, but keep the selected target and provider on platform.
			require.Equal(t, "data", parsed.resourcesLayer)
			// With two layers, platform is at the index opposite resourcesIndex.
			require.Equal(t, 1-resourcesIndex, parsed.index)
			require.Equal(t, platform, parsed.target)
			require.Equal(t, "bicep", parsed.provider)
			// Do not load data's Redis config into the selected Bicep layer's state.
			require.Empty(t, parsed.config)
			require.Equal(t, layers, parsed.layers)
		})
	}
}

func TestParseProjectLoadsOnlySelectedResourcesConfig(t *testing.T) {
	// Select data; the neighboring Bicep layer has config that is not a resources.Config.
	parsed := readAddTestProject(t, `infra:
  layers:
    - name: data
      provider: resources
      config:
        cache:
          type: db.redis
    - name: platform
      provider: bicep
      config:
        unrelated: true
`, "data")

	// Only the selected resources layer's Redis config should be decoded.
	require.Equal(t, "data", parsed.resourcesLayer)
	require.Equal(t, 0, parsed.index)
	require.Equal(t, "resources", parsed.provider)
	require.Equal(t, resources.Config{"cache": {Type: resources.Redis}}, parsed.config)
}

func TestParseProjectDoesNotInheritProvider(t *testing.T) {
	// The provider is on infra, not on data. Its unrelated config must not be decoded as resources.
	parsed := readAddTestProject(t, `infra:
  provider: resources
  layers:
    - name: data
      config:
        unrelated: true
`, "data")

	// data is still selected, but does not become a resources layer through inheritance.
	require.Equal(t, 0, parsed.index)
	require.Equal(t, "data", parsed.target["name"])
	require.Empty(t, parsed.resourcesLayer)
	require.Empty(t, parsed.provider)
	require.Empty(t, parsed.config)
}

func TestAddSummaryIncludesOnlyChangedResourcesAndServices(t *testing.T) {
	project := readAddTestProject(t, `name: example
infra:
  layers:
    - name: data
      provider: resources
      config:
        cache:
          type: db.redis
        api:
          type: host.containerapp
          port: 8080
services:
  api:
    host: containerapp
  unchanged:
    host: appservice
`, "data")
	result := &add.Result{
		Resources: resources.Config{
			"cache": project.config["cache"],
			"api":   {Type: resources.ContainerApp, Port: 8080, Uses: []string{"cache"}},
			"store": {Type: resources.Storage},
		},
		Services: map[string]map[string]any{
			"api":       {"host": "containerapp", "project": "./src/api"},
			"worker":    {"host": "containerapp"},
			"unchanged": project.services["unchanged"],
		},
		Environment: map[string]string{"SECRET": "do-not-print"},
	}
	require.Equal(t, "\nazure.yaml updated:\n"+
		"  Updated resources layer \"data\".\n"+
		"  Updated resource \"api\" (host.containerapp).\n"+
		"  Added resource \"store\" (storage).\n"+
		"  Updated service \"api\".\n"+
		"  Added service \"worker\".", project.summary("data", result))
	requests, err := project.serviceChanges(result)
	require.NoError(t, err)
	require.Len(t, requests, 2)
	require.Equal(t, "services.api", requests[0].Path)
	require.Equal(t, result.Services["api"], requests[0].Section.AsMap())
	require.Equal(t, "services.worker", requests[1].Path)
}

func TestAddCreatesLayerRPCPayloadWithoutMutatingProject(t *testing.T) {
	project := readAddTestProject(t, `name: example
infra:
  layers:
    - name: native
      provider: bicep
      path: infra/native
services:
  existing:
    host: appservice
`, defaultResourcesLayer)
	request, err := project.layerChange(defaultResourcesLayer, &add.Result{
		Resources: resources.Config{"cache": {Type: resources.Redis}},
	})
	require.NoError(t, err)
	require.Equal(t, "infra.layers", request.Path)
	require.Equal(t, []any{
		map[string]any{"name": "native", "provider": "bicep", "path": "infra/native"},
		map[string]any{
			"name": defaultResourcesLayer, "provider": "resources",
			"config": map[string]any{"cache": map[string]any{"type": "db.redis"}},
		},
	}, request.Value.AsInterface())
	require.Len(t, project.layers, 1)
	require.Nil(t, project.target)
	require.Equal(t, map[string]any{"host": "appservice"}, project.services["existing"])
}

func TestAddLayerRPCPayloadPreservesOtherFieldsAndEntries(t *testing.T) {
	project := readAddTestProject(t, `name: example
infra:
  layers:
    - name: data
      provider: resources
      dependsOn: [native]
      config:
        cache:
          type: db.redis
    - name: other
      provider: bicep
      path: infra/other
`, "data")
	request, err := project.layerChange("data", &add.Result{
		Resources: resources.Config{"storage": {Type: resources.Storage, StorageContainers: []string{"assets"}}},
	})
	require.NoError(t, err)
	require.Equal(t, []any{
		map[string]any{
			"name": "data", "provider": "resources", "dependsOn": []any{"native"},
			"config": map[string]any{
				"cache": map[string]any{"type": "db.redis"},
				"storage": map[string]any{
					"type": "storage", "containers": []any{"assets"},
				},
			},
		},
		map[string]any{"name": "other", "provider": "bicep", "path": "infra/other"},
	}, request.Value.AsInterface())
	require.Equal(t, resources.Config{"cache": {Type: resources.Redis}}, project.config)
	require.Equal(t, map[string]any{"cache": map[string]any{"type": "db.redis"}}, project.target["config"])
}

func TestAddReplacementDropsOldProviderFields(t *testing.T) {
	project := readAddTestProject(t, `name: example
infra:
  layers:
    - name: data
      provider: terraform
      path: infra/old
      config:
        unrelated: true
`, "data")
	request, err := project.layerChange("data", &add.Result{
		Resources: resources.Config{"storage": {Type: resources.Storage}},
	})
	require.NoError(t, err)
	require.Equal(t, []any{map[string]any{
		"name": "data", "provider": "resources",
		"config": map[string]any{"storage": map[string]any{"type": "storage"}},
	}}, request.Value.AsInterface())
	require.Equal(t, "terraform", project.provider)
	require.Equal(t, "infra/old", project.target["path"])
}

func TestAddRejectsUnsupportedProjectShapes(t *testing.T) {
	// Each fixture is valid YAML but violates a project shape or layer-selection rule.
	for _, test := range []struct {
		name, source, layer string
	}{
		{name: "invalid automatic layer", source: "infra:\n  layers:\n    - name: ../outside\n      provider: resources\n"},
		{name: "path traversal", source: "name: example\n", layer: "../outside"},
		{name: "legacy", source: "resources:\n  cache:\n    type: db.redis\n", layer: "data"},
		{name: "flat infra", source: "infra:\n  provider: bicep\n", layer: "data"},
		{name: "project layers", source: "layers: []\n", layer: "data"},
		{name: "bad infra", source: "infra: []\n", layer: "data"},
		{name: "bad layers", source: "infra:\n  layers: {}\n", layer: "data"},
		{name: "bad layer", source: "infra:\n  layers: [invalid]\n", layer: "data"},
		{name: "missing layer name", source: "infra:\n  layers:\n    - provider: bicep\n", layer: "data"},
		{name: "bad layer name", source: "infra:\n  layers:\n    - name: 123\n", layer: "data"},
		{name: "bad services", source: "services: []\n", layer: "data"},
		{name: "bad service", source: "services:\n  api: invalid\n", layer: "data"},
		{name: "duplicate layers", source: "infra:\n  layers:\n    - name: data\n    - name: data\n", layer: "data"},
		{name: "bad config", source: "infra:\n  layers:\n    - name: data\n      provider: resources\n      config: []\n",
			layer: "data"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var raw map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(test.source), &raw))
			// Reject during project parsing, before preparing any configuration changes.
			_, err := parseProject(raw, test.layer)
			require.Error(t, err)
		})
	}
}

func TestAddRejectsInvalidProposedResources(t *testing.T) {
	project := readAddTestProject(t, "name: example\n", "data")
	_, err := project.layerChange("data", &add.Result{
		Resources: resources.Config{"api": {Type: resources.ContainerApp}},
	})
	require.ErrorContains(t, err, "port")
	require.Empty(t, project.layers)
}
