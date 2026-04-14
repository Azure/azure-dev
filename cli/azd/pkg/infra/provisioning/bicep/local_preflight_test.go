// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/stretchr/testify/require"
)

func TestParseTemplate_ValidTemplate(t *testing.T) {
	template := armTemplate{
		Schema:         "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
		ContentVersion: "1.0.0.0",
		Resources: armTemplateResources{
			{Type: "Microsoft.Resources/resourceGroups", APIVersion: "2021-04-01", Name: "rg-test"},
		},
	}
	raw, err := json.Marshal(template)
	require.NoError(t, err)

	preflight := &localArmPreflight{}
	parsed, err := preflight.parseTemplate(azure.RawArmTemplate(raw))

	require.NoError(t, err)
	require.NotNil(t, parsed)
	require.Len(t, parsed.Resources, 1)
	require.Equal(t, "Microsoft.Resources/resourceGroups", parsed.Resources[0].Type)
}

func TestParseTemplate_MissingSchema(t *testing.T) {
	raw := []byte(`{"contentVersion": "1.0.0.0", "resources": [{"type": "Microsoft.Resources/resourceGroups"}]}`)

	preflight := &localArmPreflight{}
	_, err := preflight.parseTemplate(azure.RawArmTemplate(raw))

	require.Error(t, err)
	require.Contains(t, err.Error(), "missing required '$schema'")
}

func TestParseTemplate_MissingContentVersion(t *testing.T) {
	schema := "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#"
	raw := fmt.Appendf(nil,
		`{"$schema": "%s", "resources": [{"type": "Microsoft.Resources/resourceGroups"}]}`,
		schema,
	)

	preflight := &localArmPreflight{}
	_, err := preflight.parseTemplate(azure.RawArmTemplate(raw))

	require.Error(t, err)
	require.Contains(t, err.Error(), "missing required 'contentVersion'")
}

func TestParseTemplate_NoResources(t *testing.T) {
	schema := "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#"
	raw := fmt.Appendf(nil,
		`{"$schema": "%s", "contentVersion": "1.0.0.0", "resources": []}`,
		schema,
	)

	preflight := &localArmPreflight{}
	_, err := preflight.parseTemplate(azure.RawArmTemplate(raw))

	require.Error(t, err)
	require.Contains(t, err.Error(), "no resources")
}

func TestParseTemplate_InvalidJSON(t *testing.T) {
	preflight := &localArmPreflight{}
	_, err := preflight.parseTemplate(azure.RawArmTemplate([]byte(`{}`)))

	require.Error(t, err)
	require.Contains(t, err.Error(), "missing required")
}

func TestRegisteredChecks_RunInOrder(t *testing.T) {
	valCtx := &validationContext{
		Props: resourcesProperties{},
	}

	var checks []PreflightCheck

	// Add a warning check
	checks = append(checks, PreflightCheck{
		RuleID: "warning_rule",
		Fn: func(
			ctx context.Context,
			valCtx *validationContext,
		) ([]PreflightCheckResult, error) {
			return []PreflightCheckResult{{
				Severity:     PreflightCheckWarning,
				DiagnosticID: "warning_diag",
				Message:      "this is a warning",
			}}, nil
		},
	})

	// Add a check that returns nil (no finding)
	checks = append(checks, PreflightCheck{
		RuleID: "nil_rule",
		Fn: func(
			ctx context.Context,
			valCtx *validationContext,
		) ([]PreflightCheckResult, error) {
			return nil, nil
		},
	})

	// Add an error check
	checks = append(checks, PreflightCheck{
		RuleID: "error_rule",
		Fn: func(
			ctx context.Context,
			valCtx *validationContext,
		) ([]PreflightCheckResult, error) {
			return []PreflightCheckResult{{
				Severity:     PreflightCheckError,
				DiagnosticID: "error_diag",
				Message:      "this is an error",
			}}, nil
		},
	})

	var results []PreflightCheckResult
	for _, check := range checks {
		checkResults, err := check.Fn(t.Context(), valCtx)
		require.NoError(t, err)
		results = append(results, checkResults...)
	}

	require.Len(t, results, 2)
	require.Equal(t, PreflightCheckWarning, results[0].Severity)
	require.Equal(t, "warning_diag", results[0].DiagnosticID)
	require.Equal(t, "this is a warning", results[0].Message)
	require.Equal(t, PreflightCheckError, results[1].Severity)
	require.Equal(t, "error_diag", results[1].DiagnosticID)
	require.Equal(t, "this is an error", results[1].Message)
}

