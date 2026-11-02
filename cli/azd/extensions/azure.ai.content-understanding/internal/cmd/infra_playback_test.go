// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/armclient"
	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/infra"
	"azure.ai.content-understanding/internal/localio"
	"azure.ai.content-understanding/internal/testutil"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type infraPlaybackHost struct {
	*azdInfraHost
	transport  copyWireTransport
	credential *copyWireCredential
}

func (h *infraPlaybackHost) Capabilities(
	_ context.Context, snapshot infraSnapshot, request infra.PostRequest,
) (infra.PostCapabilities, infra.ModelCapabilities, error) {
	client, err := cuclient.NewRESTTransport(request.Endpoint, h.credential,
		&cuclient.TransportOptions{APIVersion: request.APIVersion, Transport: h.transport})
	if err != nil {
		return nil, nil, err
	}
	management, err := armclient.NewManagement(h.credential, config.ResourceScope{
		SubscriptionID: request.Subscription, ResourceGroup: request.ResourceGroup, ResourceName: request.Account,
	}, h.transport)
	if err != nil {
		return nil, nil, err
	}
	capabilities := &infraCapabilities{
		host: h.azdInfraHost, snapshot: snapshot, request: request, client: client, management: management,
	}
	return capabilities, capabilities, nil
}

func TestGeneratedProjectPostprovisionThroughHostAndRESTPlayback(t *testing.T) {
	_ = infraCommandFixture(t)
	rpc := &infraRPC{section: &azdext.GetConfigSectionResponse{}, writes: map[string]json.RawMessage{},
		subscriptions: []*azdext.Subscription{{Id: "sub", Name: "Subscription", TenantId: "tenant", IsDefault: true}}}
	_ = infraRPCClient(t, rpc)
	models := []string{"gpt-5.2", "text-embedding-3-large"}
	deployed := map[string]bool{}
	calls := []string{}
	defaultMappings := map[string]string{"custom-model": "keep-me"}
	credential := &copyWireCredential{}
	transport := copyWireTransport(func(request *http.Request) (*http.Response, error) {
		action := request.Method + " " + request.URL.Host + request.URL.Path
		calls = append(calls, action)
		assert.True(t, strings.HasPrefix(request.Header.Get("Authorization"), "Bearer "))
		assert.Empty(t, request.Header.Get("Ocp-Apim-Subscription-Key"))
		assert.NotContains(t, strings.ToLower(request.URL.Path), "listkeys")
		var body any
		code := 200
		if request.URL.Host == "management.azure.com" {
			assert.Equal(t, "2025-06-01", request.URL.Query().Get("api-version"))
			switch {
			case strings.HasSuffix(request.URL.Path, "/models"):
				rows := []any{}
				for _, name := range models {
					rows = append(rows, map[string]any{
						"name": name, "version": "1", "format": "OpenAI", "isDefaultVersion": true,
						"skus": []any{map[string]any{"name": "GlobalStandard", "capacity": map[string]int{"default": 1}}},
					})
				}
				body = map[string]any{"value": rows}
			case strings.HasSuffix(request.URL.Path, "/deployments"):
				rows := []any{}
				for _, name := range models {
					if deployed[name] {
						rows = append(rows, playbackDeployment(name))
					}
				}
				body = map[string]any{"value": rows}
			case request.Method == http.MethodPut:
				name := filepath.Base(request.URL.Path)
				assert.False(t, deployed[name])
				assert.Equal(t, "*", request.Header.Get("If-None-Match"))
				deployed[name] = true
				code, body = 201, playbackDeployment(name)
			default:
				body = playbackDeployment(filepath.Base(request.URL.Path))
			}
		} else {
			assert.Equal(t, "foundry.services.ai.azure.com", request.URL.Host)
			assert.Equal(t, cuclient.DefaultAPIVersion, request.URL.Query().Get("api-version"))
			switch {
			case strings.HasSuffix(request.URL.Path, "/analyzers/prebuilt-document"):
				body = map[string]any{"analyzerId": "prebuilt-document", "status": "ready",
					"supportedModels": map[string][]string{
						"completion": {"gpt-5.2"}, "embedding": {"text-embedding-3-large"},
					}}
			case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/defaults"):
				body = map[string]any{"modelDeployments": defaultMappings}
			case request.Method == http.MethodPatch && strings.HasSuffix(request.URL.Path, "/defaults"):
				var patch struct {
					Models map[string]string `json:"modelDeployments"`
				}
				require.NoError(t, json.NewDecoder(request.Body).Decode(&patch))
				defaultMappings = patch.Models
				body = map[string]any{"modelDeployments": defaultMappings}
			default:
				t.Fatalf("unexpected playback request: %s", action)
			}
		}
		wire, err := json.Marshal(body)
		require.NoError(t, err)
		return &http.Response{StatusCode: code, Request: request, Header: http.Header{},
			Body: io.NopCloser(bytes.NewReader(wire))}, nil
	})
	execute := func(args ...string) (string, error) {
		root := newRootCommandWithInfra(nil, func(cmd *cobra.Command, extCtx *azdext.ExtensionContext) (infraHost, error) {
			base, err := openInfraHost(cmd, extCtx)
			if err != nil {
				return nil, err
			}
			host, ok := base.(*azdInfraHost)
			require.True(t, ok)
			return &infraPlaybackHost{azdInfraHost: host, transport: transport, credential: credential}, nil
		})
		var stdout, stderr bytes.Buffer
		root.SetOut(&stdout)
		root.SetErr(&stderr)
		root.SetArgs(args)
		err := root.ExecuteContext(t.Context())
		assert.Empty(t, stderr.String())
		return stdout.String(), err
	}
	directory := filepath.Join(testutil.TempDir(t), "generated")
	_, err := execute("infra", "generate", "-d", directory, "-e", "dev", "--models", "recommended", "--no-prompt")
	require.NoError(t, err)
	assert.Empty(t, calls)
	env, err := localio.Read(filepath.Join(directory, ".azure/dev/.env"))
	require.NoError(t, err)
	assert.Contains(t, string(env), `CU_MODEL_SELECTION="recommended"`)
	rpc.mu.Lock()
	rpc.values = []*azdext.KeyValue{
		{Key: "FOUNDRY_ENDPOINT", Value: "https://foundry.services.ai.azure.com"},
		{Key: "FOUNDRY_RESOURCE_NAME", Value: "foundry"},
		{Key: "CU_ENDPOINT", Value: "https://foundry.services.ai.azure.com"},
		{Key: "AZURE_RESOURCE_GROUP", Value: "group"}, {Key: "AZURE_SUBSCRIPTION_ID", Value: "sub"},
		{Key: "AZURE_TENANT_ID", Value: "tenant"}, {Key: "CU_MODEL_SELECTION", Value: "recommended"},
	}
	rpc.mu.Unlock()
	t.Chdir(directory)
	stdout, err := execute("infra", "_postprovision-v1", "-e", "dev", "--no-prompt")
	require.NoError(t, err)
	var result infra.PostResult
	require.NoError(t, json.Unmarshal([]byte(stdout), &result))
	assert.True(t, result.GenerativeReady)
	assert.True(t, result.EnvironmentConfigured)
	assert.Equal(t, "completed", result.ModelSetup)
	assert.Len(t, deployed, 2)
	assert.Equal(t, "keep-me", defaultMappings["custom-model"])
	modelFile, err := localio.Read("infra/models.json")
	require.NoError(t, err)
	var saved []infra.Model
	require.NoError(t, json.Unmarshal(modelFile, &saved))
	assert.Len(t, saved, 2)
	rpc.mu.Lock()
	assert.JSONEq(t, `true`, string(rpc.writes["cu.modelSetupComplete"]))
	section := map[string]json.RawMessage{
		"modelSetupComplete": rpc.writes["cu.modelSetupComplete"],
		"modelDeployments":   rpc.writes["cu.modelDeployments"],
	}
	wire, err := json.Marshal(section)
	rpc.mu.Unlock()
	require.NoError(t, err)
	rpc.mu.Lock()
	rpc.section = &azdext.GetConfigSectionResponse{Found: true, Section: wire}
	rpc.mu.Unlock()
	before := len(calls)
	stdout, err = execute("infra", "_postprovision-v1", "-e", "dev", "--no-prompt")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(stdout), &result))
	assert.True(t, result.EnvironmentPreserved)
	assert.Equal(t, "skipped", result.ModelSetup)
	assert.Len(t, calls, before)
}

