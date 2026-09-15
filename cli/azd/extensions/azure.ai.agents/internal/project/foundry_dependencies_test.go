// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/envkey"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestValidateRegistryConnectionDependency(t *testing.T) {
	t.Parallel()

	agent := func(uses ...string) *azdext.ServiceConfig {
		return &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost, Uses: uses}
	}
	connection := &azdext.ServiceConfig{Name: "private-registry", Host: foundryConnectionHost}

	t.Run("external connection reference does not require uses", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, validateRegistryConnectionDependency(
			t.Context(), agent(), "/connections/external-registry", nil, "", nil,
		))
	})

	t.Run("sibling connection requires uses", func(t *testing.T) {
		t.Parallel()
		err := validateRegistryConnectionDependency(
			t.Context(), agent(), "private-registry",
			map[string]*azdext.ServiceConfig{"private-registry": connection}, "", nil,
		)
		require.ErrorContains(t, err, "is not declared")
		require.ErrorContains(t, err, "uses")
	})

	t.Run("sibling connection with uses is valid", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, validateRegistryConnectionDependency(
			t.Context(), agent("private-registry"), "private-registry",
			map[string]*azdext.ServiceConfig{"private-registry": connection}, "", nil,
		))
	})

	t.Run("sibling service must be a Foundry connection", func(t *testing.T) {
		t.Parallel()
		err := validateRegistryConnectionDependency(
			t.Context(), agent("private-registry"), "private-registry",
			map[string]*azdext.ServiceConfig{
				"private-registry": {Name: "private-registry", Host: foundryToolboxHost},
			}, "", nil,
		)
		require.ErrorContains(t, err, foundryConnectionHost)
	})

	t.Run("disabled sibling connection is rejected", func(t *testing.T) {
		t.Parallel()
		err := validateRegistryConnectionDependency(
			t.Context(), agent("private-registry"), "private-registry",
			map[string]*azdext.ServiceConfig{"private-registry": connection}, "",
			func(context.Context, string) (bool, error) { return false, nil },
		)
		require.ErrorContains(t, err, "disabled")
	})

	t.Run("condition error is returned unchanged", func(t *testing.T) {
		t.Parallel()
		expected := exterrors.Dependency(
			exterrors.CodeFoundryDependencyNotReady, "condition lookup failed", "check the service condition",
		)
		err := validateRegistryConnectionDependency(
			t.Context(), agent("private-registry"), "private-registry",
			map[string]*azdext.ServiceConfig{"private-registry": connection}, "",
			func(context.Context, string) (bool, error) { return false, expected },
		)
		require.Same(t, expected, err)
	})
}

func TestValidateRegistryConnectionDependencyPayloadName(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"inline", "config", "empty inline"} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			props, err := MarshalStruct(new(map[string]any{"name": " production-registry "}))
			require.NoError(t, err)
			// The dependency identity must come from the map key, not either name field.
			connection := &azdext.ServiceConfig{Name: "not-the-map-key", Host: foundryConnectionHost}
			if source == "inline" {
				connection.AdditionalProperties = props
				connection.Config, err = MarshalStruct(new(map[string]any{"name": "ignored-config-name"}))
				require.NoError(t, err)
			} else {
				connection.Config = props
				if source == "empty inline" {
					connection.AdditionalProperties, err = MarshalStruct(new(map[string]any{}))
					require.NoError(t, err)
				}
			}
			services := map[string]*azdext.ServiceConfig{"private-registry": connection}
			for _, tt := range []struct {
				name    string
				ref     string
				uses    []string
				enabled bool
				wantErr string
			}{
				{name: "missing uses", ref: "production-registry", wantErr: "is not declared"},
				{
					name: "payload name is not a uses key", ref: "production-registry",
					uses: []string{"production-registry"}, wantErr: "is not declared",
				},
				{
					name: "enabled", ref: " production-registry ",
					uses: []string{"private-registry"}, enabled: true,
				},
				{
					name: "disabled", ref: "production-registry",
					uses: []string{"private-registry"}, wantErr: "disabled",
				},
				{
					name: "overridden service key rejected", ref: "private-registry",
					uses: []string{"private-registry"}, enabled: true, wantErr: "whose Connection name",
				},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					agent := &azdext.ServiceConfig{Name: "agent", Uses: tt.uses}
					var checked []string
					err := validateRegistryConnectionDependency(
						t.Context(), agent, tt.ref, services, "",
						func(_ context.Context, key string) (bool, error) {
							checked = append(checked, key)
							return tt.enabled, nil
						},
					)
					if tt.wantErr != "" && tt.wantErr != "disabled" {
						require.Empty(t, checked)
					} else {
						require.Equal(t, []string{"private-registry"}, checked)
					}
					if tt.wantErr == "" {
						require.NoError(t, err)
						return
					}
					localErr, ok := errors.AsType[*azdext.LocalError](err)
					require.True(t, ok)
					require.Equal(t, exterrors.CodeFoundryDependencyNotReady, localErr.Code)
					require.Equal(t, azdext.LocalErrorCategoryDependency, localErr.Category)
					require.Contains(t, localErr.Message, tt.wantErr)
					require.Contains(t, localErr.Message, `"private-registry"`)
					if tt.wantErr == "is not declared" {
						require.Contains(t, localErr.Suggestion, `add "private-registry"`)
					}
					if tt.wantErr == "whose Connection name" {
						require.Contains(t, localErr.Suggestion, `registryConnectionId to "production-registry"`)
						require.Contains(t, localErr.Suggestion, `"private-registry" in the agent uses`)
					}
				})
			}
		})
	}
}

