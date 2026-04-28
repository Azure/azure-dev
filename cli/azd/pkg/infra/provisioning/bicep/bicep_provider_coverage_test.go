// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

// TestBicepProviderName verifies the provider name is returned.
func TestBicepProviderName(t *testing.T) {
	t.Parallel()

	p := &BicepProvider{}
	require.Equal(t, "Bicep", p.Name())
}

// TestParametersHash verifies hash behaviour across equal and different inputs.
func TestParametersHash(t *testing.T) {
	t.Parallel()

	defs := azure.ArmTemplateParameterDefinitions{
		"foo": {Type: "string", DefaultValue: "a"},
		"bar": {Type: "int", DefaultValue: 1},
	}

	t.Run("DeterministicWithDefaults", func(t *testing.T) {
		t.Parallel()
		h1, err := parametersHash(defs, azure.ArmParameters{})
		require.NoError(t, err)
		h2, err := parametersHash(defs, azure.ArmParameters{})
		require.NoError(t, err)
		require.Equal(t, h1, h2)
		require.NotEmpty(t, h1)
	})

	t.Run("ProvidedValueOverridesDefault", func(t *testing.T) {
		t.Parallel()
		hDefault, err := parametersHash(defs, azure.ArmParameters{})
		require.NoError(t, err)
		hOverridden, err := parametersHash(defs, azure.ArmParameters{
			"foo": {Value: "b"},
		})
		require.NoError(t, err)
		require.NotEqual(t, hDefault, hOverridden)
	})

	t.Run("SameFinalValueProducesSameHash", func(t *testing.T) {
		t.Parallel()
		h1, err := parametersHash(defs, azure.ArmParameters{"foo": {Value: "a"}})
		require.NoError(t, err)
		h2, err := parametersHash(defs, azure.ArmParameters{})
		require.NoError(t, err)
		require.Equal(t, h1, h2)
	})
}

// TestPrevDeploymentEqualToCurrent exhaustively covers the negative branches.
func TestPrevDeploymentEqualToCurrent(t *testing.T) {
	t.Parallel()

	templateHash := "TEMPLATE_HASH"
	paramsHash := "PARAMS_HASH"

	matchingTags := func() map[string]*string {
		return map[string]*string{
			azure.TagKeyAzdDeploymentStateParamHashName: new(paramsHash),
		}
	}

	cases := []struct {
		name string
		prev *azapi.ResourceDeployment
		want bool
	}{
		{
			name: "NilPrev",
			prev: nil,
			want: false,
		},
		{
			name: "NoTags",
			prev: &azapi.ResourceDeployment{TemplateHash: new(templateHash)},
			want: false,
		},
		{
			name: "DifferentTemplateHash",
			prev: &azapi.ResourceDeployment{
				TemplateHash: new("OTHER"),
				Tags:         matchingTags(),
			},
			want: false,
		},
		{
			name: "MissingParamHashTag",
			prev: &azapi.ResourceDeployment{
				TemplateHash: new(templateHash),
				Tags:         map[string]*string{"unrelated": new("x")},
			},
			want: false,
		},
		{
			name: "DifferentParamHash",
			prev: &azapi.ResourceDeployment{
				TemplateHash: new(templateHash),
				Tags: map[string]*string{
					azure.TagKeyAzdDeploymentStateParamHashName: new("DIFF"),
				},
			},
			want: false,
		},
		{
			name: "Equal",
			prev: &azapi.ResourceDeployment{
				TemplateHash: new(templateHash),
				Tags:         matchingTags(),
			},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := prevDeploymentEqualToCurrent(tc.prev, templateHash, paramsHash)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestLogDS simply ensures logDS does not panic for the common formatting paths.
func TestLogDS(t *testing.T) {
	t.Parallel()
	require.NotPanics(t, func() {
		logDS("plain message")
		logDS("formatted %s %d", "value", 1)
	})
}

// TestConvertPropertyChanges covers nil inputs, nil entries, recursion, and type mapping.
func TestConvertPropertyChanges(t *testing.T) {
	t.Parallel()

	t.Run("Nil", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, convertPropertyChanges(nil))
	})

	t.Run("SkipsNilEntries", func(t *testing.T) {
		t.Parallel()
		changes := []*armresources.WhatIfPropertyChange{nil, nil}
		result := convertPropertyChanges(changes)
		require.Empty(t, result)
	})

	t.Run("ConvertsAndRecurses", func(t *testing.T) {
		t.Parallel()
		modify := armresources.PropertyChangeTypeModify
		create := armresources.PropertyChangeTypeCreate
		childPath := "child"
		parent := &armresources.WhatIfPropertyChange{
			Path:               new("parent"),
			PropertyChangeType: &modify,
			Before:             "old",
			After:              "new",
			Children: []*armresources.WhatIfPropertyChange{
				{
					Path:               &childPath,
					PropertyChangeType: &create,
					After:              "created",
				},
			},
		}
		result := convertPropertyChanges([]*armresources.WhatIfPropertyChange{parent})
		require.Len(t, result, 1)
		require.Equal(t, "parent", result[0].Path)
		require.Equal(t, "old", result[0].Before)
		require.Equal(t, "new", result[0].After)
		require.Equal(t, provisioning.PropertyChangeType(modify), result[0].ChangeType)
		require.Len(t, result[0].Children, 1)
		require.Equal(t, "child", result[0].Children[0].Path)
		require.Equal(t, provisioning.PropertyChangeType(create), result[0].Children[0].ChangeType)
	})

	t.Run("NilPathAndChangeType", func(t *testing.T) {
		t.Parallel()
		change := &armresources.WhatIfPropertyChange{}
		result := convertPropertyChanges([]*armresources.WhatIfPropertyChange{change})
		require.Len(t, result, 1)
		require.Equal(t, "", result[0].Path)
		require.Nil(t, result[0].Before)
		require.Nil(t, result[0].After)
	})
}

// TestItemsCountAsText covers the normal and panic paths.
func TestItemsCountAsText(t *testing.T) {
	t.Parallel()

	t.Run("PanicsOnEmpty", func(t *testing.T) {
		t.Parallel()
		require.Panics(t, func() {
			_ = itemsCountAsText(nil)
		})
	})

	t.Run("SkipsZeroCountItems", func(t *testing.T) {
		t.Parallel()
		text := itemsCountAsText([]itemToPurge{
			{resourceType: "Key Vaults", count: 0},
			{resourceType: "App Configurations", count: 2},
		})
		require.Contains(t, text, "2 App Configurations")
		require.NotContains(t, text, "Key Vaults")
	})

	t.Run("FormatsSingleItem", func(t *testing.T) {
		t.Parallel()
		text := itemsCountAsText([]itemToPurge{
			{resourceType: "Managed HSMs", count: 1},
		})
		require.Contains(t, text, "1 Managed HSMs")
	})
}

// TestGetDeploymentOptions exercises the deployment-option prompt helper.
func TestGetDeploymentOptions(t *testing.T) {
	t.Parallel()

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		got := getDeploymentOptions(nil)
		require.Empty(t, got)
	})

	t.Run("Formats", func(t *testing.T) {
		t.Parallel()
		ts := time.Date(2024, 5, 10, 14, 30, 0, 0, time.UTC)
		deployments := []*azapi.ResourceDeployment{
			{Name: "dep-1", Timestamp: ts},
			{Name: "dep-2", Timestamp: ts.Add(time.Hour)},
		}
		got := getDeploymentOptions(deployments)
		require.Len(t, got, 2)
		require.Contains(t, got[0], "1.")
		require.Contains(t, got[0], "dep-1")
		require.Contains(t, got[1], "2.")
		require.Contains(t, got[1], "dep-2")
	})
}