func TestPreflightCheck_DiagnosticIDPropagation(t *testing.T) {
	valCtx := &validationContext{
		Props: resourcesProperties{},
	}

	check := PreflightCheck{
		RuleID: "test_rule",
		Fn: func(ctx context.Context, valCtx *validationContext) ([]PreflightCheckResult, error) {
			return []PreflightCheckResult{{
				Severity:     PreflightCheckWarning,
				DiagnosticID: "test_diagnostic_id",
				Message:      "test message",
			}}, nil
		},
	}

	results, err := check.Fn(t.Context(), valCtx)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "test_diagnostic_id", results[0].DiagnosticID)
	require.Equal(t, "test_rule", check.RuleID)
}

func TestPreflightCheck_AddCheckStoresRuleID(t *testing.T) {
	preflight := &localArmPreflight{}
	preflight.AddCheck(PreflightCheck{
		RuleID: "failing_rule",
		Fn: func(ctx context.Context, valCtx *validationContext) ([]PreflightCheckResult, error) {
			return nil, fmt.Errorf("something went wrong")
		},
	})

	require.Len(t, preflight.checks, 1)
	require.Equal(t, "failing_rule", preflight.checks[0].RuleID)
}

func TestArmField_TypedValue(t *testing.T) {
	input := `{"sku": {"name": "Standard_LRS", "tier": "Standard"}}`
	var res armTemplateResource
	require.NoError(t, json.Unmarshal([]byte(input), &res))

	sku, ok := res.SKU.Value()
	require.True(t, ok)
	require.Equal(t, "Standard_LRS", sku.Name)
	require.Equal(t, "Standard", sku.Tier)
	require.True(t, res.SKU.HasValue())
}

func TestArmField_ExpressionString(t *testing.T) {
	// Bicep conditional expressions compile to ARM expression strings.
	input := `{"identity":` +
		`"[if(equals(parameters('id'), ''),` +
		` createObject('type', 'SystemAssigned'),` +
		` createObject('type', 'UserAssigned'))]"}`
	var res armTemplateResource
	require.NoError(t, json.Unmarshal([]byte(input), &res))

	// Typed parse should fail gracefully — it's an expression string, not an object.
	_, ok := res.Identity.Value()
	require.False(t, ok)

	// Raw access should return the expression string.
	require.True(t, res.Identity.HasValue())
	require.Contains(t, string(res.Identity.Raw()), "[if(equals(")
}

func TestArmField_Null(t *testing.T) {
	input := `{"sku": null}`
	var res armTemplateResource
	require.NoError(t, json.Unmarshal([]byte(input), &res))

	require.False(t, res.SKU.HasValue())
	_, ok := res.SKU.Value()
	require.False(t, ok)
}

func TestArmField_Absent(t *testing.T) {
	input := `{"type": "Microsoft.Web/sites"}`
	var res armTemplateResource
	require.NoError(t, json.Unmarshal([]byte(input), &res))

	require.False(t, res.SKU.HasValue())
	require.False(t, res.Identity.HasValue())
	require.False(t, res.Tags.HasValue())
	require.Nil(t, res.SKU.Raw())
}

func TestArmField_Tags(t *testing.T) {
	input := `{"tags": {"env": "dev", "team": "platform"}}`
	var res armTemplateResource
	require.NoError(t, json.Unmarshal([]byte(input), &res))

	tags, ok := res.Tags.Value()
	require.True(t, ok)
	require.Equal(t, "dev", tags["env"])
	require.Equal(t, "platform", tags["team"])
}

func TestArmField_TagsExpression(t *testing.T) {
	input := `{"tags": "[variables('tags')]"}`
	var res armTemplateResource
	require.NoError(t, json.Unmarshal([]byte(input), &res))

	_, ok := res.Tags.Value()
	require.False(t, ok)
	require.True(t, res.Tags.HasValue())
	require.Contains(t, string(res.Tags.Raw()), "variables('tags')")
}

func TestArmField_RoundTrip(t *testing.T) {
	input := `{"type":"Microsoft.Web/sites","sku":{"name":"S1"},"identity":{"type":"SystemAssigned"}}`
	var res armTemplateResource
	require.NoError(t, json.Unmarshal([]byte(input), &res))

	data, err := json.Marshal(res)
	require.NoError(t, err)

	var res2 armTemplateResource
	require.NoError(t, json.Unmarshal(data, &res2))

	sku, ok := res2.SKU.Value()
	require.True(t, ok)
	require.Equal(t, "S1", sku.Name)

	id, ok := res2.Identity.Value()
	require.True(t, ok)
	require.Equal(t, "SystemAssigned", id.Type)
}

func TestArmField_OmitZero(t *testing.T) {
	// Absent armField fields should be omitted from marshaled JSON via omitzero.
	res := armTemplateResource{Type: "Microsoft.Web/sites", APIVersion: "2020-06-01", Name: "test"}
	data, err := json.Marshal(res)
	require.NoError(t, err)

	raw := string(data)
	require.NotContains(t, raw, "sku")
	require.NotContains(t, raw, "tags")
	require.NotContains(t, raw, "identity")
	require.NotContains(t, raw, "zones")
	require.Contains(t, raw, `"type":"Microsoft.Web/sites"`)
}