func TestValidateRegistryConnectionDependencyExternalWithLocalServices(t *testing.T) {
	t.Parallel()
	props, err := MarshalStruct(new(map[string]any{"name": "production-registry"}))
	require.NoError(t, err)
	config, err := MarshalStruct(new(map[string]any{"name": "ignored-config-name", "$ref": "must-not-read.yaml"}))
	require.NoError(t, err)
	toolboxProps, err := MarshalStruct(new(map[string]any{"name": "external-registry", "$ref": "must-not-read.yaml"}))
	require.NoError(t, err)
	services := map[string]*azdext.ServiceConfig{
		"private-registry": {Host: foundryConnectionHost, AdditionalProperties: props, Config: config},
		"toolbox":          {Host: foundryToolboxHost, AdditionalProperties: toolboxProps},
		"project":          {Host: foundryProjectHost, AdditionalProperties: toolboxProps},
		"agent":            {Host: foundryAgentHost, Config: toolboxProps},
	}
	for _, ref := range []string{"", "  ", "external-registry", "ignored-config-name", "/connections/production-registry"} {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()
			called := false
			require.NoError(t, validateRegistryConnectionDependency(
				t.Context(), &azdext.ServiceConfig{Name: "agent"}, ref, services, "",
				func(context.Context, string) (bool, error) { called = true; return false, nil },
			))
			require.False(t, called)
		})
	}
}

func TestValidateRegistryConnectionDependencyAmbiguousNames(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"another-registry", "production-registry"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			props, err := MarshalStruct(new(map[string]any{"name": " production-registry "}))
			require.NoError(t, err)
			services := map[string]*azdext.ServiceConfig{
				key:                {Host: foundryConnectionHost, AdditionalProperties: props},
				"private-registry": {Host: foundryConnectionHost, Config: props},
			}
			agent := &azdext.ServiceConfig{Name: "agent", Uses: []string{key, "private-registry"}}
			called := false
			err = validateRegistryConnectionDependency(
				t.Context(), agent, "production-registry", services, "",
				func(context.Context, string) (bool, error) { called = true; return true, nil },
			)
			require.False(t, called)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, exterrors.CodeFoundryDependencyNotReady, localErr.Code)
			require.Equal(t, azdext.LocalErrorCategoryDependency, localErr.Category)
			require.Contains(t, localErr.Message, "ambiguous")
			require.Contains(t, localErr.Message, key)
			require.Contains(t, localErr.Message, "private-registry")
		})
	}
}

func TestValidateRegistryConnectionDependencyWrongHostKeyTakesPrecedence(t *testing.T) {
	t.Parallel()
	props, err := MarshalStruct(new(map[string]any{"name": "production-registry"}))
	require.NoError(t, err)
	services := map[string]*azdext.ServiceConfig{
		"production-registry": {Host: foundryToolboxHost},
		"private-registry":    {Host: foundryConnectionHost, AdditionalProperties: props},
	}
	err = validateRegistryConnectionDependency(
		t.Context(), &azdext.ServiceConfig{Name: "agent", Uses: []string{"private-registry"}},
		"production-registry", services, "", nil,
	)
	require.ErrorContains(t, err, `resolves to service host "azure.ai.toolbox" instead of "azure.ai.connection"`)
}

func TestValidateRegistryConnectionDependencyKeyAndEffectiveNameCollision(t *testing.T) {
	t.Parallel()
	first, err := MarshalStruct(new(map[string]any{"name": "different-registry"}))
	require.NoError(t, err)
	second, err := MarshalStruct(new(map[string]any{"name": "private-registry"}))
	require.NoError(t, err)
	services := map[string]*azdext.ServiceConfig{
		"private-registry": {Host: foundryConnectionHost, AdditionalProperties: first},
		"other-registry":   {Host: foundryConnectionHost, AdditionalProperties: second},
	}
	checked := false
	err = validateRegistryConnectionDependency(
		t.Context(), &azdext.ServiceConfig{Name: "agent", Uses: []string{"private-registry", "other-registry"}},
		"private-registry", services, "",
		func(context.Context, string) (bool, error) { checked = true; return true, nil },
	)
	require.ErrorContains(t, err, "ambiguous")
	require.False(t, checked, "a key alias must not shadow another service's effective resource name")
}

func TestValidateRegistryConnectionDependencySameEffectiveName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", " \t", "private-registry", " PRIVATE-REGISTRY "} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			props, err := MarshalStruct(new(map[string]any{"name": name}))
			require.NoError(t, err)
			require.NoError(t, validateRegistryConnectionDependency(
				t.Context(), &azdext.ServiceConfig{Name: "agent", Uses: []string{"private-registry"}},
				"private-registry", map[string]*azdext.ServiceConfig{
					"private-registry": {Host: foundryConnectionHost, AdditionalProperties: props},
				}, "", nil,
			))
		})
	}
}