// TestConvertToDeployment verifies parameter and output conversion.
func TestConvertToDeployment(t *testing.T) {
	t.Parallel()

	p := &BicepProvider{}
	tpl := azure.ArmTemplate{
		Parameters: azure.ArmTemplateParameterDefinitions{
			"stringParam": {Type: "string", DefaultValue: "hello"},
			"intParam":    {Type: "int", DefaultValue: 1},
		},
		Outputs: azure.ArmTemplateOutputs{
			"endpoint": {Type: "string", Value: "https://example"},
		},
	}

	dep := p.convertToDeployment(tpl)
	require.Len(t, dep.Parameters, 2)
	require.Equal(t, "hello", dep.Parameters["stringParam"].DefaultValue)
	require.Equal(t, string(provisioning.ParameterTypeString), dep.Parameters["stringParam"].Type)
	require.Equal(t, string(provisioning.ParameterTypeNumber), dep.Parameters["intParam"].Type)
	require.Len(t, dep.Outputs, 1)
	require.Equal(t, "https://example", dep.Outputs["endpoint"].Value)
	require.Equal(t, provisioning.ParameterTypeString, dep.Outputs["endpoint"].Type)
}

// TestMustSetParamAsConfig covers the regular and secure paths.
func TestMustSetParamAsConfig(t *testing.T) {
	t.Parallel()

	t.Run("PlainValue", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewEmptyConfig()
		mustSetParamAsConfig("plainParam", "some-value", cfg, false)
		got, has := cfg.Get(configInfraParametersKey + "plainParam")
		require.True(t, has)
		require.Equal(t, "some-value", got)
	})

	t.Run("SecureValue", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewEmptyConfig()
		mustSetParamAsConfig("secureParam", "secret", cfg, true)
		// Secret values are retrieved via Get but stored as references.
		got, has := cfg.Get(configInfraParametersKey + "secureParam")
		require.True(t, has)
		require.NotNil(t, got)
	})

	t.Run("SecureNonStringPanics", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewEmptyConfig()
		require.Panics(t, func() {
			mustSetParamAsConfig("bad", 123, cfg, true)
		})
	})
}

