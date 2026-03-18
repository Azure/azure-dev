// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armpolicy"
	"github.com/stretchr/testify/require"
)

func TestExtractLocalAuthDenyPolicies_DenyLiteral(t *testing.T) {
	t.Parallel()

	def := &armpolicy.Definition{
		Properties: &armpolicy.DefinitionProperties{
			PolicyRule: map[string]any{
				"if": map[string]any{
					"allOf": []any{
						map[string]any{
							"field":  "type",
							"equals": "Microsoft.CognitiveServices/accounts",
						},
						map[string]any{
							"field":     "Microsoft.CognitiveServices/accounts/disableLocalAuth",
							"notEquals": true,
						},
					},
				},
				"then": map[string]any{
					"effect": "deny",
				},
			},
		},
	}

	results := extractLocalAuthDenyPolicies(def, "test-policy", nil)
	require.Len(t, results, 1)
	require.Equal(t, "Microsoft.CognitiveServices/accounts", results[0].ResourceType)
	require.Equal(t, "test-policy", results[0].PolicyName)
	require.Contains(t, results[0].FieldPath, "disableLocalAuth")
}

func TestExtractLocalAuthDenyPolicies_ParameterizedEffect_Deny(t *testing.T) {
	t.Parallel()

	def := &armpolicy.Definition{
		Properties: &armpolicy.DefinitionProperties{
			Parameters: map[string]*armpolicy.ParameterDefinitionsValue{
				"effect": {
					DefaultValue: "Audit",
				},
			},
			PolicyRule: map[string]any{
				"if": map[string]any{
					"allOf": []any{
						map[string]any{
							"field":  "type",
							"equals": "Microsoft.EventHub/namespaces",
						},
						map[string]any{
							"field":     "Microsoft.EventHub/namespaces/disableLocalAuth",
							"notEquals": true,
						},
					},
				},
				"then": map[string]any{
					"effect": "[parameters('effect')]",
				},
			},
		},
	}

	// With assignment params overriding to Deny.
	assignmentParams := map[string]any{"effect": "Deny"}
	results := extractLocalAuthDenyPolicies(def, "test-policy", assignmentParams)
	require.Len(t, results, 1)
	require.Equal(t, "Microsoft.EventHub/namespaces", results[0].ResourceType)
}

func TestExtractLocalAuthDenyPolicies_ParameterizedEffect_Audit(t *testing.T) {
	t.Parallel()

	def := &armpolicy.Definition{
		Properties: &armpolicy.DefinitionProperties{
			Parameters: map[string]*armpolicy.ParameterDefinitionsValue{
				"effect": {
					DefaultValue: "Audit",
				},
			},
			PolicyRule: map[string]any{
				"if": map[string]any{
					"allOf": []any{
						map[string]any{
							"field":  "type",
							"equals": "Microsoft.EventHub/namespaces",
						},
						map[string]any{
							"field":     "Microsoft.EventHub/namespaces/disableLocalAuth",
							"notEquals": true,
						},
					},
				},
				"then": map[string]any{
					"effect": "[parameters('effect')]",
				},
			},
		},
	}

	// No assignment params — falls back to default "Audit", which is not deny.
	results := extractLocalAuthDenyPolicies(def, "test-policy", nil)
	require.Empty(t, results)
}

func TestExtractLocalAuthDenyPolicies_NoLocalAuthField(t *testing.T) {
	t.Parallel()

	def := &armpolicy.Definition{
		Properties: &armpolicy.DefinitionProperties{
			PolicyRule: map[string]any{
				"if": map[string]any{
					"allOf": []any{
						map[string]any{
							"field":  "type",
							"equals": "Microsoft.Storage/storageAccounts",
						},
						map[string]any{
							"field":  "Microsoft.Storage/storageAccounts/supportsHttpsTrafficOnly",
							"equals": false,
						},
					},
				},
				"then": map[string]any{
					"effect": "deny",
				},
			},
		},
	}

	results := extractLocalAuthDenyPolicies(def, "test-policy", nil)
	require.Empty(t, results)
}