func TestValidateRegistryConnectionDependencyFileReferences(t *testing.T) {
	t.Parallel()
	for _, file := range []struct {
		name    string
		content string
	}{
		{"registry.yaml", "name: ' file-registry '\ncategory: ContainerRegistry\nauthType: ApiKey\n"},
		{"registry.json", `{"name":" file-registry ","category":"ContainerRegistry","authType":"ApiKey"}`},
	} {
		for _, source := range []string{"inline", "config", "empty inline"} {
			for _, overlay := range []string{"", "overlay-registry"} {
				t.Run(file.name+"/"+source+"/name="+overlay, func(t *testing.T) {
					t.Parallel()
					root := t.TempDir()
					require.NoError(t, os.WriteFile(filepath.Join(root, file.name), []byte(file.content), 0o600))
					values := map[string]any{"$ref": file.name}
					name := "file-registry"
					if overlay != "" {
						values["name"] = " " + overlay + " "
						name = overlay
					}
					props, err := MarshalStruct(&values)
					require.NoError(t, err)
					service := &azdext.ServiceConfig{
						Name: "not-the-map-key", Host: foundryConnectionHost,
						Uses: []string{"project"}, Environment: map[string]string{"KEY": "unchanged"},
					}
					if source == "inline" {
						service.AdditionalProperties = props
						service.Config, err = MarshalStruct(new(map[string]any{"$ref": "must-not-read.yaml"}))
						require.NoError(t, err)
					} else {
						service.Config = props
						if source == "empty inline" {
							service.AdditionalProperties, err = MarshalStruct(new(map[string]any{}))
							require.NoError(t, err)
						}
					}
					before := proto.Clone(service)
					services := map[string]*azdext.ServiceConfig{"private-registry": service}
					err = validateRegistryConnectionDependency(
						t.Context(), &azdext.ServiceConfig{Name: "agent", Uses: []string{"private-registry"}},
						"private-registry", services, root, nil,
					)
					require.ErrorContains(t, err, "whose Connection name")
					require.ErrorContains(t, err, name)
					require.True(t, proto.Equal(before, service), "validation must not rewrite the definition")
					for _, tt := range []struct {
						name    string
						uses    []string
						enabled bool
						wantErr string
					}{
						{name: "missing uses", wantErr: "is not declared"},
						{name: "payload name is not a uses key", uses: []string{name}, wantErr: "is not declared"},
						{name: "enabled", uses: []string{"private-registry"}, enabled: true},
						{name: "disabled", uses: []string{"private-registry"}, wantErr: "disabled"},
					} {
						t.Run(tt.name, func(t *testing.T) {
							t.Parallel()
							agent := &azdext.ServiceConfig{Name: "agent", Uses: tt.uses}
							var checked []string
							err := validateRegistryConnectionDependency(
								t.Context(), agent, name, services, root,
								func(_ context.Context, key string) (bool, error) {
									checked = append(checked, key)
									return tt.enabled, nil
								},
							)
							require.True(t, proto.Equal(before, service), "sibling configuration must not be mutated")
							if tt.wantErr == "is not declared" {
								require.Empty(t, checked)
							} else {
								require.Equal(t, []string{"private-registry"}, checked)
							}
							if tt.wantErr == "" {
								require.NoError(t, err)
								return
							}
							localErr, ok := errors.AsType[*azdext.LocalError](err)
							require.True(t, ok)
							require.Equal(t, exterrors.CodeFoundryDependencyNotReady, localErr.Code)
							require.Equal(t, azdext.LocalErrorCategoryDependency, localErr.Category)
							require.Contains(t, localErr.Message, tt.wantErr)
							require.Contains(t, localErr.Message, `"private-registry"`)
						})
					}
					if overlay != "" {
						// The file's original name is external once the inline name overrides it.
						require.NoError(t, validateRegistryConnectionDependency(
							t.Context(), &azdext.ServiceConfig{Name: "agent"}, "file-registry", services, root, nil,
						))
						require.True(t, proto.Equal(before, service))
					}
				})
			}
		}
	}
}

func TestValidateRegistryConnectionDependencyPreservesReferenceErrors(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		values map[string]any
	}{
		{"missing file", map[string]any{"$ref": "missing.yaml"}},
		{"invalid reference type", map[string]any{"$ref": true}},
		{"empty reference", map[string]any{"$ref": ""}},
		{"invalid YAML", map[string]any{"$ref": "invalid.yaml"}},
		{"invalid JSON", map[string]any{"$ref": "invalid.json"}},
		{"nested reference", map[string]any{"credentials": map[string]any{"$ref": "missing.yaml"}}},
		{"array reference", map[string]any{"metadata": []any{map[string]any{"$ref": "missing.yaml"}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "invalid.yaml"), []byte("name: ["), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(root, "invalid.json"), []byte(`{"name":`), 0o600))
			props, err := MarshalStruct(&tt.values)
			require.NoError(t, err)
			service := &azdext.ServiceConfig{Host: foundryConnectionHost, AdditionalProperties: props}
			before := proto.Clone(service)
			_, expected := foundry.ResolveFileRefs(props.AsMap(), root)
			require.Error(t, expected)
			err = validateRegistryConnectionDependency(
				t.Context(), &azdext.ServiceConfig{Name: "agent"}, "file-registry",
				map[string]*azdext.ServiceConfig{"private-registry": service}, root, nil,
			)
			require.IsType(t, &azdext.LocalError{}, err, "classified errors must not be wrapped")
			require.Equal(t, expected, err)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, foundry.CodeInvalidFileRef, localErr.Code)
			require.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)
			require.True(t, proto.Equal(before, service))
		})
	}
}