// TestEvalCommandSubstitutionPassthrough verifies that values without a command
// invocation are returned unchanged.
func TestEvalCommandSubstitutionPassthrough(t *testing.T) {
	t.Parallel()

	p := &BicepProvider{}
	for _, input := range []string{"", "plain-value", "https://example.com"} {
		out, err := p.evalCommandSubstitution(t.Context(), input)
		require.NoError(t, err)
		require.Equal(t, input, out)
	}
}

// TestCreateDeploymentFromArmDeployment verifies scope dispatch and errors.
func TestCreateDeploymentFromArmDeployment(t *testing.T) {
	t.Parallel()

	t.Run("SubscriptionScope", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)
		scope := p.deploymentManager.SubscriptionScope("SUBSCRIPTION_ID", "westus2")
		dep, err := p.createDeploymentFromArmDeployment(scope, "dep-name")
		require.NoError(t, err)
		require.NotNil(t, dep)
	})

	t.Run("ResourceGroupScope", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)
		scope := p.deploymentManager.ResourceGroupScope("SUBSCRIPTION_ID", "RG")
		dep, err := p.createDeploymentFromArmDeployment(scope, "dep-name")
		require.NoError(t, err)
		require.NotNil(t, dep)
	})

	t.Run("UnsupportedScope", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)
		_, err := p.createDeploymentFromArmDeployment(unsupportedScope{}, "dep-name")
		require.Error(t, err)
	})
}

// unsupportedScope is a stand-in that implements infra.Scope but is neither a
// resource group nor a subscription scope, exercising the error branch of
// createDeploymentFromArmDeployment.
type unsupportedScope struct{}

func (unsupportedScope) SubscriptionId() string { return "" }
func (unsupportedScope) ListDeployments(ctx context.Context) ([]*azapi.ResourceDeployment, error) {
	return nil, errors.New("not implemented")
}
func (unsupportedScope) Deployment(string) infra.Deployment { return nil }

// TestInferScopeFromEnv covers both scope branches.
func TestInferScopeFromEnv(t *testing.T) {
	t.Parallel()

	t.Run("ResourceGroupScopeWhenEnvSet", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)
		p.env.DotenvSet(environment.ResourceGroupEnvVarName, "my-rg")

		scope, err := p.inferScopeFromEnv()
		require.NoError(t, err)
		rg, ok := scope.(*infra.ResourceGroupScope)
		require.True(t, ok, "expected ResourceGroupScope")
		require.Equal(t, "my-rg", rg.ResourceGroupName())
	})

	t.Run("SubscriptionScopeByDefault", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)

		scope, err := p.inferScopeFromEnv()
		require.NoError(t, err)
		_, ok := scope.(*infra.SubscriptionScope)
		require.True(t, ok, "expected SubscriptionScope, got %T", scope)
	})
}

// TestScopeForTemplate covers subscription, resource group, and unsupported scopes.
func TestScopeForTemplate(t *testing.T) {
	t.Parallel()

	t.Run("Subscription", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)
		tpl := azure.ArmTemplate{
			Schema: "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
		}
		scope, err := p.scopeForTemplate(tpl)
		require.NoError(t, err)
		_, ok := scope.(*infra.SubscriptionScope)
		require.True(t, ok)
	})

	t.Run("ResourceGroup", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)
		p.env.DotenvSet(environment.ResourceGroupEnvVarName, "my-rg")
		tpl := azure.ArmTemplate{
			Schema: "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
		}
		scope, err := p.scopeForTemplate(tpl)
		require.NoError(t, err)
		rg, ok := scope.(*infra.ResourceGroupScope)
		require.True(t, ok)
		require.Equal(t, "my-rg", rg.ResourceGroupName())
	})

	t.Run("Unsupported", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)
		// Empty schema causes TargetScope() to return an unsupported scope or error.
		tpl := azure.ArmTemplate{Schema: "https://example.com/unknown.json#"}
		_, err := p.scopeForTemplate(tpl)
		require.Error(t, err)
	})
}

// TestDefinitionName covers the happy path and edge cases for the helper.
func TestDefinitionName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input, want string
	}{
		{"#/definitions/MyType", "MyType"},
		{"/definitions/Foo", "Foo"},
		{"Bar", "Bar"},
	}
	for _, tc := range cases {
		got, err := definitionName(tc.input)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
}