func TestExtractLocalAuthDenyPolicies_StorageAllowSharedKeyAccess(t *testing.T) {
	t.Parallel()

	def := &armpolicy.Definition{
		Properties: &armpolicy.DefinitionProperties{
			PolicyRule: map[string]any{
				"if": map[string]any{
					"allOf": []any{
						map[string]any{
							"field":  "type",
							"equals": "Microsoft.Storage/storageAccounts",
						},
						map[string]any{
							"field":     "Microsoft.Storage/storageAccounts/allowSharedKeyAccess",
							"notEquals": false,
						},
					},
				},
				"then": map[string]any{
					"effect": "deny",
				},
			},
		},
	}

	results := extractLocalAuthDenyPolicies(def, "test-policy", nil)
	require.Len(t, results, 1)
	require.Equal(t, "Microsoft.Storage/storageAccounts", results[0].ResourceType)
}

func TestExtractLocalAuthDenyPolicies_NestedAnyOf(t *testing.T) {
	t.Parallel()

	def := &armpolicy.Definition{
		Properties: &armpolicy.DefinitionProperties{
			PolicyRule: map[string]any{
				"if": map[string]any{
					"allOf": []any{
						map[string]any{
							"field":  "type",
							"equals": "Microsoft.ServiceBus/namespaces",
						},
						map[string]any{
							"anyOf": []any{
								map[string]any{
									"field":  "Microsoft.ServiceBus/namespaces/disableLocalAuth",
									"equals": false,
								},
								map[string]any{
									"field":  "Microsoft.ServiceBus/namespaces/disableLocalAuth",
									"exists": false,
								},
							},
						},
					},
				},
				"then": map[string]any{
					"effect": "Deny",
				},
			},
		},
	}

	results := extractLocalAuthDenyPolicies(def, "test-nested", nil)
	require.NotEmpty(t, results)
	require.Equal(t, "Microsoft.ServiceBus/namespaces", results[0].ResourceType)
}

func TestExtractLocalAuthDenyPolicies_MultipleResourceTypesInArray(t *testing.T) {
	t.Parallel()

	def := &armpolicy.Definition{
		Properties: &armpolicy.DefinitionProperties{
			PolicyRule: map[string]any{
				"if": map[string]any{
					"allOf": []any{
						map[string]any{
							"field": "type",
							"in": []any{
								"Microsoft.EventHub/namespaces",
								"Microsoft.ServiceBus/namespaces",
							},
						},
						map[string]any{
							"field":     "disableLocalAuth",
							"notEquals": true,
						},
					},
				},
				"then": map[string]any{
					"effect": "Deny",
				},
			},
		},
	}

	results := extractLocalAuthDenyPolicies(def, "multi-type-policy", nil)
	require.Len(t, results, 2)

	types := make(map[string]bool)
	for _, r := range results {
		types[r.ResourceType] = true
	}
	require.True(t, types["Microsoft.EventHub/namespaces"])
	require.True(t, types["Microsoft.ServiceBus/namespaces"])
}

func TestExtractLocalAuthDenyPolicies_NestedConditionInheritsResourceType(t *testing.T) {
	t.Parallel()

	// The resource type is declared at the outer allOf level, and the disableLocalAuth
	// condition is in a nested anyOf. The nested level should inherit the resource type.
	def := &armpolicy.Definition{
		Properties: &armpolicy.DefinitionProperties{
			PolicyRule: map[string]any{
				"if": map[string]any{
					"allOf": []any{
						map[string]any{
							"field":  "type",
							"equals": "Microsoft.Search/searchServices",
						},
						map[string]any{
							"anyOf": []any{
								map[string]any{
									"field":  "disableLocalAuth",
									"equals": false,
								},
							},
						},
					},
				},
				"then": map[string]any{
					"effect": "Deny",
				},
			},
		},
	}

	results := extractLocalAuthDenyPolicies(def, "nested-inherit", nil)
	require.Len(t, results, 1)
	require.Equal(t, "Microsoft.Search/searchServices", results[0].ResourceType)
}