func TestValidateRegistryConnectionDependencyEmptyProjectRoot(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		values map[string]any
		ref    bool
	}{
		{"inline name", map[string]any{"name": "file-registry"}, false},
		{"root reference", map[string]any{"$ref": "registry.yaml"}, true},
		{"nested reference", map[string]any{"credentials": map[string]any{"$ref": "registry.yaml"}}, true},
		{"array reference", map[string]any{"metadata": []any{map[string]any{"$ref": "registry.yaml"}}}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			props, err := MarshalStruct(&tt.values)
			require.NoError(t, err)
			service := &azdext.ServiceConfig{Host: foundryConnectionHost, AdditionalProperties: props}
			for _, root := range []string{"", " \t "} {
				err := validateRegistryConnectionDependency(
					t.Context(), &azdext.ServiceConfig{Name: "agent", Uses: []string{"private-registry"}}, "file-registry",
					map[string]*azdext.ServiceConfig{"private-registry": service}, root, nil,
				)
				if !tt.ref {
					require.NoError(t, err)
					continue
				}
				require.ErrorContains(t, err, "project root is empty")
				require.ErrorContains(t, err, "$ref")
				require.ErrorContains(t, err, `"private-registry"`)
			}
		})
	}
}

func TestValidateFoundryDependencies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		uses       []string
		services   map[string]*azdext.ServiceConfig
		env        map[string]string
		wantErr    bool
		wantDetail []string
	}{
		{
			name: "all supported dependencies ready",
			uses: []string{"project", "connection", "toolbox", "other-agent"},
			services: map[string]*azdext.ServiceConfig{
				"project":     {Name: "project", Host: foundryProjectHost},
				"connection":  {Name: "connection", Host: foundryConnectionHost},
				"toolbox":     {Name: "toolbox", Host: foundryToolboxHost},
				"other-agent": {Name: "other-agent", Host: foundryAgentHost},
			},
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":                            "https://example.test/projects/test",
				envkey.ConnectionServiceProjectEndpoint("connection"): "https://example.test/projects/test",
				envkey.ToolboxMCPEndpoint("toolbox"):                  "https://example.test/toolbox/mcp",
				envkey.ToolboxProjectEndpoint("toolbox"):              "https://example.test/projects/test",
				"AGENT_OTHER_AGENT_NAME":                              "other-agent",
				"AGENT_OTHER_AGENT_VERSION":                           "1",
				envkey.AgentProjectEndpoint("other-agent"):            "https://example.test/projects/test",
			},
		},
		{
			name: "missing supported dependencies are aggregated",
			uses: []string{"toolbox", "project"},
			services: map[string]*azdext.ServiceConfig{
				"project": {Name: "project", Host: foundryProjectHost},
				"toolbox": {Name: "toolbox", Host: foundryToolboxHost},
			},
			wantErr: true,
			wantDetail: []string{
				"project (azure.ai.project): FOUNDRY_PROJECT_ENDPOINT is not set",
				"toolbox (azure.ai.toolbox): TOOLBOX_TOOLBOX_MCP_ENDPOINT is not set",
				"azd provision",
				"azd deploy --all",
			},
		},
		{
			name: "transitive Foundry dependency is left to the producer",
			uses: []string{"toolbox"},
			services: map[string]*azdext.ServiceConfig{
				"project": {Name: "project", Host: foundryProjectHost},
				"toolbox": {Name: "toolbox", Host: foundryToolboxHost, Uses: []string{"project"}},
			},
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":               "https://example.test/projects/test",
				envkey.ToolboxMCPEndpoint("toolbox"):     "https://example.test/toolbox/mcp",
				envkey.ToolboxProjectEndpoint("toolbox"): "https://example.test/projects/test",
			},
		},
		{
			name: "single deploy dependency has targeted remediation",
			uses: []string{"toolbox"},
			services: map[string]*azdext.ServiceConfig{
				"toolbox": {Name: "toolbox", Host: foundryToolboxHost},
			},
			wantErr: true,
			wantDetail: []string{
				`azd deploy "toolbox"`,
				`azd deploy "agent"`,
			},
		},
		{
			name: "connection uses service name",
			uses: []string{"connection-service"},
			services: map[string]*azdext.ServiceConfig{
				"connection-service": {Name: "connection-service", Host: foundryConnectionHost},
			},
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":                                    "https://example.test/projects/test",
				envkey.ConnectionServiceProjectEndpoint("connection-service"): "https://example.test/projects/test",
			},
		},
		{
			name: "skill with version marker is ready",
			uses: []string{"summarize"},
			services: map[string]*azdext.ServiceConfig{
				"summarize": {Name: "summarize", Host: foundrySkillHost},
			},
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":               "https://example.test/projects/current",
				envkey.SkillVersion("summarize"):         "1",
				envkey.SkillProjectEndpoint("summarize"): "https://example.test/projects/current/",
			},
		},
		{
			name: "skill project endpoint comparison ignores case",
			uses: []string{"summarize"},
			services: map[string]*azdext.ServiceConfig{
				"summarize": {Name: "summarize", Host: foundrySkillHost},
			},
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":               "https://account.test/projects/current",
				envkey.SkillVersion("summarize"):         "1",
				envkey.SkillProjectEndpoint("summarize"): "HTTPS://ACCOUNT.TEST/projects/current/",
			},
		},
		{
			name: "skill endpoint aliases resolve to the same project",
			uses: []string{"summarize"},
			services: map[string]*azdext.ServiceConfig{
				"summarize": {Name: "summarize", Host: foundrySkillHost},
			},
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":               "https://account.services.ai.azure.com/api/projects/current",
				envkey.SkillVersion("summarize"):         "1",
				envkey.SkillProjectEndpoint("summarize"): "https://account.services.ai.azure.com/projects/current",
			},
		},
		{
			name: "legacy skill without readiness markers is accepted",
			uses: []string{"summarize"},
			services: map[string]*azdext.ServiceConfig{
				"summarize": {Name: "summarize", Host: foundrySkillHost},
			},
		},
		{
			name: "partial skill marker fails",
			uses: []string{"summarize"},
			services: map[string]*azdext.ServiceConfig{
				"summarize": {Name: "summarize", Host: foundrySkillHost},
			},
			env: map[string]string{
				envkey.SkillProjectEndpoint("summarize"): "https://example.test/projects/current",
			},
			wantErr:    true,
			wantDetail: []string{"summarize (azure.ai.skill): SKILL_SUMMARIZE_VERSION is not set", `azd deploy "summarize"`},
		},
		{
			name: "legacy connection names without scope are rejected",
			uses: []string{"connection"},
			services: map[string]*azdext.ServiceConfig{
				"connection": {Name: "connection", Host: foundryConnectionHost},
			},
			env: map[string]string{
				"AZURE_AI_PROJECT_CONNECTION_NAMES": "connection",
			},
			wantErr:    true,
			wantDetail: []string{envkey.ConnectionServiceProjectEndpoint("connection") + " is not set"},
		},
		{
			name: "connection readiness from another project fails",
			uses: []string{"connection"},
			services: map[string]*azdext.ServiceConfig{
				"connection": {Name: "connection", Host: foundryConnectionHost},
			},
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":                            "https://example.test/projects/current",
				envkey.ConnectionServiceProjectEndpoint("connection"): "https://example.test/projects/old",
			},
			wantErr: true,
			wantDetail: []string{
				envkey.ConnectionServiceProjectEndpoint("connection") + " does not match FOUNDRY_PROJECT_ENDPOINT",
			},
		},
		{
			name: "skill marker from another project fails",
			uses: []string{"summarize"},
			services: map[string]*azdext.ServiceConfig{
				"summarize": {Name: "summarize", Host: foundrySkillHost},
			},
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":               "https://example.test/projects/current",
				envkey.SkillVersion("summarize"):         "1",
				envkey.SkillProjectEndpoint("summarize"): "https://example.test/projects/old",
			},
			wantErr:    true,
			wantDetail: []string{"SKILL_SUMMARIZE_PROJECT_ENDPOINT does not match FOUNDRY_PROJECT_ENDPOINT"},
		},
		{
			name: "unknown Foundry host without readiness contract is ignored",
			uses: []string{"future"},
			services: map[string]*azdext.ServiceConfig{
				"future": {Name: "future", Host: "azure.ai.future"},
			},
		},
		{
			name: "non-Foundry dependency is ignored",
			uses: []string{"web"},
			services: map[string]*azdext.ServiceConfig{
				"web": {Name: "web", Host: "containerapp"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost, Uses: tt.uses}
			services := tt.services
			services[agent.Name] = agent

			err := validateFoundryDependencies(t.Context(), agent, nil, services, tt.env, nil)
			if !tt.wantErr {
				require.NoError(t, err)
				return
			}

			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, exterrors.CodeFoundryDependencyNotReady, localErr.Code)
			require.Equal(t, azdext.LocalErrorCategoryDependency, localErr.Category)
			for _, detail := range tt.wantDetail {
				require.Contains(t, localErr.Message+localErr.Suggestion, detail)
			}
		})
	}
}