// TestDefaultPromptValue exercises metadata-driven and allowed-values defaults.
func TestDefaultPromptValue(t *testing.T) {
	t.Parallel()

	t.Run("NilWhenNoMetadataOrAllowedValues", func(t *testing.T) {
		t.Parallel()
		got := defaultPromptValue(azure.ArmTemplateParameterDefinition{})
		require.Nil(t, got)
	})

	t.Run("AllowedValuesFirstString", func(t *testing.T) {
		t.Parallel()
		vals := []any{"westus2", "eastus"}
		got := defaultPromptValue(azure.ArmTemplateParameterDefinition{
			AllowedValues: &vals,
		})
		require.NotNil(t, got)
		require.Equal(t, "westus2", *got)
	})

	t.Run("AllowedValuesNonStringIgnored", func(t *testing.T) {
		t.Parallel()
		vals := []any{42, "eastus"}
		got := defaultPromptValue(azure.ArmTemplateParameterDefinition{
			AllowedValues: &vals,
		})
		require.Nil(t, got)
	})

	t.Run("AzdMetadataLocationDefault", func(t *testing.T) {
		t.Parallel()
		locationType := azure.AzdMetadataTypeLocation
		def := azure.ArmTemplateParameterDefinition{
			Metadata: map[string]json.RawMessage{
				"azd": mustMarshal(t, azure.AzdMetadata{
					Type:    &locationType,
					Default: "westus3",
				}),
			},
		}
		got := defaultPromptValue(def)
		require.NotNil(t, got)
		require.Equal(t, "westus3", *got)
	})
}

// TestLocationParameterFilterImpl verifies allow-list filtering.
func TestLocationParameterFilterImpl(t *testing.T) {
	t.Parallel()

	require.True(t, locationParameterFilterImpl(nil, account.Location{Name: "westus2"}))
	require.True(t, locationParameterFilterImpl(
		[]string{"eastus", "westus2"}, account.Location{Name: "westus2"}))
	require.False(t, locationParameterFilterImpl(
		[]string{"eastus"}, account.Location{Name: "westus2"}))
	require.False(t, locationParameterFilterImpl(
		[]string{}, account.Location{Name: "westus2"}))
}

// TestGenerateDeploymentObjectUnsupportedScope verifies the error path.
func TestGenerateDeploymentObjectUnsupportedScope(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	prepareBicepMocks(mockContext)
	p := createBicepProvider(t, mockContext)

	tpl := azure.ArmTemplate{Schema: "https://example.com/unknown.json#"}
	plan := &compileBicepResult{Template: tpl}
	_, err := p.generateDeploymentObject(plan)
	require.Error(t, err)
}

// TestGenerateDeploymentObjectResourceGroup covers the RG path.
func TestGenerateDeploymentObjectResourceGroup(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	prepareBicepMocks(mockContext)
	p := createBicepProvider(t, mockContext)
	p.env.DotenvSet(environment.ResourceGroupEnvVarName, "rg-alpha")

	tpl := azure.ArmTemplate{
		Schema: "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
	}
	plan := &compileBicepResult{Template: tpl}
	dep, err := p.generateDeploymentObject(plan)
	require.NoError(t, err)
	require.NotNil(t, dep)
	require.Contains(t, dep.Name(), "test-env")
}

// TestGenerateDeploymentObjectWithLayer verifies the layer suffix is appended
// to the deployment base name.
func TestGenerateDeploymentObjectWithLayer(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	prepareBicepMocks(mockContext)
	p := createBicepProvider(t, mockContext)
	p.layer = "api"

	tpl := azure.ArmTemplate{
		Schema: "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
	}
	plan := &compileBicepResult{Template: tpl}
	dep, err := p.generateDeploymentObject(plan)
	require.NoError(t, err)
	require.NotNil(t, dep)
	require.Contains(t, dep.Name(), "test-env-api")
}

// helper for TestDefaultPromptValue azd metadata sub-test.
func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// TestCognitiveAccountsByKind verifies grouping by kind and the FormRecognizer rename.
func TestCognitiveAccountsByKind(t *testing.T) {
	t.Parallel()

	input := map[string][]armcognitiveservices.Account{
		"rg1": {
			{Kind: new("OpenAI")},
			{Kind: new("FormRecognizer")},
		},
		"rg2": {
			{Kind: new("OpenAI")},
		},
	}

	got := cognitiveAccountsByKind(input)

	require.Contains(t, got, "OpenAI")
	require.Len(t, got["OpenAI"], 2)
	require.Contains(t, got, "Document Intelligence")
	require.Len(t, got["Document Intelligence"], 1)
	require.NotContains(t, got, "FormRecognizer")
}

// TestAutoGenerate covers missing-config error and a successful generation path.
func TestAutoGenerate(t *testing.T) {
	t.Parallel()

	t.Run("MissingConfigReturnsError", func(t *testing.T) {
		t.Parallel()
		_, err := autoGenerate("param", azure.AzdMetadata{})
		require.Error(t, err)
	})

	t.Run("GeneratesValue", func(t *testing.T) {
		t.Parallel()
		v, err := autoGenerate("param", azure.AzdMetadata{
			AutoGenerateConfig: &azure.AutoGenInput{Length: 16},
		})
		require.NoError(t, err)
		require.Len(t, v, 16)
	})
}

