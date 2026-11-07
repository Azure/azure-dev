// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/inputs"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinputs"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestInputProviderRequiredBicepLocation(t *testing.T) {
	for _, tt := range []struct {
		name         string
		parameter    azure.ArmTemplateParameterDefinition
		fileValue    string
		saved        string
		existing     string
		emptyEnv     bool
		repeated     bool
		resolved     *string
		options      []inputs.Option
		quotaFailure bool
		wantCalls    int
		wantLocation string
		wantErr      string
	}{
		{name: "missing mapped location", fileValue: "${AZURE_LOCATION}", resolved: new("eastus2"),
			wantCalls: 1, wantLocation: "eastus2"},
		{name: "shared binding resolves once", fileValue: "${AZURE_LOCATION}", resolved: new("eastus2"),
			repeated: true, wantCalls: 1, wantLocation: "eastus2"},
		{name: "explicit empty environment preserves missing input", fileValue: "${AZURE_LOCATION}",
			emptyEnv: true, wantErr: "missing required inputs"},
		{name: "parameter value wins", fileValue: "westus3", wantLocation: ""},
		{name: "environment value wins", fileValue: "${AZURE_LOCATION}", existing: "westus3",
			wantLocation: "westus3"},
		{name: "saved value wins", fileValue: "${AZURE_LOCATION}", saved: "westus3"},
		{name: "default needs no input", fileValue: "${AZURE_LOCATION}",
			parameter: azure.ArmTemplateParameterDefinition{Type: "string", DefaultValue: "westus3"}},
		{name: "unrelated parameter needs no location",
			parameter: azure.ArmTemplateParameterDefinition{Type: "string", DefaultValue: "hello"}},
		{name: "declined preserves missing input error", fileValue: "${AZURE_LOCATION}",
			wantCalls: 1, wantErr: "missing required inputs"},
		{name: "disallowed value", fileValue: "${AZURE_LOCATION}", resolved: new("eastus2"), wantCalls: 1,
			parameter: azure.ArmTemplateParameterDefinition{Type: "string", AllowedValues: new([]any{"westus3"})},
			wantErr:   "no valid input provider options"},
		{name: "invalid location", fileValue: "${AZURE_LOCATION}", resolved: new("invalid"),
			wantCalls: 1, wantErr: "no valid input provider options"},
		{name: "empty is not decline", fileValue: "${AZURE_LOCATION}", resolved: new(""),
			wantCalls: 1, wantErr: "no valid input provider options"},
		{name: "empty allowed list", fileValue: "${AZURE_LOCATION}",
			parameter: azure.ArmTemplateParameterDefinition{Type: "string", AllowedValues: new([]any{})},
			wantErr:   "no allowed values"},
		{name: "length constraint", fileValue: "${AZURE_LOCATION}", resolved: new("eastus2"), wantCalls: 1,
			parameter: azure.ArmTemplateParameterDefinition{Type: "string", MinLength: new(10)},
			wantErr:   "no valid input provider options"},
		{name: "allowed values filter before recommendation", fileValue: "${AZURE_LOCATION}",
			options:   []inputs.Option{{Value: "eastus2", Recommended: true}, {Value: "westus3"}},
			parameter: azure.ArmTemplateParameterDefinition{Type: "string", AllowedValues: new([]any{"westus3"})},
			wantCalls: 1, wantLocation: "westus3"},
		{name: "unsupported recommendation is removed", fileValue: "${AZURE_LOCATION}",
			options:   []inputs.Option{{Value: "invalid", Recommended: true}, {Value: "eastus2"}},
			wantCalls: 1, wantLocation: "eastus2"},
		{name: "native length constraint filters before selection", fileValue: "${AZURE_LOCATION}",
			options:   []inputs.Option{{Value: "eastus2", Recommended: true}, {Value: "swedencentral"}},
			parameter: azure.ArmTemplateParameterDefinition{Type: "string", MinLength: new(10)},
			wantCalls: 1, wantLocation: "swedencentral"},
		{name: "quota filters before recommendation", fileValue: "${AZURE_LOCATION}",
			options: []inputs.Option{{Value: "eastus2", Recommended: true}, {Value: "westus3"}},
			parameter: azure.ArmTemplateParameterDefinition{Type: "string", Metadata: map[string]json.RawMessage{
				"azd": json.RawMessage(`{"type":"location","usageName":["demo.usage"]}`),
			}}, wantCalls: 1, wantLocation: "westus3"},
		{name: "quota lookup failure is not skipped", fileValue: "${AZURE_LOCATION}", quotaFailure: true,
			options: []inputs.Option{{Value: "eastus2", Recommended: true}, {Value: "westus3"}},
			parameter: azure.ArmTemplateParameterDefinition{Type: "string", Metadata: map[string]json.RawMessage{
				"azd": json.RawMessage(`{"type":"location","usageName":["demo.usage"]}`),
			}}, wantCalls: 1, wantErr: "quota lookup failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(environment.LocationEnvVarName, "")
			require.NoError(t, os.Unsetenv(environment.LocationEnvVarName))
			if tt.emptyEnv {
				t.Setenv(environment.LocationEnvVarName, "")
			}
			mockContext := mocks.NewMockContext(t.Context())
			mockContext.Console.SetNoPromptMode(true)
			env := environment.New("test")
			env.SetSubscriptionId("subscription")
			if tt.existing != "" {
				env.SetLocation(tt.existing)
			}
			if tt.saved != "" {
				require.NoError(t, env.Config.Set("infra.parameters.location", tt.saved))
			}
			envManager := &mockenv.MockEnvManager{}
			envManager.On("Save", mock.Anything, env).Return(nil)
			ioc.RegisterInstance[account.Manager](mockContext.Container, &mockaccount.MockAccountManager{
				Locations: []account.Location{{Name: "eastus2"}, {Name: "westus3"}, {Name: "swedencentral"}},
			})
			calls := 0
			ioc.RegisterInstance[inputs.Resolver](mockContext.Container, mockinputs.OptionsResolver(
				func(_ context.Context, definition *v1beta.InputDefinition) ([]inputs.Option, error) {
					calls++
					require.Equal(t, inputs.AzureLocationKind, definition.Kind)
					require.Equal(t, environment.LocationEnvVarName, definition.EnvironmentVariable)
					if tt.options != nil {
						return tt.options, nil
					}
					if tt.resolved == nil {
						return nil, nil
					}
					return []inputs.Option{{Value: *tt.resolved, Recommended: true}}, nil
				},
			))
			dir := t.TempDir()
			if tt.fileValue != "" {
				parameters := map[string]any{"location": map[string]any{"value": tt.fileValue}}
				if tt.repeated {
					parameters["otherLocation"] = map[string]any{"value": tt.fileValue}
				}
				data, err := json.Marshal(map[string]any{"parameters": parameters})
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "main.parameters.json"), data, 0600))
			}
			parameter := tt.parameter
			if parameter.Type == "" {
				parameter.Type = "string"
			}
			provider := &BicepProvider{
				env: env, envManager: envManager, console: mockContext.Console,
				path: filepath.Join(dir, "main.bicep"), options: provisioning.Options{Module: "main"},
				curPrincipal: &mockCurrentPrincipal{}, serviceLocator: mockContext.Container,
			}
			if len(parameter.Metadata) > 0 {
				mockContext.HttpClient.When(func(req *http.Request) bool {
					return req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/skus")
				}).RespondFn(func(req *http.Request) (*http.Response, error) {
					if tt.quotaFailure {
						return mocks.CreateHttpResponseWithBody(req, http.StatusBadRequest, json.RawMessage(
							`{"error":{"code":"BadRequest","message":"quota lookup failed"}}`))
					}
					return mocks.CreateHttpResponseWithBody(req, http.StatusOK, json.RawMessage(
						`{"value":[{"kind":"AIServices","name":"S0","tier":"Standard",`+
							`"resourceType":"accounts","locations":["westus3"]}]}`))
				})
				mockContext.HttpClient.When(func(req *http.Request) bool {
					return req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/usages")
				}).RespondFn(func(req *http.Request) (*http.Response, error) {
					return mocks.CreateHttpResponseWithBody(req, http.StatusOK, json.RawMessage(`{"value":[]}`))
				})
				client := azapi.NewAzureClient(mockaccount.SubscriptionCredentialProviderFunc(
					func(context.Context, string) (azcore.TokenCredential, error) {
						return mockContext.Credentials, nil
					}), mockContext.ArmClientOptions)
				provider.aiModelService = ai.NewAiModelService(client, nil)
			}
			definitions := azure.ArmTemplateParameterDefinitions{"location": parameter}
			if tt.repeated {
				definitions["otherLocation"] = parameter
			}
			parameters, err := provider.ensureParameters(t.Context(), azure.ArmTemplate{Parameters: definitions})
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				envManager.AssertNotCalled(t, "Save", mock.Anything, mock.Anything)
			} else {
				require.NoError(t, err)
				if tt.resolved != nil {
					require.Equal(t, *tt.resolved, parameters["location"].Value)
					if tt.repeated {
						require.Equal(t, *tt.resolved, parameters["otherLocation"].Value)
					}
				}
				if tt.options != nil {
					require.Equal(t, tt.wantLocation, parameters["location"].Value)
				}
			}
			require.Equal(t, tt.wantCalls, calls)
			require.Equal(t, tt.wantLocation, env.GetLocation())
		})
	}
}
