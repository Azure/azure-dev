package bicep

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
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

			mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(args.Cmd, "bicep") && args.Args[0] == "--version"
			}).Respond(exec.RunResult{
				Stdout: "Bicep CLI version 0.12.40 (41892bd0fb)",
				Stderr: "",
			})

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
			})

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
				MinValue: convert.RefOf(1),
			},
			provided: []string{"0", "1"},
			expected: 1,
			messages: []string{"at least '1'"},
		},
		{
			name: "maxValue",
			param: azure.ArmTemplateParameterDefinition{
				Type:     "int",
				MaxValue: convert.RefOf(10),
			},
			provided: []string{"11", "10"},
			expected: 10,
			messages: []string{"at most '10'"},
		},
		{
			name: "rangeValue",
			param: azure.ArmTemplateParameterDefinition{
				Type:     "int",
				MinValue: convert.RefOf(1),
				MaxValue: convert.RefOf(10),
			},
			provided: []string{"0", "11", "5"},
			expected: 5,
			messages: []string{"at least '1'", "at most '10'"},
		},
		{
			name: "minLength",
			param: azure.ArmTemplateParameterDefinition{
				Type:      "string",
				MinLength: convert.RefOf(1),
			},
			provided: []string{"", "ok"},
			expected: "ok",
			messages: []string{"at least '1'"},
		},
		{
			name: "maxLength",
			param: azure.ArmTemplateParameterDefinition{
				Type:      "string",
				MaxLength: convert.RefOf(10),
			},
			provided: []string{"this is a very long string and will be rejected", "ok"},
			expected: "ok",
			messages: []string{"at most '10'"},
		},
		{
			name: "rangeLength",
			param: azure.ArmTemplateParameterDefinition{
				Type:      "string",
				MinLength: convert.RefOf(1),
				MaxLength: convert.RefOf(10),
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

			mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(args.Cmd, "bicep") && args.Args[0] == "--version"
			}).Respond(exec.RunResult{
				Stdout: "Bicep CLI version 0.12.40 (41892bd0fb)",
				Stderr: "",
			})

			mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
				return strings.Contains(options.Message, "for the 'testParam' infrastructure parameter")
			}).RespondFn(func(options input.ConsoleOptions) (any, error) {
				ret := tc.provided[0]
				tc.provided = tc.provided[1:]
				return ret, nil
			})

			p := createBicepProvider(t, mockContext)

			value, err := p.promptForParameter(*mockContext.Context, "testParam", tc.param)
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

	preparePlanningMocks(mockContext)

	p := createBicepProvider(t, mockContext)

	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "for the 'testParam' infrastructure parameter")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		require.Equal(t, 3, len(options.Options))

		return 1, nil
	})

	value, err := p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
		Type:          "string",
		AllowedValues: convert.RefOf([]any{"three", "good", "choices"}),
	})

	require.NoError(t, err)
	require.Equal(t, "good", value)

	value, err = p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
		Type:          "int",
		AllowedValues: convert.RefOf([]any{10, 20, 30}),
	})

	require.NoError(t, err)
	require.Equal(t, 20, value)
}

func TestPromptForParametersLocation(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: "Bicep CLI version 0.12.40 (41892bd0fb)",
		Stderr: "",
	})

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.Path == "/subscriptions/SUBSCRIPTION_ID/locations"
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, 200, armsubscriptions.LocationListResult{
			Value: []*armsubscriptions.Location{
				{
					Name:                to.Ptr("eastus"),
					DisplayName:         to.Ptr("East US"),
					RegionalDisplayName: to.Ptr("(US) East US"),
					Metadata: &armsubscriptions.LocationMetadata{
						RegionType: to.Ptr(armsubscriptions.RegionType("Physical")),
					},
				},
				{
					Name:                to.Ptr("eastus2"),
					DisplayName:         to.Ptr("East US 2"),
					RegionalDisplayName: to.Ptr("(US) East US 2"),
					Metadata: &armsubscriptions.LocationMetadata{
						RegionType: to.Ptr(armsubscriptions.RegionType("Physical")),
					},
				},
				{
					Name:                to.Ptr("westus"),
					DisplayName:         to.Ptr("West US"),
					RegionalDisplayName: to.Ptr("(US) West US"),
					Metadata: &armsubscriptions.LocationMetadata{
						RegionType: to.Ptr(armsubscriptions.RegionType("Physical")),
					},
				},
			},
		})
	})

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.Path, "/subscriptions/") && strings.Count(request.URL.Path, "/") == 2
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, 200, armsubscriptions.Subscription{
			SubscriptionID: to.Ptr("SUBSCRIPTION_ID"),
			TenantID:       to.Ptr("TENANT_ID"),
			DisplayName:    to.Ptr("A test subscription"),
		})
	})

	p := createBicepProvider(t, mockContext)

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
	})

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
	})

	require.NoError(t, err)
	require.Equal(t, "westus", value)
}
