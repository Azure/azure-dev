// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/inputs"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinputs"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestInputProviderRequiredBicepLocation(t *testing.T) {
	for _, tt := range []struct {
		name           string
		parameter      azure.ArmTemplateParameterDefinition
		otherParameter *azure.ArmTemplateParameterDefinition
		fileValue      string
		saved          string
		existing       string
		process        string
		emptyEnv       bool
		repeated       bool
		otherMissing   bool
		resolved       *string
		options        []inputs.Option
		quotaFailure   bool
		usageFailure   bool
		wantCalls      int
		wantLocation   string
		wantErr        string
	}{
		{name: "missing mapped location", fileValue: "${AZURE_LOCATION}", resolved: new("eastus2"),
			wantCalls: 1, wantLocation: "eastus2"},
		{name: "shared binding resolves once", fileValue: "${AZURE_LOCATION}", resolved: new("eastus2"),
			repeated: true, wantCalls: 1, wantLocation: "eastus2"},
		{name: "shared allowed values are intersected before selection", fileValue: "${AZURE_LOCATION}", repeated: true,
			options: []inputs.Option{{Value: "eastus2", Recommended: true}, {Value: "westus3"}},
			parameter: azure.ArmTemplateParameterDefinition{
				Type: "string", AllowedValues: new([]any{"eastus2", "westus3"}),
			},
			otherParameter: new(azure.ArmTemplateParameterDefinition{Type: "string", AllowedValues: new([]any{"westus3"})}),
			wantCalls:      1, wantLocation: "westus3"},
		{name: "shared length constraints filter before selection", fileValue: "${AZURE_LOCATION}", repeated: true,
			options:        []inputs.Option{{Value: "eastus2", Recommended: true}, {Value: "swedencentral"}},
			otherParameter: new(azure.ArmTemplateParameterDefinition{Type: "string", MinLength: new(10)}),
			wantCalls:      1, wantLocation: "swedencentral"},
		{name: "shared quota constraints filter before selection", fileValue: "${AZURE_LOCATION}", repeated: true,
			options: []inputs.Option{{Value: "eastus2", Recommended: true}, {Value: "westus3"}},
			otherParameter: new(azure.ArmTemplateParameterDefinition{Type: "string", Metadata: map[string]json.RawMessage{
				"azd": json.RawMessage(`{"type":"location","usageName":["demo.usage"]}`),
			}}), wantCalls: 1, wantLocation: "westus3"},
		{name: "explicit empty environment preserves missing input", fileValue: "${AZURE_LOCATION}",
			emptyEnv: true, wantErr: "missing required inputs"},
		{name: "parameter value wins", fileValue: "westus3", wantLocation: ""},
		{name: "environment value wins", fileValue: "${AZURE_LOCATION}", existing: "westus3",
			wantLocation: "westus3"},
		{name: "process environment wins", fileValue: "${AZURE_LOCATION}", process: "westus3",
			wantLocation: "westus3"},
		{name: "dotenv wins over process environment", fileValue: "${AZURE_LOCATION}",
			existing: "westus3", process: "eastus2", wantLocation: "westus3"},
		{name: "saved value wins", fileValue: "${AZURE_LOCATION}", saved: "westus3"},
		{name: "default needs no input", fileValue: "${AZURE_LOCATION}",
			parameter: azure.ArmTemplateParameterDefinition{Type: "string", DefaultValue: "westus3"}},
		{name: "unrelated parameter needs no location",
			parameter: azure.ArmTemplateParameterDefinition{Type: "string", DefaultValue: "hello"}},
		{name: "declined preserves missing input error", fileValue: "${AZURE_LOCATION}",
			wantCalls: 1, wantErr: "missing required inputs"},
		{name: "mapped ambiguity retains the complete missing input report", fileValue: "${AZURE_LOCATION}",
			options: []inputs.Option{{Value: "eastus2"}, {Value: "westus3"}}, otherMissing: true,
			wantCalls: 1, wantErr: "missing required inputs"},
		{name: "unmapped ambiguity retains configuration guidance", otherMissing: true,
			options: []inputs.Option{{Value: "eastus2"}, {Value: "westus3"}},
			parameter: azure.ArmTemplateParameterDefinition{Type: "string", Metadata: map[string]json.RawMessage{
				"azd": json.RawMessage(`{"type":"location"}`),
			}}, wantCalls: 1, wantErr: "missing required inputs"},
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
		{name: "usage lookup failure is not skipped", fileValue: "${AZURE_LOCATION}", usageFailure: true,
			options: []inputs.Option{{Value: "eastus2", Recommended: true}, {Value: "westus3"}},
			parameter: azure.ArmTemplateParameterDefinition{Type: "string", Metadata: map[string]json.RawMessage{
				"azd": json.RawMessage(`{"type":"location","usageName":["demo.usage"]}`),
			}}, wantCalls: 1, wantErr: "usage lookup failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(environment.LocationEnvVarName, "")
			require.NoError(t, os.Unsetenv(environment.LocationEnvVarName))
			if tt.process != "" {
				t.Setenv(environment.LocationEnvVarName, tt.process)
			}
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
					if tt.fileValue == "${AZURE_LOCATION}" {
						require.Equal(t, environment.LocationEnvVarName, definition.EnvironmentVariable)
					} else {
						require.Empty(t, definition.EnvironmentVariable)
					}
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
			metadata, _ := parameter.AzdMetadata()
			hasQuota := len(metadata.UsageName) > 0
			if tt.otherParameter != nil {
				otherMetadata, _ := tt.otherParameter.AzdMetadata()
				hasQuota = hasQuota || len(otherMetadata.UsageName) > 0
			}
			var skuCalls atomic.Int32
			if hasQuota {
				mockContext.HttpClient.When(func(req *http.Request) bool {
					return req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/skus")
				}).RespondFn(func(req *http.Request) (*http.Response, error) {
					skuCalls.Add(1)
					if tt.quotaFailure {
						return mocks.CreateHttpResponseWithBody(req, http.StatusBadRequest, json.RawMessage(
							`{"error":{"code":"BadRequest","message":"quota lookup failed"}}`))
					}
					locations := `["westus3"]`
					if tt.usageFailure {
						locations = `["eastus2","westus3"]`
					}
					return mocks.CreateHttpResponseWithBody(req, http.StatusOK, json.RawMessage(
						`{"value":[{"kind":"AIServices","name":"S0","tier":"Standard",`+
							`"resourceType":"accounts","locations":`+locations+`}]}`))
				})
				mockContext.HttpClient.When(func(req *http.Request) bool {
					return req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/usages")
				}).RespondFn(func(req *http.Request) (*http.Response, error) {
					if tt.usageFailure && strings.Contains(req.URL.Path, "/locations/eastus2/") {
						return mocks.CreateHttpResponseWithBody(req, http.StatusBadRequest, json.RawMessage(
							`{"error":{"code":"BadRequest","message":"usage lookup failed"}}`))
					}
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
				if tt.otherParameter != nil {
					definitions["otherLocation"] = *tt.otherParameter
				}
			}
			if tt.otherMissing {
				definitions["database"] = azure.ArmTemplateParameterDefinition{Type: "string"}
			}
			parameters, err := provider.ensureParameters(t.Context(), azure.ArmTemplate{Parameters: definitions})
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				envManager.AssertNotCalled(t, "Save", mock.Anything, mock.Anything)
				if tt.otherMissing {
					missing, ok := errors.AsType[*MissingInputsError](err)
					require.True(t, ok)
					require.Len(t, missing.Inputs, 2)
					require.Contains(t, missing.ToString(""), "infra.parameters.location")
					require.Contains(t, missing.ToString(""), "infra.parameters.database")
				}
			} else {
				require.NoError(t, err)
				if tt.process != "" {
					require.Equal(t, tt.wantLocation, parameters["location"].Value)
				}

				if tt.resolved != nil {
					require.Equal(t, *tt.resolved, parameters["location"].Value)
					if tt.repeated {
						require.Equal(t, *tt.resolved, parameters["otherLocation"].Value)
					}
				}
				if tt.options != nil {
					require.Equal(t, tt.wantLocation, parameters["location"].Value)
					if tt.repeated {
						require.Equal(t, tt.wantLocation, parameters["otherLocation"].Value)
					}
				}
			}
			require.Equal(t, tt.wantCalls, calls)
			require.Equal(t, tt.wantLocation, env.GetLocation())
			if hasQuota {
				require.Equal(t, int32(1), skuCalls.Load(), "quota validation must batch provider candidates")
			}
		})
	}
}

