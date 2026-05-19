// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"

	"azureaiagent/internal/pkg/agents/eval_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// resolveEvalOutputPath resolves the eval config output path.
func resolveEvalOutputPath(output, agentProject string) string {
	return eval_api.ResolveEvalOutputPath(output, agentProject)
}

// resolveEvalConfigPath resolves the eval config path for reading.
func resolveEvalConfigPath(config, agentProject string) string {
	return eval_api.ResolveEvalConfigPath(config, agentProject)
}

// resolvePortalPrefix reads AZURE_AI_PROJECT_ID from the azd environment and
// returns a PortalPrefix for building Foundry portal URLs.
// Returns nil on any failure.
func resolvePortalPrefix(ctx context.Context, azdClient *azdext.AzdClient, envName string) *eval_api.PortalPrefix {
	if azdClient == nil || envName == "" {
		return nil
	}
	v, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     "AZURE_AI_PROJECT_ID",
	})
	if err != nil || v.Value == "" {
		log.Printf("[debug] could not read AZURE_AI_PROJECT_ID: %v", err)
		return nil
	}
	prefix, err := eval_api.NewPortalPrefix(v.Value)
	if err != nil {
		log.Printf("[debug] failed to build portal prefix: %v", err)
		return nil
	}
	return prefix
}

// buildEvalReportURL constructs the Foundry portal URL for an eval run report.
// Returns empty string on any failure.
func buildEvalReportURL(ctx context.Context, azdClient *azdext.AzdClient, envName, evalID, runID string) string {
	if evalID == "" || runID == "" {
		return ""
	}
	prefix := resolvePortalPrefix(ctx, azdClient, envName)
	if prefix == nil {
		return ""
	}
	return prefix.EvalRunURL(evalID, runID)
}

// formatAny converts any value to a string for display.
func formatAny(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	case bool:
		return fmt.Sprintf("%t", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}