// TestUsageNameDetailsFromString covers all parsing branches.
func TestUsageNameDetailsFromString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		wantErr  bool
		wantName string
		wantCap  float64
	}{
		{name: "Empty", input: "   ", wantErr: true},
		{name: "SinglePart", input: "OpenAI.S0.AccountCount", wantName: "OpenAI.S0.AccountCount", wantCap: 1},
		{name: "TwoParts", input: "OpenAI.Tokens , 10", wantName: "OpenAI.Tokens", wantCap: 10},
		{name: "TooManyParts", input: "a, 1, 2", wantErr: true},
		{name: "InvalidCapacity", input: "x, abc", wantErr: true},
		{name: "ZeroCapacity", input: "x, 0", wantErr: true},
		{name: "NegativeCapacity", input: "x, -5", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := usageNameDetailsFromString(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantName, got.UsageName)
			require.Equal(t, tc.wantCap, got.Capacity)
		})
	}
}

// TestArmTemplateResourcesUnmarshalJSON exercises both the array and
// symbolic-name (map) forms of the "resources" member and the error path.
func TestArmTemplateResourcesUnmarshalJSON(t *testing.T) {
	t.Parallel()

	t.Run("ArrayForm", func(t *testing.T) {
		t.Parallel()
		var r armTemplateResources
		err := json.Unmarshal([]byte(`[{"type":"Microsoft.Storage/storageAccounts","name":"s1"}]`), &r)
		require.NoError(t, err)
		require.Len(t, r, 1)
		require.Equal(t, "Microsoft.Storage/storageAccounts", r[0].Type)
	})

	t.Run("MapForm", func(t *testing.T) {
		t.Parallel()
		var r armTemplateResources
		err := json.Unmarshal([]byte(`{"sym":{"type":"Microsoft.Storage/storageAccounts","name":"s1"}}`), &r)
		require.NoError(t, err)
		require.Len(t, r, 1)
	})

	t.Run("Invalid", func(t *testing.T) {
		t.Parallel()
		var r armTemplateResources
		err := json.Unmarshal([]byte(`"a string, not an array or object"`), &r)
		require.Error(t, err)
	})
}

// TestArmParameterFileValue covers the type-coercion matrix for the helper.
func TestArmParameterFileValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		paramType provisioning.ParameterType
		value     any
		defValue  any
		want      any
	}{
		{name: "NilPassthrough", paramType: provisioning.ParameterTypeString, value: nil, want: nil},
		{name: "NonStringPassthrough", paramType: provisioning.ParameterTypeNumber, value: 42, want: 42},
		{name: "BoolFromString", paramType: provisioning.ParameterTypeBoolean, value: "true", want: true},
		{name: "BoolFromStringBad", paramType: provisioning.ParameterTypeBoolean, value: "not-bool", want: nil},
		{name: "NumberFromString", paramType: provisioning.ParameterTypeNumber, value: "123", want: int64(123)},
		{name: "NumberFromStringBad", paramType: provisioning.ParameterTypeNumber, value: "abc", want: nil},
		{name: "StringNonEmpty", paramType: provisioning.ParameterTypeString, value: "hello", want: "hello"},
		{name: "StringEmptyNoDefault", paramType: provisioning.ParameterTypeString, value: "", want: nil},
		{
			name:      "StringEmptyWithMatchingDefault",
			paramType: provisioning.ParameterTypeString,
			value:     "", defValue: "",
			want: nil,
		},
		{
			name:      "DefaultCase",
			paramType: provisioning.ParameterTypeArray,
			value:     "x",
			want:      "x",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := armParameterFileValue(tc.paramType, tc.value, tc.defValue)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestIsValueAssignableToParameterTypeAdditional covers extra branches of
// isValueAssignableToParameterType not covered by the existing test.
func TestIsValueAssignableToParameterTypeAdditional(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		paramType provisioning.ParameterType
		value     any
		want      bool
	}{
		{name: "ArrayOk", paramType: provisioning.ParameterTypeArray, value: []any{1, 2}, want: true},
		{name: "ArrayNo", paramType: provisioning.ParameterTypeArray, value: "not-array", want: false},
		{name: "BoolOk", paramType: provisioning.ParameterTypeBoolean, value: true, want: true},
		{name: "BoolNo", paramType: provisioning.ParameterTypeBoolean, value: "true", want: false},
		{name: "NumberIntOk", paramType: provisioning.ParameterTypeNumber, value: 5, want: true},
		{name: "NumberUintOk", paramType: provisioning.ParameterTypeNumber, value: uint(5), want: true},
		{name: "NumberFloatOk", paramType: provisioning.ParameterTypeNumber, value: 5.0, want: true},
		{name: "NumberFloatFrac", paramType: provisioning.ParameterTypeNumber, value: 5.5, want: false},
		{name: "NumberJSONOk", paramType: provisioning.ParameterTypeNumber, value: json.Number("7"), want: true},
		{name: "NumberBad", paramType: provisioning.ParameterTypeNumber, value: "5", want: false},
		{name: "ObjectOk", paramType: provisioning.ParameterTypeObject, value: map[string]any{"a": 1}, want: true},
		{name: "ObjectNo", paramType: provisioning.ParameterTypeObject, value: []any{}, want: false},
		{name: "StringOk", paramType: provisioning.ParameterTypeString, value: "hi", want: true},
		{name: "StringNo", paramType: provisioning.ParameterTypeString, value: 1, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isValueAssignableToParameterType(tc.paramType, tc.value)
			require.Equal(t, tc.want, got)
		})
	}

	t.Run("UnknownTypePanics", func(t *testing.T) {
		t.Parallel()
		require.Panics(t, func() {
			isValueAssignableToParameterType(provisioning.ParameterType("bogus"), 1)
		})
	})
}