func TestInputProviderBicepParamRetainsNativeLocationFallback(t *testing.T) {
	t.Setenv(environment.LocationEnvVarName, "")
	require.NoError(t, os.Unsetenv(environment.LocationEnvVarName))
	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Console.SetNoPromptMode(true)
	env := environment.New("test")
	env.SetSubscriptionId("subscription")
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, env).Return(nil).Run(func(mock.Arguments) {
		require.Empty(t, env.GetLocation(), "must not persist an unconstrained provider recommendation")
	}).Once()
	ioc.RegisterInstance[account.Manager](mockContext.Container, &mockaccount.MockAccountManager{
		Locations: []account.Location{{Name: "eastus2"}, {Name: "westus3"}},
	})
	calls := 0
	ioc.RegisterInstance[inputs.Resolver](mockContext.Container, mockinputs.OptionsResolver(
		func(context.Context, *v1beta.InputDefinition) ([]inputs.Option, error) {
			calls++
			return []inputs.Option{{Value: "eastus2", Recommended: true}, {Value: "westus3"}}, nil
		}))
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "infra"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "infra", "main.bicep"),
		[]byte("@allowed(['westus3'])\nparam location string\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "infra", "main.bicepparam"),
		[]byte("using './main.bicep'\nparam location = readEnvironmentVariable('AZURE_LOCATION')\n"), 0600))
	provider := &BicepProvider{
		mode: bicepparamMode, env: env, envManager: envManager, serviceLocator: mockContext.Container,
		projectPath: dir, options: provisioning.Options{Path: "infra", Module: "main"},
		prompters: prompt.NewDefaultPrompter(env, mockContext.Console, nil, nil, nil, cloud.AzurePublic()),
	}
	err := provider.EnsureEnv(t.Context())
	missing, ok := errors.AsType[*input.PromptRequiredError](err)
	require.True(t, ok, "%v", err)
	require.Contains(t, missing.ToString(""), environment.LocationEnvVarName)
	require.Zero(t, calls)
	require.Empty(t, env.GetLocation())
	envManager.AssertExpectations(t)
}
