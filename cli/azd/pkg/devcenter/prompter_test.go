// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package devcenter

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestPromptParametersSkip(t *testing.T) {
	t.Run("SkipParameterWithTrueValue", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		// Set up environment variable for skipping
		t.Setenv("AZURE_TESTPARAM_SKIP", "true")

		prompter := NewPrompter(mockContext.Console, nil, nil)

		env := environment.NewWithValues("test-env", map[string]string{})

		envDef := &devcentersdk.EnvironmentDefinition{
			Parameters: []devcentersdk.Parameter{
				{
					Id:   "testParam",
					Name: "Test Parameter",
					Type: devcentersdk.ParameterTypeString,
				},
			},
		}

		result, err := prompter.PromptParameters(*mockContext.Context, env, envDef)

		require.NoError(t, err)
		require.Contains(t, result, "testParam")
		require.Nil(t, result["testParam"])
	})

	t.Run("SkipParameterWith1Value", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		// Set up environment variable for skipping
		t.Setenv("AZURE_TESTPARAM_SKIP", "1")

		prompter := NewPrompter(mockContext.Console, nil, nil)

		env := environment.NewWithValues("test-env", map[string]string{})

		envDef := &devcentersdk.EnvironmentDefinition{
			Parameters: []devcentersdk.Parameter{
				{
					Id:   "testParam",
					Name: "Test Parameter",
					Type: devcentersdk.ParameterTypeString,
				},
			},
		}

		result, err := prompter.PromptParameters(*mockContext.Context, env, envDef)

		require.NoError(t, err)
		require.Contains(t, result, "testParam")
		require.Nil(t, result["testParam"])
	})

	t.Run("NoSkipParameterWithFalseValue", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		// Set up environment variable for NOT skipping
		t.Setenv("AZURE_TESTPARAM_SKIP", "false")

		// Expect a prompt to occur
		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
			return options.Message == "Enter a value for Test Parameter"
		}).Respond("test-value")

		prompter := NewPrompter(mockContext.Console, nil, nil)

		env := environment.NewWithValues("test-env", map[string]string{})

		envDef := &devcentersdk.EnvironmentDefinition{
			Parameters: []devcentersdk.Parameter{
				{
					Id:   "testParam",
					Name: "Test Parameter",
					Type: devcentersdk.ParameterTypeString,
				},
			},
		}

		result, err := prompter.PromptParameters(*mockContext.Context, env, envDef)

		require.NoError(t, err)
		require.Contains(t, result, "testParam")
		require.Equal(t, "test-value", result["testParam"])
	})
}