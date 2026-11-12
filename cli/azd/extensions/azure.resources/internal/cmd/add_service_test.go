// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"azure.resources/internal/add"
	"azure.resources/internal/resources"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAddServicesUsesProjectRPC(t *testing.T) {
	host := startAddTestHost(t, &addPromptServer{})
	require.NoError(t, host.project.config.Set("services.existing", map[string]any{
		"project": ".", "language": "python", "host": "appservice",
	}))
	client, err := azdext.NewAzdClient()
	require.NoError(t, err)
	t.Cleanup(client.Close)

	current, err := loadProject(t.Context(), client.Project())
	require.NoError(t, err)
	loaded, err := parseProject(current, "application")
	require.NoError(t, err)
	service := map[string]any{
		"host": "containerapp", "language": "python", "project": "./src/api",
		"resourceName": "${SERVICE_API_NAME}", "resourceGroup": "${SERVICE_API_RESOURCE_GROUP}",
		"docker": map[string]any{"registry": "${SERVICE_API_REGISTRY_ENDPOINT}"},
		"custom": map[string]any{"enabled": true},
	}
	result := &add.Result{
		Resources: resources.Config{"api": {Type: resources.ContainerApp, Port: 8080}},
		Services:  map[string]map[string]any{"existing": loaded.services["existing"], "api": service},
	}
	layer, err := loaded.layerChange("application", result)
	require.NoError(t, err)
	requests, err := loaded.serviceChanges(result)
	require.NoError(t, err)
	_, err = client.Project().SetConfigValue(t.Context(), layer)
	require.NoError(t, err)
	require.NoError(t, saveAddServices(t.Context(), client.Project(), current, requests))
	require.Len(t, host.project.requests, 1, "unchanged services must not be sent")
	require.Equal(t, "services.api", host.project.requests[0].Path)
	require.Equal(t, service, host.project.requests[0].Section.AsMap())

	current, err = loadProject(t.Context(), client.Project())
	require.NoError(t, err)
	reloaded, err := parseProject(current, "application")
	require.NoError(t, err)
	require.Equal(t, service, reloaded.services["api"])
	require.Equal(t, loaded.services["existing"], reloaded.services["existing"])
	require.Equal(t, resources.ContainerApp, reloaded.config["api"].Type)
}

func TestAddServiceRPCFailureRestoresProject(t *testing.T) {
	host := startAddTestHost(t, &addPromptServer{})
	existingService := map[string]any{"project": ".", "language": "python", "host": "appservice"}
	require.NoError(t, host.project.config.Set("services.existing", existingService))
	require.NoError(t, host.project.config.Set("infra.layers", []any{map[string]any{"name": "core", "path": "infra"}}))
	host.project.err = status.Error(codes.PermissionDenied, "service update denied")
	client, err := azdext.NewAzdClient()
	require.NoError(t, err)
	t.Cleanup(client.Close)

	original, err := loadProject(t.Context(), client.Project())
	require.NoError(t, err)
	loaded, err := parseProject(original, "data")
	require.NoError(t, err)
	result := &add.Result{
		Resources: resources.Config{"cache": {Type: resources.Redis}},
		Services:  map[string]map[string]any{"api": {"host": "containerapp"}, "worker": {"host": "containerapp"}},
	}
	layer, err := loaded.layerChange("data", result)
	require.NoError(t, err)
	requests, err := loaded.serviceChanges(result)
	require.NoError(t, err)
	_, err = client.Project().SetConfigValue(t.Context(), layer)
	require.NoError(t, err)

	err = saveAddServices(t.Context(), client.Project(), original, requests)
	require.ErrorContains(t, err, "saving services.api")
	require.ErrorContains(t, err, "service update denied")
	require.ErrorContains(t, err, "azure.yaml was left unchanged")
	require.Len(t, host.project.requests, 1, "stop after the first failed service write")

	restored, err := loadProject(t.Context(), client.Project())
	require.NoError(t, err)
	require.Equal(t, original, restored)
}

