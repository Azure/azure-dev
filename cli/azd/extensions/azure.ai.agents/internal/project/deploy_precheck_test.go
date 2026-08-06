// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestValidateEnvironmentVariableNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		serviceEnvironment map[string]string
		agentEnvironment   *[]agent_yaml.EnvironmentVariable
		wantInvalid        []string
	}{
		{
			name: "accepts supported names",
			serviceEnvironment: map[string]string{
				"API_URL": "",
				"_TOKEN2": "",
				"name":    "",
			},
			agentEnvironment: &[]agent_yaml.EnvironmentVariable{
				{Name: "LEGACY_VALUE"},
			},
		},
		{
			name: "rejects invalid service name",
			serviceEnvironment: map[string]string{
				"function-api-base-url": "",
			},
			wantInvalid: []string{"function-api-base-url"},
		},
		{
			name: "rejects invalid legacy names",
			agentEnvironment: &[]agent_yaml.EnvironmentVariable{
				{Name: "9TOKEN"},
				{Name: "API.URL"},
			},
			wantInvalid: []string{"9TOKEN", "API.URL"},
		},
		{
			name: "rejects empty name",
			agentEnvironment: &[]agent_yaml.EnvironmentVariable{
				{Name: ""},
			},
			wantInvalid: []string{""},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := validateEnvironmentVariableNames(
				test.serviceEnvironment,
				test.agentEnvironment,
			)
			if len(test.wantInvalid) == 0 {
				require.NoError(t, err)
				return
			}

			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(
				t,
				exterrors.CodeInvalidEnvironmentVariableName,
				localErr.Code,
			)
			for _, name := range test.wantInvalid {
				require.Contains(t, localErr.Message, name)
			}
		})
	}
}

func TestDeployBlocksInvalidEnvironmentVariableName(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"kind": "hosted",
		"name": "test-agent",
	})
	require.NoError(t, err)

	serviceConfig := &azdext.ServiceConfig{
		Name: "agent",
		Environment: map[string]string{
			"function-api-base-url": "https://example.test",
		},
		AdditionalProperties: props,
	}
	provider := &AgentServiceTargetProvider{
		deployContextReady: true,
	}

	result, err := provider.Deploy(
		t.Context(),
		serviceConfig,
		&azdext.ServiceContext{},
		nil,
		func(string) {},
	)

	require.Nil(t, result)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Equal(
		t,
		exterrors.CodeInvalidEnvironmentVariableName,
		localErr.Code,
	)
}