// TestEvalParamEnvSubst exercises the principal/location/virtualEnv/env var branches.
func TestEvalParamEnvSubst(t *testing.T) {
	t.Parallel()

	env := environment.NewWithValues("test", map[string]string{
		"MY_VAR": "my-value",
	})

	t.Run("PrincipalIdAndType", func(t *testing.T) {
		t.Parallel()
		out, res, err := evalParamEnvSubst(
			"${AZURE_PRINCIPAL_ID}-${AZURE_PRINCIPAL_TYPE}",
			"pid-123", "User", "param", env, nil)
		require.NoError(t, err)
		require.Equal(t, "pid-123-User", out)
		require.False(t, res.hasUnsetEnvVar)
	})

	t.Run("LocationIsTracked", func(t *testing.T) {
		t.Parallel()
		_, res, err := evalParamEnvSubst(
			"${AZURE_LOCATION}", "", "", "locParam", env, nil)
		require.NoError(t, err)
		require.Contains(t, res.parametersMappedToAzureLocation, "locParam")
	})

	t.Run("VirtualEnvOverride", func(t *testing.T) {
		t.Parallel()
		out, res, err := evalParamEnvSubst(
			"${FOO}", "", "", "p", env, map[string]string{"FOO": "bar"})
		require.NoError(t, err)
		require.Equal(t, "bar", out)
		require.True(t, res.hasVirtualEnvVar)
	})

	t.Run("EnvVarLookup", func(t *testing.T) {
		t.Parallel()
		out, res, err := evalParamEnvSubst(
			"${MY_VAR}", "", "", "p", env, nil)
		require.NoError(t, err)
		require.Equal(t, "my-value", out)
		require.False(t, res.hasUnsetEnvVar)
	})

	t.Run("UnsetEnvVar", func(t *testing.T) {
		t.Parallel()
		_, res, err := evalParamEnvSubst(
			"${NOT_SET}", "", "", "p", env, nil)
		require.NoError(t, err)
		require.True(t, res.hasUnsetEnvVar)
	})
}

// TestResolveResourceGroupLocation covers the short-circuit paths (empty sub id, no RG env var).
func TestResolveResourceGroupLocation(t *testing.T) {
	t.Parallel()

	t.Run("EmptySubscriptionId", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)
		loc := p.resolveResourceGroupLocation(t.Context(), "")
		require.Equal(t, "", loc)
	})

	t.Run("NoResourceGroupEnvVar", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)
		// Don't set AZURE_RESOURCE_GROUP; expect empty result.
		loc := p.resolveResourceGroupLocation(t.Context(), "SUBSCRIPTION_ID")
		require.Equal(t, "", loc)
	})
}

// TestConvertIntAndJsonHelpers covers the small prompt converter helpers.
func TestConvertIntAndJsonHelpers(t *testing.T) {
	t.Parallel()

	t.Run("ConvertStringPassthrough", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, "abc", convertString("abc"))
	})

	t.Run("ConvertIntSuccess", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, 42, convertInt("42"))
	})

	t.Run("ConvertIntPanicOnBadInput", func(t *testing.T) {
		t.Parallel()
		require.Panics(t, func() { convertInt("not-a-number") })
	})

	t.Run("ConvertJsonArraySuccess", func(t *testing.T) {
		t.Parallel()
		got := convertJson[[]string](`["a","b"]`)
		require.Equal(t, []string{"a", "b"}, got)
	})

	t.Run("ConvertJsonObjectSuccess", func(t *testing.T) {
		t.Parallel()
		got := convertJson[map[string]any](`{"k":1}`)
		require.Equal(t, float64(1), got["k"])
	})

	t.Run("ConvertJsonPanicOnBadInput", func(t *testing.T) {
		t.Parallel()
		require.Panics(t, func() { convertJson[map[string]any](`not-json`) })
	})
}

