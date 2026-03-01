// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/stretchr/testify/require"
)

func TestPromptForParameter(t *testing.T) {
	t.Parallel()

	for _, cc := range []struct {
		name      string
		paramType string
		provided  any
		expected  any
	}{
		{"string", "string", "value", "value"},
		{"emptyString", "string", "", ""},
		{"int", "int", "1", 1},
		{"intNegative", "int", "-1", -1},
		{"boolTrue", "bool", 0, false},
		{"boolFalse", "bool", 1, true},
		{"arrayParam", "array", `["hello", "world"]`, []any{"hello", "world"}},
		{"objectParam", "object", `{"hello": "world"}`, map[string]any{"hello": "world"}},
		{"secureObject", "secureObject", `{"hello": "world"}`, map[string]any{"hello": "world"}},
		{"secureString", "secureString", "value", "value"},
	} {
		tc := cc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockContext := mocks.NewMockContext(context.Background())
			prepareBicepMocks(mockContext)

			p := createBicepProvider(t, mockContext)

			if _, ok := tc.provided.(int); ok {
				mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
					return strings.Contains(options.Message, "for the 'testParam' infrastructure parameter")
				}).Respond(tc.provided)
			} else {
				mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
					return strings.Contains(options.Message, "for the 'testParam' infrastructure parameter")
				}).Respond(tc.provided)
			}

			mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
				return true
			}).Respond(tc.provided)

			value, err := p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
				Type: tc.paramType,
			}, nil, nil)

			require.NoError(t, err)
			require.Equal(t, tc.expected, value)
		})
	}
}

func TestPromptForParameterValidation(t *testing.T) {
	t.Parallel()

	for _, cc := range []struct {
		name     string
		param    azure.ArmTemplateParameterDefinition
		provided []string
		expected any
		messages []string
	}{
		{
			name: "minValue",
			param: azure.ArmTemplateParameterDefinition{
				Type:     "int",
				MinValue: to.Ptr(1),
			},
			provided: []string{"0", "1"},
			expected: 1,
			messages: []string{"at least '1'"},
		},
		{
			name: "maxValue",
			param: azure.ArmTemplateParameterDefinition{
				Type:     "int",
				MaxValue: to.Ptr(10),
			},
			provided: []string{"11", "10"},
			expected: 10,
			messages: []string{"at most '10'"},
		},
		{
			name: "rangeValue",
			param: azure.ArmTemplateParameterDefinition{
				Type:     "int",
				MinValue: to.Ptr(1),
				MaxValue: to.Ptr(10),
			},
			provided: []string{"0", "11", "5"},
			expected: 5,
			messages: []string{"at least '1'", "at most '10'"},
		},
		{
			name: "minLength",
			param: azure.ArmTemplateParameterDefinition{
				Type:      "string",
				MinLength: to.Ptr(1),
			},
			provided: []string{"", "ok"},
			expected: "ok",
			messages: []string{"at least '1'"},
		},
		{
			name: "maxLength",
			param: azure.ArmTemplateParameterDefinition{
				Type:      "string",
				MaxLength: to.Ptr(10),
			},
			provided: []string{"this is a very long string and will be rejected", "ok"},
			expected: "ok",
			messages: []string{"at most '10'"},
		},
		{
			name: "rangeLength",
			param: azure.ArmTemplateParameterDefinition{
				Type:      "string",
				MinLength: to.Ptr(1),
				MaxLength: to.Ptr(10),
			},
			provided: []string{"this is a very long string and will be rejected", "", "ok"},
			expected: "ok",
			messages: []string{"at least '1'", "at most '10'"},
		},
		{
			name: "badInt",
			param: azure.ArmTemplateParameterDefinition{
				Type: "int",
			},
			provided: []string{"bad", "100"},
			expected: 100,
			messages: []string{"failed to convert 'bad' to an integer"},
		},
		{
			name: "badJsonObject",
			param: azure.ArmTemplateParameterDefinition{
				Type: "object",
			},
			provided: []string{"[]", "{}"},
			expected: make(map[string]any),
			messages: []string{"failed to parse value as a JSON object"},
		},
		{
			name: "badJsonArray",
			param: azure.ArmTemplateParameterDefinition{
				Type: "array",
			},
			provided: []string{"{}", "[]"},
			expected: []any{},
			messages: []string{"failed to parse value as a JSON array"},
		},
	} {
		tc := cc

		t.Run(tc.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())
			prepareBicepMocks(mockContext)

			p := createBicepProvider(t, mockContext)

			mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
				return strings.Contains(options.Message, "for the 'testParam' infrastructure parameter")
			}).RespondFn(func(options input.ConsoleOptions) (any, error) {
				ret := tc.provided[0]
				tc.provided = tc.provided[1:]
				return ret, nil
			})

			value, err := p.promptForParameter(*mockContext.Context, "testParam", tc.param, nil, nil)
			require.NoError(t, err)
			require.Equal(t, tc.expected, value)

			outputLog := mockContext.Console.Output()
			for _, msg := range tc.messages {
				match := false
				for _, line := range outputLog {
					match = match || strings.Contains(line, msg)
				}
				require.True(t, match, "the output log: %#v should have contained '%s' but did not", outputLog, msg)
			}
		})
	}
}

