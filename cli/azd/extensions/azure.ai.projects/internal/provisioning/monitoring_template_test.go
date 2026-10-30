// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"encoding/json"
	"testing"

	"azure.ai.projects/internal/synthesis"

	"github.com/stretchr/testify/require"
)

func TestMonitoringTemplateAllowsEditableResources(t *testing.T) {
	t.Parallel()
	template := compiledMonitoringTemplate(t)
	require.NoError(t, validateProjectTemplate(template, "main.bicep"))
	workspace := findMonitoringTemplateObject(template, func(object map[string]any) bool {
		return object["type"] == "Microsoft.OperationalInsights/workspaces"
	})
	require.NotNil(t, workspace)
	workspace["properties"].(map[string]any)["retentionInDays"] = float64(90)
	require.NoError(t, validateProjectTemplate(template, "custom-main.bicep"))
}

func TestMonitoringTemplateRejectsUnsafeConnectionEdits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(map[string]any, map[string]any)
	}{
		{name: "unguarded", mutate: func(connection, _ map[string]any) { delete(connection, "@options") }},
		{name: "scope override", mutate: func(connection, _ map[string]any) { connection["scope"] = "/subscriptions/other" }},
		{name: "loop", mutate: func(connection, _ map[string]any) { connection["copy"] = map[string]any{} }},
		{name: "condition override", mutate: func(connection, _ map[string]any) { connection["condition"] = true }},
		{name: "different name", mutate: func(connection, _ map[string]any) { connection["name"] = "user-connection" }},
		{name: "different credential", mutate: func(connection, _ map[string]any) {
			connection["properties"].(map[string]any)["credentials"] = map[string]any{"key": "[parameters('userKey')]"}
		}},
		{name: "shared connection", mutate: func(connection, _ map[string]any) {
			connection["properties"].(map[string]any)["isSharedToAll"] = true
		}},
		{name: "different target", mutate: func(connection, _ map[string]any) {
			connection["properties"].(map[string]any)["target"] = "[parameters('otherComponent')]"
		}},
		{name: "different component reference", mutate: func(_, template map[string]any) {
			connectionTemplate := findMonitoringTemplateObject(template, func(object map[string]any) bool {
				resources, _ := object["resources"].(map[string]any)
				return resources["connection"] != nil && resources["component"] != nil
			})
			require.NotNil(t, connectionTemplate)
			component := connectionTemplate["resources"].(map[string]any)["component"].(map[string]any)
			component["name"] = "other-component"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template := compiledMonitoringTemplate(t)
			connection := findMonitoringTemplateObject(template, func(object map[string]any) bool {
				properties, _ := object["properties"].(map[string]any)
				return object["type"] == "Microsoft.CognitiveServices/accounts/projects/connections" &&
					properties["category"] == "AppInsights"
			})
			require.NotNil(t, connection)
			tt.mutate(connection, template)
			require.Error(t, validateProjectTemplate(template, "modified-main.bicep"))
		})
	}
}

func compiledMonitoringTemplate(t *testing.T) map[string]any {
	t.Helper()
	data, err := synthesis.ARMTemplate()
	require.NoError(t, err)
	var template map[string]any
	require.NoError(t, json.Unmarshal(data, &template))
	return template
}

func findMonitoringTemplateObject(value any, matches func(map[string]any) bool) map[string]any {
	switch value := value.(type) {
	case map[string]any:
		if matches(value) {
			return value
		}
		for _, child := range value {
			if match := findMonitoringTemplateObject(child, matches); match != nil {
				return match
			}
		}
	case []any:
		for _, child := range value {
			if match := findMonitoringTemplateObject(child, matches); match != nil {
				return match
			}
		}
	}
	return nil
}
