// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type parameterResolverFunc func(context.Context, string, map[string]string, *[]string) (any, bool, error)

func (f parameterResolverFunc) ResolveParameter(
	ctx context.Context, name string, values map[string]string, mapping *[]string,
) (any, bool, error) {
	return f(ctx, name, values, mapping)
}

func TestParametersRefreshAfterEnsureEnv(t *testing.T) {
	for _, initial := range []string{"old-token", ""} {
		t.Run("initial token "+initial, func(t *testing.T) {
			t.Setenv("TOKEN", "process-token")
			t.Setenv("PROCESS_ONLY", "old-process")
			env := &environmentServer{values: map[string]string{
				"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_LOCATION": "eastus",
				"TOKEN": initial, "UPSTREAM": "old-output", "REMOVED": "old-value", "VIRTUAL": "saved-value",
				principalIDPlaceholder: "authored-id", principalTypePlaceholder: "User",
			}}
			prompts, account := &promptServer{}, &accountServer{}
			p := New(newHostClientWithIdentity(t, env, prompts, account, &projectServer{}), nil)
			require.NoError(t, p.Initialize(t.Context(), ".", &azdext.ProvisioningOptions{
				Path: t.TempDir(), Name: "app", VirtualEnv: map[string]string{
					"VIRTUAL": "virtual-value", principalIDPlaceholder: "virtual-id", principalTypePlaceholder: "User",
				},
			}))
			p.compile = func(context.Context, string) ([]byte, error) {
				return []byte(`{
					"$schema": "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
					"parameters": {
						"token": {"type": "secureString"},
						"upstream": {"type": "string"},
						"removed": {"type": "string"},
						"process": {"type": "string"},
						"virtual": {"type": "string"},
						"principalId": {"type": "string"},
						"principalType": {"type": "string"}
					}
				}`), nil
			}
			input := `{"parameters": {
				"token": {"value": "${TOKEN}"},
				"upstream": {"value": "${UPSTREAM}"},
				"removed": {"value": "${REMOVED:=fallback}"},
				"process": {"value": "${PROCESS_ONLY}"},
				"virtual": {"value": "${VIRTUAL}"},
				"principalId": {"value": "${AZURE_PRINCIPAL_ID}"},
				"principalType": {"value": "${AZURE_PRINCIPAL_TYPE}"}
			}}`
			require.NoError(t, os.WriteFile(filepath.Join(p.path, "main.parameters.json"), []byte(input), 0o600))
			require.NoError(t, p.EnsureEnv(t.Context()))
			backend, name := p.azure, p.name

			// Hooks and dependent layers update the host after serial provider initialization.
			require.NoError(t, p.setEnv(t.Context(), "TOKEN", "hook-token"))
			require.NoError(t, p.setEnv(t.Context(), "UPSTREAM", "layer-output"))
			require.NoError(t, p.setEnv(t.Context(), "VIRTUAL", "updated-saved-value"))
			delete(env.values, "REMOVED")
			t.Setenv("PROCESS_ONLY", "new-process")
			parameters, err := p.Parameters(t.Context())
			require.NoError(t, err)
			byName := map[string]*azdext.ProvisioningParameter{}
			for _, parameter := range parameters {
				byName[parameter.Name] = parameter
			}
			require.Equal(t, &azdext.ProvisioningParameter{
				Name: "token", Value: "hook-token", Secret: true,
				EnvVarMapping: []string{"TOKEN"}, UsingEnvVarMapping: true,
			}, byName["token"])
			require.Equal(t, "layer-output", byName["upstream"].Value)
			require.Equal(t, "fallback", byName["removed"].Value)
			require.Equal(t, "new-process", byName["process"].Value)
			require.Equal(t, "virtual-value", byName["virtual"].Value)
			require.Equal(t, "resource-tenant-object-id", byName["principalId"].Value)
			require.Equal(t, "ServicePrincipal", byName["principalType"].Value)
			require.Empty(t, byName["principalId"].EnvVarMapping)
			require.Empty(t, byName["principalType"].EnvVarMapping)
			require.Empty(t, prompts.promptRequests)
			require.Empty(t, prompts.locations)
			require.Zero(t, prompts.subscriptionRequests)
			require.Len(t, account.principals, 1)
			require.Len(t, account.lookups, 1)
			require.Len(t, env.cloudReads, 1)
			require.Equal(t, backend, p.azure)
			require.Equal(t, name, p.name)
			require.Len(t, env.reads, 2)

			require.NoError(t, p.setEnv(t.Context(), "TOKEN", "later-token"))
			repeated, err := p.Parameters(t.Context())
			require.NoError(t, err)
			require.Equal(t, parameters, repeated)
			require.Len(t, env.reads, 2, "prepared parameters must not reload inputs or repeat prompts")
		})
	}
}