func TestPromptForParameterAllowedValues(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())

	prepareBicepMocks(mockContext)

	p := createBicepProvider(t, mockContext)

	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "for the 'testParam' infrastructure parameter")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		require.Equal(t, 3, len(options.Options))

		return 1, nil
	})

	value, err := p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
		Type:          "string",
		AllowedValues: to.Ptr([]any{"three", "good", "choices"}),
	}, nil, nil)

	require.NoError(t, err)
	require.Equal(t, "good", value)

	value, err = p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
		Type:          "int",
		AllowedValues: to.Ptr([]any{10, 20, 30}),
	}, nil, nil)

	require.NoError(t, err)
	require.Equal(t, 20, value)
}

func TestPromptForParametersLocation(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	prepareBicepMocks(mockContext)

	env := environment.New("test")
	accountManager := &mockaccount.MockAccountManager{
		Subscriptions: []account.Subscription{
			{
				Id:   "00000000-0000-0000-0000-000000000000",
				Name: "test",
			},
		},
		Locations: []account.Location{
			{
				Name:                "eastus",
				DisplayName:         "East US",
				RegionalDisplayName: "(US) East US",
			},
			{
				Name:                "eastus2",
				DisplayName:         "East US 2",
				RegionalDisplayName: "(US) East US 2",
			},
			{
				Name:                "westus",
				DisplayName:         "West US",
				RegionalDisplayName: "(US) West US",
			},
		},
	}

	p := createBicepProvider(t, mockContext)
	p.prompters = prompt.NewDefaultPrompter(
		env,
		mockContext.Console,
		accountManager,
		p.resourceService,
		cloud.AzurePublic(),
	)

	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "'unfilteredLocation")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		require.Len(t, options.Options, 3)
		return 1, nil
	})

	value, err := p.promptForParameter(*mockContext.Context, "unfilteredLocation", azure.ArmTemplateParameterDefinition{
		Type: "string",
		Metadata: map[string]json.RawMessage{
			"azd": json.RawMessage(`{"type": "location"}`),
		},
	}, nil, nil)

	require.NoError(t, err)
	require.Equal(t, "eastus2", value)

	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "filteredLocation")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		require.Len(t, options.Options, 1)
		return 0, nil
	})

	value, err = p.promptForParameter(*mockContext.Context, "filteredLocation", azure.ArmTemplateParameterDefinition{
		Type: "string",
		Metadata: map[string]json.RawMessage{
			"azd": json.RawMessage(`{"type": "location"}`),
		},
		AllowedValues: &[]any{"westus"},
	}, nil, nil)

	require.NoError(t, err)
	require.Equal(t, "westus", value)
}

type mockCurrentPrincipal struct{}