func TestAddServiceRestoreFailureReportsManualCleanup(t *testing.T) {
	host := startAddTestHost(t, &addPromptServer{})
	host.project.err = status.Error(codes.PermissionDenied, "service update denied")
	host.project.unsetErr = status.Error(codes.Unavailable, "unset unavailable")
	client, err := azdext.NewAzdClient()
	require.NoError(t, err)
	t.Cleanup(client.Close)
	loaded := readAddTestProject(t, "name: example\n", "data")
	requests, err := loaded.serviceChanges(&add.Result{Services: map[string]map[string]any{
		"api": {"host": "containerapp"},
	}})
	require.NoError(t, err)

	err = saveAddServices(t.Context(), client.Project(), map[string]any{}, requests)
	require.ErrorContains(t, err, "service update denied")
	require.ErrorContains(t, err, "restoring azure.yaml also failed")
	require.ErrorContains(t, err, "unset unavailable")
}

func TestAddServicePayloadValidationBeforeSaving(t *testing.T) {
	loaded := readAddTestProject(t, "name: example\n", "data")
	for _, test := range []struct {
		name    string
		service map[string]any
	}{
		{name: "bad.name", service: map[string]any{"host": "containerapp"}},
		{name: "api", service: map[string]any{"unsupported": make(chan int)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := loaded.serviceChanges(&add.Result{Services: map[string]map[string]any{test.name: test.service}})
			require.Error(t, err)
			require.Empty(t, loaded.layers)
		})
	}
}

func TestAddHostCommandDelegatesServiceWriteToRPC(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("fastapi\n"), 0o600))
	prompts := &addPromptServer{hostProject: dir}
	saved, output, host, err := runAddTest(t, "name: example\n", []string{"--layer", "application"}, prompts)
	require.NoError(t, err)
	require.Len(t, host.project.requests, 1)
	request := host.project.requests[0]
	require.Contains(t, request.Path, "services.")
	service := request.Section.AsMap()
	require.Equal(t, "appservice", service["host"])
	require.Equal(t, "python", service["language"])
	require.Contains(t, service["resourceName"], "${SERVICE_")
	snapshot := readAddTestProject(t, saved, "application")
	require.Len(t, snapshot.layers, 1)
	require.Len(t, snapshot.services, 1)
	require.Len(t, host.project.layerRequests, 1)
	require.Equal(t, "infra.layers", host.project.layerRequests[0].Path)
	require.Contains(t, output, "Added service")
}

func TestAddHostCommandRestoresProjectAfterServiceRPCFailure(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("fastapi\n"), 0o600))
	prompts := &addPromptServer{
		hostProject: dir, serviceError: status.Error(codes.PermissionDenied, "service update denied"),
	}
	original := "name: example\n"
	saved, output, host, err := runAddTest(t, original, []string{"--layer", "application"}, prompts)
	require.ErrorContains(t, err, "saving services.")
	require.ErrorContains(t, err, "azure.yaml was left unchanged")
	require.Len(t, host.project.requests, 1)
	require.Equal(t, []string{"services", "infra"}, host.project.unsetRequests)
	require.Zero(t, host.writes)
	require.Empty(t, prompts.commands)
	require.NotContains(t, output, "azure.yaml updated:")
	require.Equal(t, original, saved, "the layer written before the failed service must be rolled back")
}

func TestAddLayerRPCFailureStopsBeforeServiceWrites(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("fastapi\n"), 0o600))
	prompts := &addPromptServer{
		hostProject: dir, layerError: status.Error(codes.PermissionDenied, "layer update denied"),
	}
	original := "name: example\n"
	saved, output, host, err := runAddTest(t, original, []string{"--layer", "application"}, prompts)
	require.ErrorContains(t, err, `saving infrastructure layer "application"`)
	require.ErrorContains(t, err, "layer update denied")
	require.Equal(t, original, saved)
	require.Len(t, host.project.layerRequests, 1)
	require.Empty(t, host.project.requests)
	require.Zero(t, host.writes)
	require.Empty(t, prompts.commands)
	require.NotContains(t, output, "azure.yaml updated:")
}

func TestAddReadRPCFailureStopsBeforePromptsOrWrites(t *testing.T) {
	prompts := &addPromptServer{readError: status.Error(codes.Unavailable, "project unavailable")}
	original := "name: example\n"
	saved, _, host, err := runAddTest(t, original, nil, prompts)
	require.ErrorContains(t, err, "reading project resources")
	require.ErrorContains(t, err, "project unavailable")
	require.Equal(t, original, saved)
	require.Zero(t, prompts.layerPrompts)
	require.Empty(t, prompts.confirmations)
	require.Empty(t, host.project.layerRequests)
	require.Empty(t, host.project.requests)
	require.Zero(t, host.writes)
}