func TestPreviewAndDeployPreserveResourceExistsAfterEnsureEnv(t *testing.T) {
	t.Setenv("AZURE_SUBSCRIPTION_ID", "")
	t.Setenv("AZURE_LOCATION", "")
	p := readyProvider(t, &fakeBackend{})
	env := &environmentServer{values: map[string]string{
		"SERVICE_API_RESOURCE_EXISTS": "false", "PASSWORD": "saved-password", "QUOTED": "saved-setting",
	}}
	prompts := &promptServer{}
	p.client = newHostClient(t, env, prompts)
	template := p.template
	require.NoError(t, p.Initialize(t.Context(), ".", &azdext.ProvisioningOptions{Path: "testdata", Name: "app"}))
	p.compile = func(context.Context, string) ([]byte, error) { return json.Marshal(template) }
	backend := &fakeBackend{
		deployment: deployedFixture(), groupErr: &azcore.ResponseError{StatusCode: 404},
		changes: []*armresources.WhatIfChange{{ResourceID: new(groupID), ChangeType: new(armresources.ChangeTypeCreate)}},
	}
	p.azure = backend
	require.NoError(t, p.EnsureEnv(t.Context()))
	require.Len(t, env.writes, 2, "serial scope setup must persist subscription and location")
	require.NoError(t, p.setEnv(t.Context(), "SERVICE_API_RESOURCE_EXISTS", "true"))

	_, err := p.Preview(t.Context(), nil)
	require.NoError(t, err)
	request := backend.previewInput
	parameters, ok := request.Properties.Parameters.(map[string]any)
	require.True(t, ok)
	require.Equal(t, map[string]any{"value": true}, parameters["serviceExists"],
		"the generated Container App template must preserve the deployed image instead of selecting hello-world")
	require.Equal(t, backend, p.azure)
	require.Equal(t, "eastus", *request.Location)
	require.Equal(t, "sub", p.scope.subscriptionID)
	require.Equal(t, 1, prompts.subscriptionRequests)
	require.Len(t, prompts.locations, 1)

	require.NoError(t, p.setEnv(t.Context(), "SERVICE_API_RESOURCE_EXISTS", "false"))
	result, err := p.Deploy(t.Context(), nil)
	require.NoError(t, err)
	require.True(t, backend.deployed)
	require.Equal(t, request.Properties.Parameters, backend.deployInput.Properties.Parameters,
		"preview and deployment must use the same prepared inputs")
	require.Equal(t, "true", result.Deployment.Parameters["serviceExists"].Value)
	require.Len(t, env.reads, 2)
}

func TestParametersEnvironmentRefreshFailureIsRetryable(t *testing.T) {
	p := readyProvider(t, &fakeBackend{})
	env := &environmentServer{values: map[string]string{
		"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_LOCATION": "eastus",
		"PASSWORD": "old-password", "QUOTED": "saved-setting",
	}}
	p.client = newHostClient(t, env, &promptServer{})
	p.scope = nil
	require.NoError(t, p.EnsureEnv(t.Context()))
	env.valuesErr = errors.New("refresh failed")

	parameters, err := p.Parameters(t.Context())
	require.ErrorContains(t, err, "reading environment values")
	require.ErrorContains(t, err, "refresh failed")
	require.Nil(t, parameters)
	require.Nil(t, p.params)
	require.Nil(t, p.metadata)

	env.valuesErr = nil
	require.NoError(t, p.setEnv(t.Context(), "PASSWORD", "fresh-password"))
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			parameters, err := p.Parameters(t.Context())
			if !assert.NoError(t, err) {
				return
			}
			for _, parameter := range parameters {
				if parameter.Name == "password" {
					assert.Equal(t, "fresh-password", parameter.Value)
				}
			}
		})
	}
	wg.Wait()
	require.NotNil(t, p.params)
	require.Len(t, env.reads, 3, "concurrent callers must share the first successful parameter preparation")
}

func TestParameterResolverValueIsCoercedAndMetadataPreserved(t *testing.T) {
	p := readyProvider(t, &fakeBackend{})
	calls := 0
	p.resolver = parameterResolverFunc(func(
		ctx context.Context, name string, values map[string]string, mapping *[]string,
	) (any, bool, error) {
		calls++
		require.Equal(t, t.Context(), ctx)
		require.Equal(t, "count", name)
		require.Equal(t, "sub", values["AZURE_SUBSCRIPTION_ID"])
		*mapping = append(*mapping, "Z", "A", "Z")
		return "42", true, nil
	})

	parameter, metadata, err := p.prepareParameter(t.Context(), "count",
		map[string]any{"value": "ignored"}, parameterDefinition{Type: "int"},
		map[string]string{"AZURE_SUBSCRIPTION_ID": "sub"})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"value": float64(42)}, parameter)
	require.Equal(t, &azdext.ProvisioningParameter{
		Name: "count", Value: "42", EnvVarMapping: []string{"A", "Z"}, UsingEnvVarMapping: true, LocalPrompt: true,
	}, metadata)
	require.Equal(t, 1, calls)
}