func TestFoundryDependencyDeploymentSuggestions(t *testing.T) {
	t.Parallel()
	for _, host := range []string{
		foundryConnectionHost, foundryToolboxHost, foundryAgentHost, foundrySkillHost, foundryRoutineHost,
	} {
		t.Run(host, func(t *testing.T) {
			t.Parallel()
			agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost, Uses: []string{"dependency"}}
			services := map[string]*azdext.ServiceConfig{
				"dependency": {Name: "dependency", Host: host},
			}
			// Routines publish no readiness marker today. Exercise the same
			// classification and error builder without inventing a marker contract.
			failure := newFoundryDependencyFailure("dependency", host, "dependency is not ready")
			require.True(t, failure.requiresDeploy)
			require.False(t, failure.requiresProvision)
			err := foundryDependenciesError(agent, services, []foundryDependencyFailure{failure})
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, exterrors.CodeFoundryDependencyNotReady, localErr.Code)
			require.Contains(t, localErr.Suggestion, `azd deploy "dependency"`)
			require.Contains(t, localErr.Suggestion, `azd deploy "agent"`)
			require.NotContains(t, localErr.Suggestion, "azd provision")
		})
	}
}

func TestValidateFoundryDependenciesRoutineDoesNotRequireReadinessMarker(t *testing.T) {
	t.Parallel()
	agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost, Uses: []string{"routine"}}
	services := map[string]*azdext.ServiceConfig{
		"routine": {Name: "routine", Host: foundryRoutineHost},
	}
	require.NoError(t, validateFoundryDependencies(t.Context(), agent, nil, services, nil, nil))
}

