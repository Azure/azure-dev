// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"os"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	extensionyaml "go.yaml.in/yaml/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestBetaApplyServiceConfigAcceptsReserializedNumericSnapshot(t *testing.T) {
	for _, operation := range []string{"added", "unchanged-service", "stale-large-integer"} {
		t.Run(operation, func(t *testing.T) {
			override, _, path := newBetaProjectServiceOverrideFixture(t)
			manifest := "name: test\nservices:\n  worker:\n    host: containerapp\n    language: python\n" +
				"    config:\n      zero: 0.0\n      one: 1.0\n      negativeZero: -0.0\n" +
				"      large: 9007199254740993\n"
			require.NoError(t, os.WriteFile(path, []byte(manifest), 0o600))
			var snapshot map[string]any
			require.NoError(t, extensionyaml.Unmarshal([]byte(manifest), &snapshot))
			expected, err := extensionyaml.Marshal(snapshot["services"])
			require.NoError(t, err)
			if operation == "stale-large-integer" {
				manifest = strings.ReplaceAll(manifest, "9007199254740993", "9007199254740992")
				require.NoError(t, os.WriteFile(path, []byte(manifest), 0o600))
			}
			request := &v1beta.ApplyServiceConfigRequest{
				ServiceName: "search", ExpectedServicesYaml: string(expected),
				ServiceYaml: "host: azure.ai.connection\ntarget: https://search.example.test\n",
			}
			if operation == "unchanged-service" {
				services, ok := snapshot["services"].(map[string]any)
				require.True(t, ok)
				candidate, err := extensionyaml.Marshal(services["worker"])
				require.NoError(t, err)
				request.ServiceName = "worker"
				request.ServiceYaml = string(candidate)
			}
			_, err = override.ApplyServiceConfig(t.Context(), request)
			saved, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			if operation == "stale-large-integer" {
				require.Equal(t, codes.Aborted, status.Code(err))
				assert.Equal(t, manifest, string(saved))
			} else {
				require.NoError(t, err)
				if operation == "unchanged-service" {
					assert.Equal(t, manifest, string(saved))
				} else {
					assert.Contains(t, string(saved), "search:")
				}
				assert.Contains(t, string(saved), "9007199254740993")
			}
		})
	}
}

func TestBetaApplyServiceConfigPreservesRawValues(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "added", true: "updated"}[existing], func(t *testing.T) {
			override, service, path := newBetaProjectServiceOverrideFixture(t)
			services := map[string]any{
				"worker.api": map[string]any{
					"host": "containerapp", "language": "python",
					"config": map[string]any{"number": uint64(9007199254740993)},
				},
			}
			if existing {
				services["search.api"] = map[string]any{"host": "azure.ai.connection", "target": "old"}
			}
			manifest, err := yaml.Marshal(map[string]any{"name": "test", "services": services})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, manifest, 0o600))
			expected, err := yaml.Marshal(services)
			require.NoError(t, err)
			req := &v1beta.ApplyServiceConfigRequest{
				ServiceName:          "search.api",
				ExpectedServicesYaml: string(expected),
				ServiceYaml: "host: azure.ai.connection\n$ref: ./connection.yaml\n" +
					"env:\n  KEY: ${SEARCH_KEY}\nconfig:\n  number: 9007199254740993\n",
			}
			_, err = override.ApplyServiceConfig(t.Context(), req)
			require.NoError(t, err)
			saved, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Contains(t, string(saved), "9007199254740993")
			assert.NotContains(t, string(saved), "9007199254740992")
			assert.Contains(t, string(saved), "${SEARCH_KEY}")
			cfg, err := project.LoadConfig(t.Context(), path)
			require.NoError(t, err)
			actual, ok := cfg.Raw()["services"].(map[string]any)
			require.True(t, ok)
			assert.Contains(t, actual, "worker.api")
			assert.Contains(t, actual, "search.api")
			assert.NotContains(t, actual, "search")
			worker, ok := actual["worker.api"].(map[string]any)
			require.True(t, ok)
			search, ok := actual["search.api"].(map[string]any)
			require.True(t, ok)
			for _, svc := range []map[string]any{worker, search} {
				config, ok := svc["config"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, int(9007199254740993), config["number"])
			}
			cached, err := service.lazyProjectConfig.GetValue()
			require.NoError(t, err)
			require.Contains(t, cached.Services, "search.api")

			req.ExpectedServicesYaml = string(mustAuthoringYAML(t, actual))
			before := string(saved)
			_, err = override.ApplyServiceConfig(t.Context(), req)
			require.NoError(t, err)
			saved, err = os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, string(saved), "unchanged service must not rewrite the manifest")
		})
	}
}

func mustAuthoringYAML(t *testing.T, value any) []byte {
	t.Helper()
	wire, err := yaml.Marshal(value)
	require.NoError(t, err)
	return wire
}