func playbackDeployment(name string) map[string]any {
	return map[string]any{"name": name, "properties": map[string]any{
		"provisioningState": "Succeeded", "model": map[string]string{"name": name, "version": "1", "format": "OpenAI"},
	}}
}

func TestPostprovisionUsesExplicitHookEnvironmentInsteadOfDefault(t *testing.T) {
	_ = infraCommandFixture(t)
	project, fixture := materializeContractProject(t)
	t.Chdir(project)
	t.Setenv("AZURE_ENV_NAME", "prod")
	t.Setenv("AZD_ENVIRONMENT", "dev")
	rpc := &infraRPC{
		defaultEnvironment: "dev",
		section:            &azdext.GetConfigSectionResponse{},
		writes:             map[string]json.RawMessage{},
		values: []*azdext.KeyValue{
			{Key: "CU_ENDPOINT", Value: "https://prod-foundry.services.ai.azure.com"},
			{Key: "FOUNDRY_RESOURCE_NAME", Value: "prod-foundry"},
			{Key: "AZURE_SUBSCRIPTION_ID", Value: "prod-sub"},
			{Key: "AZURE_RESOURCE_GROUP", Value: "prod-group"},
			{Key: "AZURE_TENANT_ID", Value: "prod-tenant"},
			{Key: "CU_MODEL_SELECTION", Value: "none"},
		},
	}
	_ = infraRPCClient(t, rpc)
	requests := 0
	transport := copyWireTransport(func(request *http.Request) (*http.Response, error) {
		requests++
		assert.Equal(t, http.MethodGet, request.Method)
		assert.Equal(t, "management.azure.com", request.URL.Host)
		assert.Equal(t,
			"/subscriptions/prod-sub/resourceGroups/prod-group/providers/Microsoft.CognitiveServices/"+
				"accounts/prod-foundry/deployments", request.URL.Path)
		return &http.Response{StatusCode: http.StatusOK, Request: request, Header: http.Header{},
			Body: io.NopCloser(strings.NewReader(`{"value":[]}`))}, nil
	})
	root := newRootCommandWithInfra(nil, func(cmd *cobra.Command, extCtx *azdext.ExtensionContext) (infraHost, error) {
		base, err := openInfraHost(cmd, extCtx)
		if err != nil {
			return nil, err
		}
		host, ok := base.(*azdInfraHost)
		require.True(t, ok)
		return &infraPlaybackHost{azdInfraHost: host, transport: transport, credential: &copyWireCredential{}}, nil
	})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append(fixture.NativeHookArguments[2:], "--no-prompt"))
	require.NoError(t, root.ExecuteContext(t.Context()))
	assert.Empty(t, stderr.String())
	var result infra.PostResult
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
	assert.True(t, result.Succeeded)
	assert.True(t, result.EnvironmentConfigured)
	assert.Equal(t, 1, requests)
	rpc.mu.Lock()
	defer rpc.mu.Unlock()
	assert.NotContains(t, rpc.calls, "current", "the hook must not select the project default")
	assert.Contains(t, rpc.calls, "get:prod")
	assert.Contains(t, rpc.calls, "values:prod")
	assert.Contains(t, rpc.calls, "config:prod:cu")
	assert.Contains(t, rpc.calls, "env:prod:CU_ENDPOINT:https://prod-foundry.services.ai.azure.com")
	assert.Contains(t, rpc.calls, "env:prod:CU_DEFAULT_ANALYZER:prebuilt-layout")
	for _, call := range rpc.calls {
		assert.NotContains(t, call, ":dev", "postprovision must not read or write the default environment")
	}
}