func (m *mockCurrentPrincipal) CurrentPrincipalId(_ context.Context) (string, error) {
	return "11111111-1111-1111-1111-111111111111", nil
}

func (m *mockCurrentPrincipal) CurrentPrincipalType(_ context.Context) (provisioning.PrincipalType, error) {
	return provisioning.UserType, nil
}

func TestPromptForParameterOverrideDefault(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())

	prepareBicepMocks(mockContext)

	p := createBicepProvider(t, mockContext)

	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "for the 'testParam' infrastructure parameter")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		require.Equal(t, 3, len(options.Options))
		require.Equal(t, "good", options.DefaultValue)
		return 1, nil
	})

	value, err := p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
		Type:          "string",
		AllowedValues: to.Ptr([]any{"three", "good", "choices"}),
		Metadata: map[string]json.RawMessage{
			"azd": json.RawMessage(`{"default": "good"}`),
		},
	}, nil, nil)

	require.NoError(t, err)
	require.Equal(t, "good", value)
}

func TestPromptForParameterOverrideDefaultError(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())

	prepareBicepMocks(mockContext)

	p := createBicepProvider(t, mockContext)

	_, err := p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
		Type:          "string",
		AllowedValues: to.Ptr([]any{"three", "good", "choices"}),
		Metadata: map[string]json.RawMessage{
			"azd": json.RawMessage(`{"default": "other"}`),
		},
	}, nil, nil)

	require.Error(t, err)
}

func TestPromptForParameterEmptyAllowedValuesError(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())

	prepareBicepMocks(mockContext)

	p := createBicepProvider(t, mockContext)

	_, err := p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
		Type:          "string",
		AllowedValues: to.Ptr([]any{}),
	}, nil, nil)

	require.Error(t, err)
}

func TestPromptForParameterBoolDefaultType(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())

	prepareBicepMocks(mockContext)

	p := createBicepProvider(t, mockContext)

	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "for the 'testParam' infrastructure parameter")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		require.Equal(t, 2, len(options.Options))
		require.Equal(t, "True", options.DefaultValue)
		return 1, nil
	})

	value, err := p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
		Type: "bool",
		Metadata: map[string]json.RawMessage{
			"azd": json.RawMessage(`{"default": true}`)},
	}, nil, nil)

	require.NoError(t, err)
	require.Equal(t, true, value)
}

func TestPromptForParameterBoolDefaultStringType(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())

	prepareBicepMocks(mockContext)

	p := createBicepProvider(t, mockContext)

	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "for the 'testParam' infrastructure parameter")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		require.Equal(t, 2, len(options.Options))
		require.Equal(t, "False", options.DefaultValue)
		return 0, nil
	})

	value, err := p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
		Type: "bool",
		Metadata: map[string]json.RawMessage{
			"azd": json.RawMessage(`{"default": "false"}`)},
	}, nil, nil)

	require.NoError(t, err)
	require.Equal(t, false, value)
}

func TestPromptForParameterNumberDefaultType(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())

	prepareBicepMocks(mockContext)

	p := createBicepProvider(t, mockContext)

	mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "for the 'testParam' infrastructure parameter")
	}).Respond("33")

	value, err := p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
		Type: "int",
		Metadata: map[string]json.RawMessage{
			"azd": json.RawMessage(`{"default": 33}`)},
	}, nil, nil)

	require.NoError(t, err)
	require.Equal(t, 33, value)
}

func TestPromptForParameterNumberDefaultStringTypeError(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())

	prepareBicepMocks(mockContext)

	p := createBicepProvider(t, mockContext)

	mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "for the 'testParam' infrastructure parameter")
	}).Respond("33")

	_, err := p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
		Type: "int",
		Metadata: map[string]json.RawMessage{
			"azd": json.RawMessage(`{"default": "33"}`)},
	}, nil, nil)

	require.Error(t, err)
}