func TestParameterResolverFallbackUsesNormalSubstitution(t *testing.T) {
	p := readyProvider(t, &fakeBackend{})
	p.resolver = parameterResolverFunc(func(context.Context, string, map[string]string, *[]string) (any, bool, error) {
		return nil, false, nil
	})

	parameter, metadata, err := p.prepareParameter(t.Context(), "location",
		map[string]any{"value": "${AZURE_LOCATION}"}, parameterDefinition{Type: "string"},
		map[string]string{"AZURE_LOCATION": "eastus"})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"value": "eastus"}, parameter)
	require.Equal(t, &azdext.ProvisioningParameter{
		Name: "location", Value: "eastus", EnvVarMapping: []string{"AZURE_LOCATION"}, UsingEnvVarMapping: true,
	}, metadata)
}

func TestParameterResolverFallbackCanUseTemplateDefault(t *testing.T) {
	p := readyProvider(t, &fakeBackend{})
	p.resolver = parameterResolverFunc(func(context.Context, string, map[string]string, *[]string) (any, bool, error) {
		return nil, false, nil
	})
	definition := parameterDefinition{Type: "string", DefaultValue: []byte(`"eastus"`)}

	parameter, metadata, err := p.prepareParameter(t.Context(), "location", nil, definition, nil)
	require.NoError(t, err)
	require.Nil(t, parameter)
	require.Nil(t, metadata)
}

func TestParameterResolverFailureDoesNotFallBack(t *testing.T) {
	p := readyProvider(t, &fakeBackend{})
	expected := errors.New("location resolution failed")
	p.resolver = parameterResolverFunc(func(context.Context, string, map[string]string, *[]string) (any, bool, error) {
		return nil, false, expected
	})

	parameter, metadata, err := p.prepareParameter(t.Context(), "location",
		map[string]any{"value": "eastus"}, parameterDefinition{Type: "string"}, nil)
	require.ErrorIs(t, err, expected)
	require.ErrorContains(t, err, `resolving parameter "location"`)
	require.Nil(t, parameter)
	require.Nil(t, metadata)
}

func TestParameterResolverFailureLeavesPreparationRetryable(t *testing.T) {
	p := readyProvider(t, &fakeBackend{})
	expected := errors.New("temporary resolver failure")
	fail := true
	p.resolver = parameterResolverFunc(func(context.Context, string, map[string]string, *[]string) (any, bool, error) {
		if fail {
			return nil, false, expected
		}
		return nil, false, nil
	})

	parameters, err := p.Parameters(t.Context())
	require.ErrorIs(t, err, expected)
	require.Nil(t, parameters)
	require.Nil(t, p.params)
	require.Nil(t, p.metadata)

	fail = false
	parameters, err = p.Parameters(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, parameters)
	require.NotEmpty(t, p.params)
}

func TestParameterResolverDoesNotHandleKeyVaultReferences(t *testing.T) {
	p := readyProvider(t, &fakeBackend{})
	p.resolver = parameterResolverFunc(func(context.Context, string, map[string]string, *[]string) (any, bool, error) {
		t.Fatal("Key Vault references must not invoke the value resolver")
		return nil, false, nil
	})
	reference := map[string]any{"keyVault": map[string]any{"id": "vault-id"}, "secretName": "password"}

	parameter, metadata, err := p.prepareParameter(t.Context(), "password",
		map[string]any{"reference": reference}, parameterDefinition{Type: "secureString"}, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"reference": reference}, parameter)
	require.Equal(t, "null", metadata.Value)
	require.True(t, metadata.Secret)
	require.False(t, metadata.LocalPrompt)
}

func TestParameterResolverFallbackPromptsForMissingValue(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "supplied value"},
		{name: "prompt canceled", err: status.Error(codes.Canceled, "canceled")},
	} {
		t.Run(test.name, func(t *testing.T) {
			prompts := &promptServer{promptValue: "password", promptErr: test.err}
			p := readyProvider(t, &fakeBackend{})
			p.client = newHostClient(t, &environmentServer{}, prompts)
			p.resolver = parameterResolverFunc(func(
				context.Context, string, map[string]string, *[]string,
			) (any, bool, error) {
				return nil, false, nil
			})
			definition := parameterDefinition{Type: "secureString"}
			definition.Metadata.Description = "Credential for the service"

			parameter, metadata, err := p.prepareParameter(t.Context(), "password", nil, definition, nil)
			require.Len(t, prompts.promptRequests, 1)
			options := prompts.promptRequests[0].Options
			require.Equal(t, "Enter a value for password", options.Message)
			require.Equal(t, definition.Metadata.Description, options.HelpMessage)
			require.True(t, options.Required)
			require.True(t, options.Secret)
			if test.err != nil {
				require.ErrorContains(t, err, `prompting for parameter "password"`)
				require.Equal(t, codes.Canceled, status.Code(err))
				require.Nil(t, parameter)
				require.Nil(t, metadata)
				return
			}
			require.NoError(t, err)
			require.Equal(t, map[string]any{"value": "password"}, parameter)
			require.Equal(t, "password", metadata.Value)
			require.True(t, metadata.Secret)
			require.True(t, metadata.LocalPrompt)
			require.False(t, metadata.UsingEnvVarMapping)
		})
	}
}