func TestValidateFoundryDependenciesLegacyToolbox(t *testing.T) {
	t.Parallel()

	agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost}
	config := &ServiceTargetAgentConfig{Toolboxes: []Toolbox{{Name: "legacy-tools"}}}
	err := validateFoundryDependencies(
		t.Context(),
		agent,
		config,
		map[string]*azdext.ServiceConfig{"agent": agent},
		nil,
		nil,
	)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Contains(t, localErr.Message, "no azure.ai.toolbox service")
	require.Contains(t, localErr.Suggestion, "declare azure.ai.toolbox services")
}

func TestValidateFoundryDependenciesRejectsLegacyToolboxFromAnotherProject(t *testing.T) {
	t.Parallel()
	agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost}
	config := &ServiceTargetAgentConfig{Toolboxes: []Toolbox{{Name: "legacy-tools"}}}
	env := map[string]string{
		"FOUNDRY_PROJECT_ENDPOINT":                "https://account.services.ai.azure.com/api/projects/current",
		envkey.ToolboxMCPEndpoint("legacy-tools"): "https://account.services.ai.azure.com/api/projects/old/toolboxes/legacy-tools/mcp",
	}
	err := validateFoundryDependencies(
		t.Context(), agent, config, map[string]*azdext.ServiceConfig{"agent": agent}, env, nil,
	)
	require.ErrorContains(t, err, "no azure.ai.toolbox service")
}

func TestValidateFoundryDependenciesSplitToolboxReference(t *testing.T) {
	t.Parallel()

	agent := &azdext.ServiceConfig{
		Name: "agent",
		Host: foundryAgentHost,
		Uses: []string{"tools"},
	}
	config := &ServiceTargetAgentConfig{Toolboxes: []Toolbox{{Name: "tools"}}}
	services := map[string]*azdext.ServiceConfig{
		"agent": agent,
		"tools": {Name: "tools", Host: foundryToolboxHost},
	}
	err := validateFoundryDependencies(t.Context(), agent, config, services, nil, nil)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.NotContains(t, localErr.Message, "legacy bundled toolbox")
	require.Contains(t, localErr.Suggestion, `azd deploy "tools"`)
}

func TestAddAgentToolboxDependency(t *testing.T) {
	t.Parallel()

	config := &ServiceTargetAgentConfig{}
	reference := &agent_yaml.ToolboxReference{Name: " support-tools ", Version: "2"}
	addAgentToolboxDependency(config, reference)
	addAgentToolboxDependency(config, reference)

	require.Equal(t, []Toolbox{{Name: "support-tools"}}, config.Toolboxes)
}

func TestValidateFoundryDependenciesUnwiredSplitToolbox(t *testing.T) {
	t.Parallel()
	agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost}
	config := &ServiceTargetAgentConfig{Toolboxes: []Toolbox{{Name: "tools"}}}
	services := map[string]*azdext.ServiceConfig{
		"agent": agent,
		"tools": {Name: "tools", Host: foundryToolboxHost},
	}
	err := validateFoundryDependencies(t.Context(), agent, config, services, nil, nil)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Contains(t, localErr.Message, "toolbox service is not declared in agent uses")
	require.Contains(t, localErr.Suggestion, `add "tools" to the "agent" service uses list`)
}

func TestValidateFoundryDependenciesRejectsToolboxServiceWithWrongHost(t *testing.T) {
	t.Parallel()
	agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost, Uses: []string{"tools"}}
	config := &ServiceTargetAgentConfig{Toolboxes: []Toolbox{{Name: "tools"}}}
	services := map[string]*azdext.ServiceConfig{
		"agent": agent,
		"tools": {Name: "tools", Host: foundryAgentHost},
	}
	env := map[string]string{
		"FOUNDRY_PROJECT_ENDPOINT":             "https://example.test/projects/current",
		envkey.ToolboxMCPEndpoint("tools"):     "https://example.test/projects/current/toolboxes/tools/mcp",
		envkey.ToolboxProjectEndpoint("tools"): "https://example.test/projects/current",
		envkey.AgentProjectEndpoint("tools"):   "https://example.test/projects/current",
		"AGENT_TOOLS_NAME":                     "tools",
		"AGENT_TOOLS_VERSION":                  "1",
	}

	err := validateFoundryDependencies(t.Context(), agent, config, services, env, nil)
	require.ErrorContains(t, err, "resolves to service host")
	require.ErrorContains(t, err, "instead of")
}