func TestTopoSortParameterPromptsNoDependencies(t *testing.T) {
	t.Parallel()

	prompts := []parameterPromptEntry{
		{key: "c", param: azure.ArmTemplateParameterDefinition{Type: "string"}},
		{key: "a", param: azure.ArmTemplateParameterDefinition{Type: "string"}},
		{key: "b", param: azure.ArmTemplateParameterDefinition{Type: "string"}},
	}

	template := azure.ArmTemplate{
		Parameters: azure.ArmTemplateParameterDefinitions{
			"a": {Type: "string"},
			"b": {Type: "string"},
			"c": {Type: "string"},
		},
	}

	sorted, err := topoSortParameterPrompts(prompts, template, nil)
	require.NoError(t, err)
	// With no dependencies, order is preserved (stable)
	require.Len(t, sorted, 3)
	require.Equal(t, "c", sorted[0].key)
	require.Equal(t, "a", sorted[1].key)
	require.Equal(t, "b", sorted[2].key)
}

func TestTopoSortParameterPromptsDependencyOrdering(t *testing.T) {
	t.Parallel()

	// location depends on modelName and modelCapacity via $(p:...) references
	prompts := []parameterPromptEntry{
		{key: "aiLocation", param: azure.ArmTemplateParameterDefinition{
			Type: "string",
			Metadata: map[string]json.RawMessage{
				"azd": json.RawMessage(`{"type": "location", "usageName": ["$(p:modelName), $(p:modelCapacity)"]}`),
			},
		}},
		{key: "modelName", param: azure.ArmTemplateParameterDefinition{Type: "string"}},
		{key: "modelCapacity", param: azure.ArmTemplateParameterDefinition{Type: "int"}},
	}

	template := azure.ArmTemplate{
		Parameters: azure.ArmTemplateParameterDefinitions{
			"aiLocation":    {Type: "string"},
			"modelName":     {Type: "string"},
			"modelCapacity": {Type: "int"},
		},
	}

	sorted, err := topoSortParameterPrompts(prompts, template, nil)
	require.NoError(t, err)
	require.Len(t, sorted, 3)

	// modelName and modelCapacity must come before aiLocation
	keyOrder := make(map[string]int)
	for i, s := range sorted {
		keyOrder[s.key] = i
	}
	require.Less(t, keyOrder["modelName"], keyOrder["aiLocation"])
	require.Less(t, keyOrder["modelCapacity"], keyOrder["aiLocation"])
}

func TestTopoSortParameterPromptsDependencyAlreadyResolved(t *testing.T) {
	t.Parallel()

	// location depends on modelName, but modelName is already resolved
	prompts := []parameterPromptEntry{
		{key: "aiLocation", param: azure.ArmTemplateParameterDefinition{
			Type: "string",
			Metadata: map[string]json.RawMessage{
				"azd": json.RawMessage(`{"type": "location", "usageName": ["$(p:modelName), 10"]}`),
			},
		}},
	}

	template := azure.ArmTemplate{
		Parameters: azure.ArmTemplateParameterDefinitions{
			"aiLocation": {Type: "string"},
			"modelName":  {Type: "string"},
		},
	}

	alreadyResolved := map[string]any{"modelName": "gpt-4o"}

	sorted, err := topoSortParameterPrompts(prompts, template, alreadyResolved)
	require.NoError(t, err)
	require.Len(t, sorted, 1)
	require.Equal(t, "aiLocation", sorted[0].key)
}

func TestTopoSortParameterPromptsCircularDependency(t *testing.T) {
	t.Parallel()

	// A depends on B and B depends on A
	prompts := []parameterPromptEntry{
		{key: "paramA", param: azure.ArmTemplateParameterDefinition{
			Type: "string",
			Metadata: map[string]json.RawMessage{
				"azd": json.RawMessage(`{"type": "location", "usageName": ["$(p:paramB)"]}`),
			},
		}},
		{key: "paramB", param: azure.ArmTemplateParameterDefinition{
			Type: "string",
			Metadata: map[string]json.RawMessage{
				"azd": json.RawMessage(`{"type": "location", "usageName": ["$(p:paramA)"]}`),
			},
		}},
	}

	template := azure.ArmTemplate{
		Parameters: azure.ArmTemplateParameterDefinitions{
			"paramA": {Type: "string"},
			"paramB": {Type: "string"},
		},
	}

	_, err := topoSortParameterPrompts(prompts, template, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "circular parameter dependency")
}