func TestAnalyzeResources(t *testing.T) {
	tests := []struct {
		name               string
		resources          []armTemplateResource
		hasRoleAssignments bool
	}{
		{
			name:               "empty resources",
			resources:          nil,
			hasRoleAssignments: false,
		},
		{
			name: "no role assignments",
			resources: []armTemplateResource{
				{Type: "Microsoft.Storage/storageAccounts"},
				{Type: "Microsoft.Web/sites"},
			},
			hasRoleAssignments: false,
		},
		{
			name: "has role assignments",
			resources: []armTemplateResource{
				{Type: "Microsoft.Storage/storageAccounts"},
				{Type: "Microsoft.Authorization/roleAssignments"},
			},
			hasRoleAssignments: true,
		},
		{
			name: "case insensitive match",
			resources: []armTemplateResource{
				{Type: "microsoft.authorization/roleassignments"},
			},
			hasRoleAssignments: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			props := analyzeResources(tt.resources)
			require.Equal(t, tt.hasRoleAssignments, props.HasRoleAssignments)
		})
	}
}

func TestAnalyzeArmTemplateResources(t *testing.T) {
	tests := []struct {
		name               string
		resources          armTemplateResources
		hasRoleAssignments bool
	}{
		{
			name:               "empty resources",
			resources:          nil,
			hasRoleAssignments: false,
		},
		{
			name: "no role assignments",
			resources: armTemplateResources{
				{Type: "Microsoft.Storage/storageAccounts"},
			},
			hasRoleAssignments: false,
		},
		{
			name: "direct role assignment",
			resources: armTemplateResources{
				{Type: "Microsoft.Authorization/roleAssignments"},
			},
			hasRoleAssignments: true,
		},
		{
			name: "role assignment in child resources",
			resources: armTemplateResources{
				{
					Type: "Microsoft.ManagedIdentity/userAssignedIdentities",
					Resources: armTemplateResources{
						{Type: "Microsoft.Authorization/roleAssignments"},
					},
				},
			},
			hasRoleAssignments: true,
		},
		{
			name: "role assignment in nested deployment",
			resources: armTemplateResources{
				{
					Type: "Microsoft.Resources/deployments",
					Properties: mustMarshal(t, map[string]any{
						"template": map[string]any{
							"resources": []map[string]any{
								{"type": "Microsoft.Authorization/roleAssignments"},
							},
						},
					}),
				},
			},
			hasRoleAssignments: true,
		},
		{
			name: "nested deployment with invalid properties json",
			resources: armTemplateResources{
				{
					Type:       "Microsoft.Resources/deployments",
					Properties: json.RawMessage([]byte("{invalid")),
				},
			},
			hasRoleAssignments: true, // conservative: can't inspect → assume role assignments exist
		},
		{
			name: "nested deployment with template link only",
			resources: armTemplateResources{
				{
					Type: "Microsoft.Resources/deployments",
					Properties: mustMarshal(t, map[string]any{
						"templateLink": map[string]any{
							"uri": "https://example.invalid/template.json",
						},
					}),
				},
			},
			hasRoleAssignments: true, // conservative: no inline template → assume role assignments exist
		},
		{
			name: "deeply nested deployment",
			resources: armTemplateResources{
				{
					Type: "Microsoft.Resources/deployments",
					Properties: mustMarshal(t, map[string]any{
						"template": map[string]any{
							"resources": []map[string]any{
								{
									"type": "Microsoft.Resources/deployments",
									"properties": map[string]any{
										"template": map[string]any{
											"resources": []map[string]any{
												{"type": "Microsoft.Authorization/roleAssignments"},
											},
										},
									},
								},
							},
						},
					}),
				},
			},
			hasRoleAssignments: true,
		},
		{
			name: "nested deployment without role assignments",
			resources: armTemplateResources{
				{
					Type: "Microsoft.Resources/deployments",
					Properties: mustMarshal(t, map[string]any{
						"template": map[string]any{
							"resources": []map[string]any{
								{"type": "Microsoft.Storage/storageAccounts"},
							},
						},
					}),
				},
			},
			hasRoleAssignments: false,
		},
		{
			name: "case insensitive in nested deployment",
			resources: armTemplateResources{
				{
					Type: "microsoft.resources/deployments",
					Properties: mustMarshal(t, map[string]any{
						"template": map[string]any{
							"resources": []map[string]any{
								{"type": "microsoft.authorization/roleassignments"},
							},
						},
					}),
				},
			},
			hasRoleAssignments: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			props := analyzeArmTemplateResources(tt.resources)
			require.Equal(t, tt.hasRoleAssignments, props.HasRoleAssignments)
		})
	}
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}