func TestValidateFoundryDependenciesSkipsDisabledDependency(t *testing.T) {
	t.Parallel()
	agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost, Uses: []string{"tools"}}
	services := map[string]*azdext.ServiceConfig{
		"agent": agent,
		"tools": {Name: "tools", Host: foundryToolboxHost},
	}
	err := validateFoundryDependencies(
		t.Context(), agent, nil, services, nil,
		func(context.Context, string) (bool, error) { return false, nil },
	)
	require.NoError(t, err)
}

func TestValidateFoundryDependenciesReportsDisabledRuntimeToolbox(t *testing.T) {
	t.Parallel()
	agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost, Uses: []string{"tools"}}
	config := &ServiceTargetAgentConfig{Toolboxes: []Toolbox{{Name: "tools"}}}
	services := map[string]*azdext.ServiceConfig{
		"agent": agent,
		"tools": {Name: "tools", Host: foundryToolboxHost},
	}
	err := validateFoundryDependencies(
		t.Context(), agent, config, services, nil,
		func(context.Context, string) (bool, error) { return false, nil },
	)
	require.ErrorContains(t, err, "toolbox dependency is disabled")
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Contains(t, localErr.Suggestion, "enable the toolbox dependency or remove it from the agent definition")
}

func TestValidateFoundryDependenciesCombinesMigrationAndProvisionRemediation(t *testing.T) {
	t.Parallel()
	agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost, Uses: []string{"project"}}
	config := &ServiceTargetAgentConfig{Toolboxes: []Toolbox{{Name: "legacy-tools"}}}
	services := map[string]*azdext.ServiceConfig{
		"agent": agent, "project": {Name: "project", Host: foundryProjectHost},
	}
	err := validateFoundryDependencies(t.Context(), agent, config, services, nil, nil)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Contains(t, localErr.Suggestion, "declare azure.ai.toolbox services")
	require.Contains(t, localErr.Suggestion, "azd provision")
	require.Contains(t, localErr.Suggestion, "azd deploy --all")
}

func TestValidateFoundryDependenciesIgnoresResourceUses(t *testing.T) {
	t.Parallel()
	agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost, Uses: []string{"storage"}}
	called := false
	err := validateFoundryDependencies(
		t.Context(), agent, nil, map[string]*azdext.ServiceConfig{"agent": agent}, nil,
		func(context.Context, string) (bool, error) { called = true; return true, nil },
	)
	require.NoError(t, err)
	require.False(t, called)
}

func TestValidateFoundryDependenciesRejectsCrossProjectMarkers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		host   string
		env    map[string]string
		detail string
	}{
		{
			name: "toolbox", host: foundryToolboxHost,
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":           "https://current",
				envkey.ToolboxMCPEndpoint("dep"):     "https://mcp",
				envkey.ToolboxProjectEndpoint("dep"): "https://old",
			},
			detail: "TOOLBOX_DEP_PROJECT_ENDPOINT",
		},
		{
			name: "agent", host: foundryAgentHost,
			env: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT": "https://current",
				"AGENT_DEP_NAME":           "dep", "AGENT_DEP_VERSION": "1",
				envkey.AgentProjectEndpoint("dep"): "https://old",
			},
			detail: "AGENT_DEP_PROJECT_ENDPOINT",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			agent := &azdext.ServiceConfig{Name: "root", Host: foundryAgentHost, Uses: []string{"dep"}}
			services := map[string]*azdext.ServiceConfig{
				"root": agent, "dep": {Name: "dep", Host: tt.host},
			}
			err := validateFoundryDependencies(t.Context(), agent, nil, services, tt.env, nil)
			require.ErrorContains(t, err, tt.detail)
		})
	}
}

func TestValidateFoundryDependenciesProvisionAndConnectionDeployRemediation(t *testing.T) {
	t.Parallel()

	agent := &azdext.ServiceConfig{
		Name: "agent",
		Host: foundryAgentHost,
		Uses: []string{"project", "connection"},
	}
	services := map[string]*azdext.ServiceConfig{
		"agent":      agent,
		"project":    {Name: "project", Host: foundryProjectHost},
		"connection": {Name: "connection", Host: foundryConnectionHost},
	}
	err := validateFoundryDependencies(t.Context(), agent, nil, services, nil, nil)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Contains(t, localErr.Suggestion, "azd provision")
	require.Contains(t, localErr.Suggestion, "azd deploy --all")
}