func TestTopoSortParameterPromptsUnknownReference(t *testing.T) {
	t.Parallel()

	prompts := []parameterPromptEntry{
		{key: "aiLocation", param: azure.ArmTemplateParameterDefinition{
			Type: "string",
			Metadata: map[string]json.RawMessage{
				"azd": json.RawMessage(`{"type": "location", "usageName": ["$(p:nonExistent)"]}`),
			},
		}},
	}

	template := azure.ArmTemplate{
		Parameters: azure.ArmTemplateParameterDefinitions{
			"aiLocation": {Type: "string"},
		},
	}

	_, err := topoSortParameterPrompts(prompts, template, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown parameter 'nonExistent'")
}

func TestTopoSortParameterPromptsDependencyHasDefault(t *testing.T) {
	t.Parallel()

	// location depends on modelName, but modelName has a default value in the template
	prompts := []parameterPromptEntry{
		{key: "aiLocation", param: azure.ArmTemplateParameterDefinition{
			Type: "string",
			Metadata: map[string]json.RawMessage{
				"azd": json.RawMessage(`{"type": "location", "usageName": ["$(p:modelName), 10"]}`),
			},
		}},
	}

	template := azure.ArmTemplate{
		Parameters: azure.ArmTemplateParameterDefinitions{
			"aiLocation": {Type: "string"},
			"modelName":  {Type: "string", DefaultValue: "gpt-4o"},
		},
	}

	sorted, err := topoSortParameterPrompts(prompts, template, nil)
	require.NoError(t, err)
	require.Len(t, sorted, 1)
	require.Equal(t, "aiLocation", sorted[0].key)
}

// TestResolveUsageNamesWithReferencesFullUsageName verifies that when a $(p:...) reference
// resolves to a string whose first token already looks like a full SKU usage name
// (i.e. contains a dot, e.g. "OpenAI.GlobalStandard.gpt-4o"), the AI model catalog lookup
// is skipped and the token is used as-is.  This allows templates to hard-code the provider
// and SKU tier while only parameterizing the capacity.
func TestResolveUsageNamesWithReferencesFullUsageName(t *testing.T) {
	t.Parallel()

	// A minimal BicepProvider is sufficient — the catalog path must not be triggered
	// (aiModelService == nil would panic if it were).
	p := &BicepProvider{}

	mockContext := mocks.NewMockContext(context.Background())
	resolvedValues := map[string]any{
		"cap": "10",
	}

	result, err := p.resolveUsageNamesWithReferences(
		*mockContext.Context,
		[]string{
			// Full SKU usage name — catalog lookup must be skipped
			"OpenAI.GlobalStandard.gpt-4o, $(p:cap)",
			// Constant entry (no references) — should pass through unchanged
			"OpenAI.Standard.gpt-4, 5",
		},
		resolvedValues,
	)
	require.NoError(t, err)
	require.Len(t, result, 2)
	require.Equal(t, "OpenAI.GlobalStandard.gpt-4o, 10", result[0])
	require.Equal(t, "OpenAI.Standard.gpt-4, 5", result[1])
}

// TestResolveUsageNamesWithReferencesFullUsageNameNoCapacity verifies pass-through
// when only the usage name itself is parameterised (no capacity token).
func TestResolveUsageNamesWithReferencesFullUsageNameNoCapacity(t *testing.T) {
	t.Parallel()

	p := &BicepProvider{}

	mockContext := mocks.NewMockContext(context.Background())
	resolvedValues := map[string]any{
		"usageName": "OpenAI.DataZoneStandard.gpt-4o",
	}

	result, err := p.resolveUsageNamesWithReferences(
		*mockContext.Context,
		[]string{"$(p:usageName)"},
		resolvedValues,
	)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, "OpenAI.DataZoneStandard.gpt-4o", result[0])
}