func TestIsLocalAuthField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		field string
		want  bool
	}{
		{"Microsoft.CognitiveServices/accounts/disableLocalAuth", true},
		{"Microsoft.EventHub/namespaces/disableLocalAuth", true},
		{"Microsoft.Storage/storageAccounts/allowSharedKeyAccess", true},
		{"Microsoft.ServiceBus/namespaces/disableLocalAuth", true},
		{"disableLocalAuth", true},
		{"Microsoft.Storage/storageAccounts/supportsHttpsTrafficOnly", false},
		{"type", false},
		{"location", false},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, isLocalAuthField(tt.field))
		})
	}
}

func TestResourceTypeFromFieldPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		field string
		want  string
	}{
		{"Microsoft.CognitiveServices/accounts/disableLocalAuth", "Microsoft.CognitiveServices/accounts"},
		{"Microsoft.EventHub/namespaces/disableLocalAuth", "Microsoft.EventHub/namespaces"},
		{"disableLocalAuth", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, resourceTypeFromFieldPath(tt.field))
		})
	}
}

func TestExtractParameterReference(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want string
	}{
		{"[parameters('effect')]", "effect"},
		{"[parameters('myEffect')]", "myEffect"},
		{"deny", ""},
		{"[concat('a', 'b')]", ""},
		{"", ""},
		{"  [parameters('effect')]  ", "effect"},
		{"\t[parameters('effect')]\n", "effect"},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, extractParameterReference(tt.expr))
		})
	}
}

func TestResourceHasLocalAuthDisabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		resourceType string
		properties   string
		want         bool
	}{
		{
			"CognitiveServices disabled",
			"Microsoft.CognitiveServices/accounts",
			`{"disableLocalAuth": true}`,
			true,
		},
		{
			"CognitiveServices enabled",
			"Microsoft.CognitiveServices/accounts",
			`{"disableLocalAuth": false}`,
			false,
		},
		{
			"CognitiveServices missing property",
			"Microsoft.CognitiveServices/accounts",
			`{}`,
			false,
		},
		{
			"Storage allowSharedKeyAccess false",
			"Microsoft.Storage/storageAccounts",
			`{"allowSharedKeyAccess": false}`,
			true,
		},
		{
			"Storage allowSharedKeyAccess true",
			"Microsoft.Storage/storageAccounts",
			`{"allowSharedKeyAccess": true}`,
			false,
		},
		{
			"Empty properties",
			"Microsoft.EventHub/namespaces",
			``,
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, ResourceHasLocalAuthDisabled(
				tt.resourceType, []byte(tt.properties),
			))
		})
	}
}

func TestIsDenyEffect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		ruleMap          map[string]any
		definitionParams map[string]*armpolicy.ParameterDefinitionsValue
		assignmentParams map[string]any
		want             bool
	}{
		{
			"literal deny",
			map[string]any{"then": map[string]any{"effect": "deny"}},
			nil, nil, true,
		},
		{
			"literal Deny (capitalized)",
			map[string]any{"then": map[string]any{"effect": "Deny"}},
			nil, nil, true,
		},
		{
			"literal audit",
			map[string]any{"then": map[string]any{"effect": "audit"}},
			nil, nil, false,
		},
		{
			"parameterized deny via assignment",
			map[string]any{"then": map[string]any{"effect": "[parameters('effect')]"}},
			nil,
			map[string]any{"effect": "Deny"},
			true,
		},
		{
			"parameterized deny via default",
			map[string]any{"then": map[string]any{"effect": "[parameters('effect')]"}},
			map[string]*armpolicy.ParameterDefinitionsValue{
				"effect": {DefaultValue: "Deny"},
			},
			nil, true,
		},
		{
			"parameterized audit via default",
			map[string]any{"then": map[string]any{"effect": "[parameters('effect')]"}},
			map[string]*armpolicy.ParameterDefinitionsValue{
				"effect": {DefaultValue: "Audit"},
			},
			nil, false,
		},
		{
			"no then block",
			map[string]any{},
			nil, nil, false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, isDenyEffect(tt.ruleMap, tt.definitionParams, tt.assignmentParams))
		})
	}
}
