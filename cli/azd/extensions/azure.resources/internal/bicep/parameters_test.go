// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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

func TestParameterResolverValueIsCoercedAndMetadataPreserved(t *testing.T) {
	p := readyProvider(&fakeBackend{})
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
		map[string]any{"value": "ignored"}, parameterDefinition{Type: "int"})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"value": float64(42)}, parameter)
	require.Equal(t, &azdext.ProvisioningParameter{
		Name: "count", Value: "42", EnvVarMapping: []string{"A", "Z"}, UsingEnvVarMapping: true, LocalPrompt: true,
	}, metadata)
	require.Equal(t, 1, calls)
}

func TestParameterResolverFallbackUsesNormalSubstitution(t *testing.T) {
	p := readyProvider(&fakeBackend{})
	p.resolver = parameterResolverFunc(func(context.Context, string, map[string]string, *[]string) (any, bool, error) {
		return nil, false, nil
	})

	parameter, metadata, err := p.prepareParameter(t.Context(), "location",
		map[string]any{"value": "${AZURE_LOCATION}"}, parameterDefinition{Type: "string"})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"value": "eastus"}, parameter)
	require.Equal(t, &azdext.ProvisioningParameter{
		Name: "location", Value: "eastus", EnvVarMapping: []string{"AZURE_LOCATION"}, UsingEnvVarMapping: true,
	}, metadata)
}

func TestParameterResolverFallbackCanUseTemplateDefault(t *testing.T) {
	p := readyProvider(&fakeBackend{})
	p.resolver = parameterResolverFunc(func(context.Context, string, map[string]string, *[]string) (any, bool, error) {
		return nil, false, nil
	})
	definition := parameterDefinition{Type: "string", DefaultValue: []byte(`"eastus"`)}

	parameter, metadata, err := p.prepareParameter(t.Context(), "location", nil, definition)
	require.NoError(t, err)
	require.Nil(t, parameter)
	require.Nil(t, metadata)
}

func TestParameterResolverFailureDoesNotFallBack(t *testing.T) {
	p := readyProvider(&fakeBackend{})
	expected := errors.New("location resolution failed")
	p.resolver = parameterResolverFunc(func(context.Context, string, map[string]string, *[]string) (any, bool, error) {
		return nil, false, expected
	})

	parameter, metadata, err := p.prepareParameter(t.Context(), "location",
		map[string]any{"value": "eastus"}, parameterDefinition{Type: "string"})
	require.ErrorIs(t, err, expected)
	require.ErrorContains(t, err, `resolving parameter "location"`)
	require.Nil(t, parameter)
	require.Nil(t, metadata)
}

func TestParameterResolverFailureLeavesPreparationRetryable(t *testing.T) {
	p := readyProvider(&fakeBackend{})
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
	p := readyProvider(&fakeBackend{})
	p.resolver = parameterResolverFunc(func(context.Context, string, map[string]string, *[]string) (any, bool, error) {
		t.Fatal("Key Vault references must not invoke the value resolver")
		return nil, false, nil
	})
	reference := map[string]any{"keyVault": map[string]any{"id": "vault-id"}, "secretName": "password"}

	parameter, metadata, err := p.prepareParameter(t.Context(), "password",
		map[string]any{"reference": reference}, parameterDefinition{Type: "secureString"})
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
			p := readyProvider(&fakeBackend{})
			p.client = newHostClient(t, &environmentServer{}, prompts)
			p.resolver = parameterResolverFunc(func(
				context.Context, string, map[string]string, *[]string,
			) (any, bool, error) {
				return nil, false, nil
			})
			definition := parameterDefinition{Type: "secureString"}
			definition.Metadata.Description = "Credential for the service"

			parameter, metadata, err := p.prepareParameter(t.Context(), "password", nil, definition)
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