func TestValidateFoundryDependenciesConnectionUsesDeployMarker(t *testing.T) {
	t.Parallel()

	agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost, Uses: []string{"connection"}}
	services := map[string]*azdext.ServiceConfig{
		"agent":      agent,
		"connection": {Name: "connection", Host: foundryConnectionHost},
	}

	t.Run("matching project is ready", func(t *testing.T) {
		t.Parallel()
		env := map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT":                            "https://example.test/projects/current",
			envkey.ConnectionServiceProjectEndpoint("connection"): "https://example.test/projects/current/",
		}
		require.NoError(t, validateFoundryDependencies(t.Context(), agent, nil, services, env, nil))
	})

	t.Run("other project is rejected", func(t *testing.T) {
		t.Parallel()
		env := map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT":                            "https://example.test/projects/current",
			envkey.ConnectionServiceProjectEndpoint("connection"): "https://example.test/projects/old",
		}
		err := validateFoundryDependencies(t.Context(), agent, nil, services, env, nil)
		require.ErrorContains(t, err, envkey.ConnectionServiceProjectEndpoint("connection"))
	})

	t.Run("missing marker recommends targeted deploy", func(t *testing.T) {
		t.Parallel()
		err := validateFoundryDependencies(t.Context(), agent, nil, services, nil, nil)
		localErr, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok)
		require.Contains(t, localErr.Suggestion, `azd deploy "connection"`)
		require.NotContains(t, localErr.Suggestion, "azd provision")
	})

	t.Run("aggregate markers cannot establish readiness", func(t *testing.T) {
		t.Parallel()
		env := map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT":                      "https://example.test/projects/current",
			"AZURE_AI_PROJECT_CONNECTION_NAMES":             "connection",
			"AZURE_AI_PROJECT_CONNECTIONS_PROJECT_ENDPOINT": "https://example.test/projects/current",
		}
		err := validateFoundryDependencies(t.Context(), agent, nil, services, env, nil)
		require.ErrorContains(t, err, envkey.ConnectionServiceProjectEndpoint("connection")+" is not set")
	})

	t.Run("service marker without active project is rejected", func(t *testing.T) {
		t.Parallel()
		env := map[string]string{
			envkey.ConnectionServiceProjectEndpoint("connection"): "https://example.test/projects/current",
		}
		err := validateFoundryDependencies(t.Context(), agent, nil, services, env, nil)
		require.ErrorContains(t, err, "does not match FOUNDRY_PROJECT_ENDPOINT")
	})
}

func TestValidateFoundryDependenciesToolboxRequiresSplitService(t *testing.T) {
	t.Parallel()
	const endpoint = "https://example.test/projects/current"
	agent := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost, Uses: []string{"tools"}}
	config := &ServiceTargetAgentConfig{Toolboxes: []Toolbox{{Name: "tools"}}}
	env := map[string]string{
		"FOUNDRY_PROJECT_ENDPOINT":             endpoint,
		envkey.ToolboxMCPEndpoint("tools"):     endpoint + "/toolboxes/tools/mcp",
		envkey.ToolboxProjectEndpoint("tools"): endpoint,
	}
	err := validateFoundryDependencies(t.Context(), agent, config, nil, env, nil)
	require.ErrorContains(t, err, "no azure.ai.toolbox service")

	// External reuse still requires a split service and its owning extension's
	// readiness output. Merely declaring endpoint cannot bypass deployment.
	props, err := MarshalStruct(new(map[string]any{"endpoint": "https://external.test/mcp"}))
	require.NoError(t, err)
	services := map[string]*azdext.ServiceConfig{
		"tools": {Name: "tools", Host: foundryToolboxHost, AdditionalProperties: props},
	}
	env[envkey.ToolboxMCPEndpoint("tools")] = "https://external.test/mcp"
	require.NoError(t, validateFoundryDependencies(t.Context(), agent, config, services, env, nil))
	delete(env, envkey.ToolboxMCPEndpoint("tools"))
	require.ErrorContains(t,
		validateFoundryDependencies(t.Context(), agent, config, services, env, nil), "TOOLBOX_TOOLS_MCP_ENDPOINT is not set")
	env[envkey.ToolboxMCPEndpoint("tools")] = "https://external.test/mcp"
	delete(env, "FOUNDRY_PROJECT_ENDPOINT")
	delete(env, envkey.ToolboxProjectEndpoint("tools"))
	require.ErrorContains(t,
		validateFoundryDependencies(t.Context(), agent, config, services, env, nil), "FOUNDRY_PROJECT_ENDPOINT is not set")
}

func TestValidateFoundryConnectionDependencyDoesNotAcceptAnotherServiceMarker(t *testing.T) {
	t.Parallel()
	const endpoint = "https://example.test/projects/current"
	env := map[string]string{
		"FOUNDRY_PROJECT_ENDPOINT":                               endpoint,
		envkey.ConnectionServiceProjectEndpoint("my connection"): endpoint,
	}
	require.Empty(t, validateFoundryConnectionDependency(&azdext.ServiceConfig{Name: "my connection"}, env))
	for _, other := range []string{"my--connection", "my_connection", "My Connection"} {
		require.NotEmpty(t, validateFoundryConnectionDependency(&azdext.ServiceConfig{Name: other}, env))
	}

	// Ambiguous markers from the old normalization cannot prove readiness.
	oldMarkers := map[string]string{
		"FOUNDRY_PROJECT_ENDPOINT":                  endpoint,
		"CONNECTION_MY_CONNECTION_PROJECT_ENDPOINT": endpoint,
	}
	require.NotEmpty(t, validateFoundryConnectionDependency(&azdext.ServiceConfig{Name: "my connection"}, oldMarkers))
}