// TestNewLocalArmPreflightAndAddCheck covers the constructor and AddCheck append path.
func TestNewLocalArmPreflightAndAddCheck(t *testing.T) {
	t.Parallel()

	pf := newLocalArmPreflight("infra/main.bicep", nil, nil, "westus2")
	require.NotNil(t, pf)
	require.Equal(t, "infra/main.bicep", pf.modulePath)
	require.Equal(t, "westus2", pf.envLocation)
	require.Nil(t, pf.target)
	require.Empty(t, pf.checks)

	// AddCheck appends; verify count grows.
	noopFn := func(ctx context.Context, valCtx *validationContext) ([]PreflightCheckResult, error) {
		return nil, nil
	}
	pf.AddCheck(PreflightCheck{RuleID: "rule1", Fn: noopFn})
	pf.AddCheck(PreflightCheck{RuleID: "rule2", Fn: noopFn})
	require.Len(t, pf.checks, 2)
}

// TestLatestDeploymentResult covers the success path through the deployment manager
// by reusing the package's mockedScope which returns 3 tagged deployments.
func TestLatestDeploymentResult(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	prepareBicepMocks(mockContext)
	p := createBicepProvider(t, mockContext)

	scope := &mockedScope{
		baseDate: "1989-10-31",
		envTag:   p.env.Name(),
	}

	dep, err := p.latestDeploymentResult(t.Context(), scope)
	require.NoError(t, err)
	require.NotNil(t, dep)
}

// TestLatestDeploymentResultError verifies error propagation from
// scope.ListDeployments.
func TestLatestDeploymentResultError(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	prepareBicepMocks(mockContext)
	p := createBicepProvider(t, mockContext)

	// unsupportedScope returns error from ListDeployments.
	_, err := p.latestDeploymentResult(t.Context(), unsupportedScope{})
	require.Error(t, err)
}

// TestDeploymentStateErrors exercises the error branches of deploymentState
// without requiring any HTTP mocks.
func TestDeploymentStateErrors(t *testing.T) {
	t.Parallel()

	t.Run("listDeploymentsError", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)

		_, err := p.deploymentState(
			t.Context(),
			&compileBicepResult{},
			unsupportedScope{},
			"hash",
		)
		require.Error(t, err)
	})
}

// TestValidateErrors exercises the error/skip paths of localArmPreflight.validate
// without depending on a real Bicep snapshot.
func TestValidateErrors(t *testing.T) {
	t.Parallel()

	t.Run("parseTemplateError", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		p := createBicepProvider(t, mockContext)

		pre := newLocalArmPreflight("main.bicep", p.bicepCli, nil, "eastus2")
		// Pass invalid JSON to trigger parseTemplate error.
		_, err := pre.validate(
			t.Context(),
			mockContext.Console,
			azure.RawArmTemplate("not-json"),
			azure.ArmParameters{},
		)
		require.Error(t, err)
	})

	t.Run("snapshotUnavailableSkips", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		// Mock bicep snapshot to fail; validate should treat it as a skip.
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return len(args.Args) > 0 && args.Args[0] == "snapshot"
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.RunResult{ExitCode: 1, Stderr: "snapshot not supported"},
					errors.New("snapshot not supported")
			})
		p := createBicepProvider(t, mockContext)

		// Minimal valid ARM template so parseTemplate succeeds.
		raw := azure.RawArmTemplate(
			`{"$schema":"x","contentVersion":"1.0",` +
				`"resources":[{"type":"Microsoft.Resources/deployments",` +
				`"name":"x","apiVersion":"2020-10-01"}]}`)

		pre := newLocalArmPreflight("nonexistent.bicepparam", p.bicepCli, nil, "")
		results, err := pre.validate(
			t.Context(),
			mockContext.Console,
			raw,
			azure.ArmParameters{},
		)
		require.NoError(t, err)
		require.Nil(t, results)
	})

	t.Run("bicepModulePathCreatesTempParam", func(t *testing.T) {
		t.Parallel()
		mockContext := mocks.NewMockContext(t.Context())
		prepareBicepMocks(mockContext)
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return len(args.Args) > 0 && args.Args[0] == "snapshot"
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.RunResult{ExitCode: 1, Stderr: "snapshot not supported"},
					errors.New("snapshot not supported")
			})
		p := createBicepProvider(t, mockContext)

		raw := azure.RawArmTemplate(
			`{"$schema":"x","contentVersion":"1.0",` +
				`"resources":[{"type":"Microsoft.Resources/deployments",` +
				`"name":"x","apiVersion":"2020-10-01"}]}`)

		// Use a .bicep module path (in a writable tempdir) so validate must
		// create and clean up a temp .bicepparam file.
		moduleDir := t.TempDir()
		modulePath := moduleDir + "/main.bicep"

		pre := newLocalArmPreflight(modulePath, p.bicepCli, nil, "eastus2")
		results, err := pre.validate(
			t.Context(),
			mockContext.Console,
			raw,
			azure.ArmParameters{
				"foo": {Value: "bar"},
			},
		)
		require.NoError(t, err)
		require.Nil(t, results)
	})
}

