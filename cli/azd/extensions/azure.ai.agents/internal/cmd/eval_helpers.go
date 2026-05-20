// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"log"

	"azureaiagent/internal/pkg/agents/eval_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

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
