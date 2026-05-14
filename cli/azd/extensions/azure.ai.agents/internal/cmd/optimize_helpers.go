// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	azdext "github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// optimizeConnectionFlags holds connection settings shared across all optimize sub-commands.
type optimizeConnectionFlags struct {
	projectEndpoint string
	endpoint        string // override: direct optimization service URL (for local dev only)
}

// register adds the connection flags to the given cobra command.
func (f *optimizeConnectionFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&f.projectEndpoint, "project-endpoint", "p", "", "Foundry project endpoint URL")
	cmd.Flags().StringVar(&f.endpoint, "endpoint", "", "Optimization service endpoint (for local dev)")
}

// resolve returns the project endpoint for optimize API calls.
// Priority: --endpoint flag → AZURE_AI_OPTIMIZE_ENDPOINT → --project-endpoint → azd environment → AZURE_AI_PROJECT_ENDPOINT env var.
func (f *optimizeConnectionFlags) resolve(ctx context.Context) (string, error) {
	if f.endpoint != "" {
		return strings.TrimRight(f.endpoint, "/"), nil
	}
	if ep := os.Getenv("AZURE_AI_OPTIMIZE_ENDPOINT"); ep != "" {
		return strings.TrimRight(ep, "/"), nil
	}

	// Explicit --project-endpoint flag
	if f.projectEndpoint != "" {
		return strings.TrimRight(f.projectEndpoint, "/"), nil
	}

	// Try azd environment (works when running under azd)
	projectEndpoint, err := resolveAgentEndpoint(ctx, "", "")
	if err != nil {
		// Fall back to AZURE_AI_PROJECT_ENDPOINT env var (works standalone)
		if ep := os.Getenv("AZURE_AI_PROJECT_ENDPOINT"); ep != "" {
			return strings.TrimRight(ep, "/"), nil
		}
		return "", fmt.Errorf("could not resolve project endpoint\n\n" +
			"Set AZURE_AI_PROJECT_ENDPOINT, provide --project-endpoint (-p),\n" +
			"or run 'azd ai agent init'")
	}

	return projectEndpoint, nil
}

// optimizeAPIVersion is the API version used for optimization service calls.
const optimizeAPIVersion = "v1"

// optimizeLastJobIDKey is the azd environment key for the last optimization job ID.
const optimizeLastJobIDKey = "OPTIMIZE_LAST_OPERATION_ID"

// tokenRequestOptions returns the token request options for Azure AI scope.
func tokenRequestOptions() policy.TokenRequestOptions {
	return policy.TokenRequestOptions{
		Scopes: []string{"https://ai.azure.com/.default"},
	}
}

// saveLastOptimizeJobID stores the operation ID in the azd environment.
// Best-effort — silently ignores errors (e.g., when running outside azd).
func saveLastOptimizeJobID(ctx context.Context, operationID string) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return
	}
	defer azdClient.Close()

	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil || envResp == nil {
		return
	}

	_, _ = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envResp.Environment.Name,
		Key:     optimizeLastJobIDKey,
		Value:   operationID,
	})
}

// loadLastOptimizeJobID retrieves the last operation ID from the azd environment.
// Returns empty string if not available.
func loadLastOptimizeJobID(ctx context.Context) string {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return ""
	}
	defer azdClient.Close()

	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil || envResp == nil {
		return ""
	}

	resp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envResp.Environment.Name,
		Key:     optimizeLastJobIDKey,
	})
	if err != nil || resp == nil {
		return ""
	}
	return resp.Value
}