// TestPurgeHelpersEmptyInputs exercises the fast-path (empty input) and
// skip-path branches of the purge/getToPurge helpers. None of these paths
// require HTTP mocks.
func TestPurgeHelpersEmptyInputs(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	prepareBicepMocks(mockContext)
	p := createBicepProvider(t, mockContext)
	ctx := t.Context()
	empty := map[string][]*azapi.Resource{}

	t.Run("getKeyVaults", func(t *testing.T) {
		got, err := p.getKeyVaults(ctx, empty)
		require.NoError(t, err)
		require.Empty(t, got)
	})
	t.Run("getKeyVaultsToPurge", func(t *testing.T) {
		got, err := p.getKeyVaultsToPurge(ctx, empty)
		require.NoError(t, err)
		require.Empty(t, got)
	})
	t.Run("getManagedHSMs", func(t *testing.T) {
		got, err := p.getManagedHSMs(ctx, empty)
		require.NoError(t, err)
		require.Empty(t, got)
	})
	t.Run("getManagedHSMsToPurge", func(t *testing.T) {
		got, err := p.getManagedHSMsToPurge(ctx, empty)
		require.NoError(t, err)
		require.Empty(t, got)
	})
	t.Run("getCognitiveAccountsToPurge", func(t *testing.T) {
		got, err := p.getCognitiveAccountsToPurge(ctx, empty)
		require.NoError(t, err)
		require.Empty(t, got)
	})
	t.Run("getAppConfigsToPurge", func(t *testing.T) {
		got, err := p.getAppConfigsToPurge(ctx, empty)
		require.NoError(t, err)
		require.Empty(t, got)
	})
	t.Run("getApiManagementsToPurge", func(t *testing.T) {
		got, err := p.getApiManagementsToPurge(ctx, empty)
		require.NoError(t, err)
		require.Empty(t, got)
	})
	t.Run("purgeKeyVaultsEmpty", func(t *testing.T) {
		require.NoError(t, p.purgeKeyVaults(ctx, nil, true))
	})
	t.Run("purgeManagedHSMsEmpty", func(t *testing.T) {
		require.NoError(t, p.purgeManagedHSMs(ctx, nil, true))
	})
	t.Run("purgeCognitiveAccountsEmpty", func(t *testing.T) {
		require.NoError(t, p.purgeCognitiveAccounts(ctx, nil, true))
	})
	t.Run("purgeAppConfigsEmpty", func(t *testing.T) {
		require.NoError(t, p.purgeAppConfigs(ctx, nil, true))
	})
	t.Run("purgeAPIManagementEmpty", func(t *testing.T) {
		require.NoError(t, p.purgeAPIManagement(ctx, nil, true))
	})
	t.Run("forceDeleteLogAnalyticsWorkspacesEmpty", func(t *testing.T) {
		require.NoError(t, p.forceDeleteLogAnalyticsWorkspaces(ctx, nil))
	})
	t.Run("purgeItemsEmpty", func(t *testing.T) {
		require.NoError(t, p.purgeItems(ctx, nil, provisioning.NewDestroyOptions(false, false)))
	})
	t.Run("runPurgeAsStepSkipped", func(t *testing.T) {
		called := false
		err := p.runPurgeAsStep(ctx, "resource", "name", func() error {
			called = true
			return nil
		}, true /* skipped */)
		require.NoError(t, err)
		require.False(t, called, "step fn must not be called when skipped")
	})
	t.Run("runPurgeAsStepExecutes", func(t *testing.T) {
		called := false
		err := p.runPurgeAsStep(ctx, "resource", "name", func() error {
			called = true
			return nil
		}, false)
		require.NoError(t, err)
		require.True(t, called)
	})
}

// TestPurgeCognitiveAccountsValidationErrors verifies early-return errors for
// accounts missing required fields.
func TestPurgeCognitiveAccountsValidationErrors(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	prepareBicepMocks(mockContext)
	p := createBicepProvider(t, mockContext)
	ctx := t.Context()

	t.Run("missingName", func(t *testing.T) {
		err := p.purgeCognitiveAccounts(ctx, []cognitiveAccount{
			{account: armcognitiveservices.Account{}, resourceGroup: "rg"},
		}, false)
		require.Error(t, err)
	})
	t.Run("missingId", func(t *testing.T) {
		err := p.purgeCognitiveAccounts(ctx, []cognitiveAccount{
			{account: armcognitiveservices.Account{Name: new("n")}, resourceGroup: "rg"},
		}, false)
		require.Error(t, err)
	})
	t.Run("missingLocation", func(t *testing.T) {
		err := p.purgeCognitiveAccounts(ctx, []cognitiveAccount{
			{account: armcognitiveservices.Account{
				Name: new("n"),
				ID:   new("/subscriptions/x/resourceGroups/rg/providers/p/accounts/n"),
			}, resourceGroup: "rg"},
		}, false)
		require.Error(t, err)
	})
}