func TestBetaApplyServiceConfigPreservesExplicitEmptyMappings(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "added", true: "updated"}[existing], func(t *testing.T) {
			override, service, path := newBetaProjectServiceOverrideFixture(t)
			services := map[string]any{
				"isolated": map[string]any{"host": "azure.ai.connection", "env": map[string]any{}},
				"fallback": map[string]any{"host": "azure.ai.connection"},
				"bound": map[string]any{
					"host": "azure.ai.connection", "env": map[string]any{"KEY": "${KEY}"},
				},
			}
			if existing {
				services["search"] = map[string]any{"host": "azure.ai.connection", "target": "old"}
			}
			manifest := map[string]any{"name": "test", "infra": map[string]any{}, "services": services}
			require.NoError(t, os.WriteFile(path, mustAuthoringYAML(t, manifest), 0o600))
			_, err := override.ApplyServiceConfig(t.Context(), &v1beta.ApplyServiceConfigRequest{
				ServiceName: "search", ExpectedServicesYaml: string(mustAuthoringYAML(t, services)),
				ServiceYaml: "host: azure.ai.connection\nenv: {}\n",
			})
			require.NoError(t, err)
			services["search"] = map[string]any{"host": "azure.ai.connection", "env": map[string]any{}}
			saved, err := project.LoadConfig(t.Context(), path)
			require.NoError(t, err)
			assert.Equal(t, manifest, saved.Raw())
			for _, name := range []string{"isolated", "search"} {
				response, err := service.GetServiceConfigValue(t.Context(), &azdext.GetServiceConfigValueRequest{
					ServiceName: name, Path: "env",
				})
				require.NoError(t, err)
				assert.True(t, response.GetFound(), "explicit empty env must retain isolation")
			}
		})
	}
}

func TestBetaApplyServiceConfigValidatesBeforeRawSave(t *testing.T) {
	override, _, path := newBetaProjectServiceOverrideFixture(t)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	_, err = override.ApplyServiceConfig(t.Context(), &v1beta.ApplyServiceConfigRequest{
		ServiceName: "worker", ExpectedServicesYaml: "{}", ServiceYaml: "host: containerapp\n",
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestBetaApplyServiceConfigRejectsInterveningWritesUnderLock(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "added", true: "updated"}[existing], func(t *testing.T) {
			override, service, path := newBetaProjectServiceOverrideFixture(t)
			expected := "{}"
			if existing {
				expected = "search.api:\n  host: azure.ai.connection\n  target: old\n"
			}
			req := &v1beta.ApplyServiceConfigRequest{
				ServiceName: "search.api", ExpectedServicesYaml: expected,
				ServiceYaml: "host: azure.ai.connection\ntarget: candidate\n",
			}
			service.configMutationMu.Lock()
			result := make(chan error, 1)
			go func() {
				_, err := override.ApplyServiceConfig(t.Context(), req)
				result <- err
			}()
			latest := []byte("name: test\nservices:\n  search.api:\n    host: azure.ai.connection\n    target: latest\n")
			err := os.WriteFile(path, latest, 0o600)
			service.configMutationMu.Unlock()
			require.NoError(t, err)
			require.Equal(t, codes.Aborted, status.Code(<-result))
			actual, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, latest, actual)
			require.True(t, service.configMutationMu.TryLock())
			service.configMutationMu.Unlock()
		})
	}
}

func TestBetaApplyServiceConfigRejectsInvalidYAMLWithoutDisclosure(t *testing.T) {
	for _, invalid := range []string{
		"", "null", "[]", "secret: [private-marker", "secret: private-marker\n---\n{}", "{a: 1, a: 2}",
	} {
		t.Run(invalid, func(t *testing.T) {
			override, _, path := newBetaProjectServiceOverrideFixture(t)
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			for _, expected := range []bool{false, true} {
				req := &v1beta.ApplyServiceConfigRequest{
					ServiceName: "search", ExpectedServicesYaml: "{}", ServiceYaml: "host: azure.ai.connection",
				}
				if expected {
					req.ExpectedServicesYaml = invalid
				} else {
					req.ServiceYaml = invalid
				}
				_, err = override.ApplyServiceConfig(t.Context(), req)
				require.Equal(t, codes.InvalidArgument, status.Code(err))
				assert.NotContains(t, err.Error(), "private-marker")
			}
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}

func TestBetaApplyServiceConfigFocusedAdapter(t *testing.T) {
	override, _, _ := newBetaProjectServiceOverrideFixture(t)
	adapter := &betaProjectServiceAdapter{
		override: betaOverrideChain{struct{}{}, override},
	}
	_, err := adapter.ApplyServiceConfig(t.Context(), &v1beta.ApplyServiceConfigRequest{
		ServiceName: "search", ExpectedServicesYaml: "{}", ServiceYaml: "host: azure.ai.connection",
	})
	require.NoError(t, err)
	adapter.override = nil
	_, err = adapter.ApplyServiceConfig(t.Context(), &v1beta.ApplyServiceConfigRequest{})
	assert.Equal(t, codes.Unimplemented, status.Code(err))
}

func TestServerE2E_BetaApplyServiceConfigComposesAndSerializes(t *testing.T) {
	server, service := newProjectE2EServer(t, WithBetaServiceOverride(BetaProjectService, focusedProjectGet{}))
	client, ctx := dialProjectSDK(t, server)
	response, err := client.ProjectBeta().Get(ctx, &v1beta.EmptyRequest{})
	require.NoError(t, err)
	assert.Equal(t, "focused", response.GetProject().GetName())
	results := make(chan error, 2)
	for _, target := range []string{"first", "second"} {
		go func() {
			_, err := client.ProjectBeta().ApplyServiceConfig(ctx, &v1beta.ApplyServiceConfigRequest{
				ServiceName: "search.api", ExpectedServicesYaml: "{}",
				ServiceYaml: "host: azure.ai.connection\ntarget: " + target,
			})
			results <- err
		}()
	}
	codesSeen := map[codes.Code]int{}
	for range 2 {
		codesSeen[status.Code(<-results)]++
	}
	assert.Equal(t, map[codes.Code]int{codes.OK: 1, codes.Aborted: 1}, codesSeen)
	azdContext, err := service.lazyAzdContext.GetValue()
	require.NoError(t, err)
	saved, err := project.LoadConfig(t.Context(), azdContext.ProjectPath())
	require.NoError(t, err)
	services, ok := saved.Raw()["services"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, services, "search.api")
	assert.NotContains(t, services, "search")
}
