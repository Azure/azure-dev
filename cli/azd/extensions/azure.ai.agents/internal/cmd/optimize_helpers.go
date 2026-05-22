// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// optimize_helpers.go provides shared utilities for optimize commands:
// connection flag resolution, job ID persistence in the azd environment,
// and portal link construction.

package cmd

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"azureaiagent/internal/pkg/agents/eval_api"
	"azureaiagent/internal/pkg/agents/optimize_api"

	azdext "github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// optimizeConnectionFlags holds connection settings shared across all optimize sub-commands.
type optimizeConnectionFlags struct {
	projectEndpoint string // Foundry project endpoint URL
	endpoint        string // direct optimization service URL (for local dev only)
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

// optimizeLastJobIDKey is the azd environment key for the last optimization job ID.
const optimizeLastJobIDKey = "OPTIMIZE_LAST_OPERATION_ID"

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

// printOptimizePortalLink prints the Foundry portal URL for an optimization job.
// Best-effort — silently skips if the portal prefix cannot be resolved.
func printOptimizePortalLink(ctx context.Context, out io.Writer, agentName, operationID string) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return
	}
	defer azdClient.Close()

	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil || envResp == nil {
		return
	}

	printPortalLink(ctx, out, azdClient, envResp.Environment.Name, func(prefix *eval_api.PortalPrefix) string {
		return prefix.OptimizationURL(agentName, operationID)
	})
}

// reportOptimizationDeployments reports optimization candidate deployments to the optimization service.
// For each hosted agent service, if AGENT_{KEY}_OPTIMIZATION_CANDIDATE_ID is set in
// the azd environment, it calls the promote API and then clears the env var.
// This is best-effort — failures are logged but do not block the deploy.
func reportOptimizationDeployments(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	hostedAgents []*azdext.ServiceConfig,
	envName, projectEndpoint string,
	newClient func(endpoint string) *optimize_api.OptimizeClient,
) {
	log.Printf("postdeploy: reporting optimization deployments for %d hosted agents", len(hostedAgents))

	for _, svc := range hostedAgents {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("postdeploy: optimization reporting panicked for %s: %v", svc.Name, r)
				}
			}()
			reportSvcOptimizationDeployment(ctx, azdClient, svc, envName, projectEndpoint, newClient)
		}()
	}
}

// reportSvcOptimizationDeployment reports a single service's optimization candidate.
func reportSvcOptimizationDeployment(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	svc *azdext.ServiceConfig,
	envName, projectEndpoint string,
	newClient func(endpoint string) *optimize_api.OptimizeClient,
) {
	serviceKey := toServiceKey(svc.Name)
	candidateKey := fmt.Sprintf("AGENT_%s_OPTIMIZATION_CANDIDATE_ID", serviceKey)

	candidateResp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     candidateKey,
	})
	if err != nil || candidateResp.Value == "" {
		log.Printf("postdeploy: no optimization candidate for %s, skipping", svc.Name)
		return
	}

	versionKey := fmt.Sprintf("AGENT_%s_VERSION", serviceKey)
	versionResp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     versionKey,
	})
	if err != nil || versionResp.Value == "" {
		log.Printf("postdeploy: no version for %s, skipping", svc.Name)
		return
	}

	log.Printf("postdeploy: promoting candidate %s for %s (version %s)",
		candidateResp.Value, svc.Name, versionResp.Value)

	optClient := newClient(projectEndpoint)
	if err := optClient.ReportDeployment(ctx, &optimize_api.DeploymentReport{
		CandidateID:  candidateResp.Value,
		AgentName:    svc.Name,
		AgentVersion: versionResp.Value,
	}); err != nil {
		log.Printf("postdeploy: failed to report optimization deployment for %s: %v", svc.Name, err)
		return
	}

	log.Printf("postdeploy: successfully promoted candidate %s for %s", candidateResp.Value, svc.Name)

	// Clear the candidate ID after successful reporting.
	if _, err := azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envName,
		Key:     candidateKey,
		Value:   "",
	}); err != nil {
		log.Printf("postdeploy: failed to clear %s: %v", candidateKey, err)
	}
}
