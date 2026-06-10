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
// projectEndpointFromEnv returns the project endpoint from FOUNDRY_PROJECT_ENDPOINT
// or AZURE_AI_PROJECT_ENDPOINT (deprecated) environment variables (in that priority order).
// Returns empty string if neither is set.
func projectEndpointFromEnv() string {
	if ep := os.Getenv("FOUNDRY_PROJECT_ENDPOINT"); ep != "" {
		return strings.TrimRight(ep, "/")
	}
	if ep := os.Getenv("AZURE_AI_PROJECT_ENDPOINT"); ep != "" { // deprecated fallback
		return strings.TrimRight(ep, "/")
	}
	return ""
}

// Priority: --endpoint flag → --project-endpoint → azd environment → FOUNDRY_PROJECT_ENDPOINT env var.
// envName selects a specific azd environment; empty means "current default".
func (f *optimizeConnectionFlags) resolve(ctx context.Context, envName string) (string, error) {
	if f.endpoint != "" {
		return strings.TrimRight(f.endpoint, "/"), nil
	}

	// Explicit --project-endpoint flag
	if f.projectEndpoint != "" {
		return strings.TrimRight(f.projectEndpoint, "/"), nil
	}

	// When an explicit envName is provided, read FOUNDRY_PROJECT_ENDPOINT
	// directly from that environment instead of relying on the default
	// cascade (which always reads the current/default environment).
	if envName != "" {
		if ep := endpointFromNamedEnv(ctx, envName); ep != "" {
			return strings.TrimRight(ep, "/"), nil
		}
	}

	// Try azd environment / global config cascade (works when running under azd)
	projectEndpoint, err := resolveAgentEndpoint(ctx, "", "")
	if err != nil {
		// Fall back to FOUNDRY_PROJECT_ENDPOINT or AZURE_AI_PROJECT_ENDPOINT env var (works standalone)
		if ep := projectEndpointFromEnv(); ep != "" {
			return ep, nil
		}
		return "", fmt.Errorf("could not resolve project endpoint\n\n" +
			"Set FOUNDRY_PROJECT_ENDPOINT, provide --project-endpoint (-p),\n" +
			"or run 'azd ai agent init'")
	}

	return projectEndpoint, nil
}

// endpointFromNamedEnv reads FOUNDRY_PROJECT_ENDPOINT from the specified
// azd environment. Returns empty string on any failure.
func endpointFromNamedEnv(ctx context.Context, envName string) string {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return ""
	}
	defer azdClient.Close()

	env := getExistingEnvironment(ctx, envName, azdClient)
	if env == nil {
		return ""
	}
	v, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: env.Name,
		Key:     "FOUNDRY_PROJECT_ENDPOINT",
	})
	if err != nil || v.Value == "" {
		return ""
	}
	return v.Value
}

// optimizeLastJobIDKey is the global azd environment key for the most recent
// optimization job ID. It is used by `optimize status` (which has no agent
// context) and as a fallback when a per-agent job ID is not set.
const optimizeLastJobIDKey = "OPTIMIZE_LAST_OPERATION_ID"

// optimizeJobIDKeyForAgent returns the per-agent azd environment key that stores
// the optimization job ID, mirroring AGENT_{KEY}_OPTIMIZATION_CANDIDATE_ID.
func optimizeJobIDKeyForAgent(agentName string) string {
	return fmt.Sprintf("AGENT_%s_OPTIMIZATION_JOB_ID", toServiceKey(agentName))
}

// saveLastOptimizeJobID stores the operation ID in the azd environment under
// both the per-agent key (AGENT_{KEY}_OPTIMIZATION_JOB_ID) and the global
// last-job key. Best-effort — silently ignores errors (e.g., when running
// outside azd).
func saveLastOptimizeJobID(ctx context.Context, agentName, operationID, envName string) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return
	}
	defer azdClient.Close()

	env := getExistingEnvironment(ctx, envName, azdClient)
	if env == nil {
		return
	}

	// Per-agent key — used by apply/deploy/postdeploy to promote the correct job.
	if agentName != "" {
		_, _ = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: env.Name,
			Key:     optimizeJobIDKeyForAgent(agentName),
			Value:   operationID,
		})
	}

	// Global key — convenience for `optimize status` and a fallback.
	_, _ = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: env.Name,
		Key:     optimizeLastJobIDKey,
		Value:   operationID,
	})
}

// loadOptimizeJobIDForAgent retrieves the optimization job ID for a specific
// agent, preferring the per-agent key and falling back to the global last-job
// key. Returns empty string if neither is available.
func loadOptimizeJobIDForAgent(ctx context.Context, agentName, envName string) string {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return ""
	}
	defer azdClient.Close()

	env := getExistingEnvironment(ctx, envName, azdClient)
	if env == nil {
		return ""
	}

	if agentName != "" {
		if resp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: env.Name,
			Key:     optimizeJobIDKeyForAgent(agentName),
		}); err == nil && resp != nil && resp.Value != "" {
			return resp.Value
		}
	}

	resp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: env.Name,
		Key:     optimizeLastJobIDKey,
	})
	if err != nil || resp == nil {
		return ""
	}
	return resp.Value
}

// loadLastOptimizeJobID retrieves the global last optimization job ID from the
// azd environment. Used by `optimize status`, which has no agent context.
// Returns empty string if not available.
func loadLastOptimizeJobID(ctx context.Context, envName string) string {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return ""
	}
	defer azdClient.Close()

	env := getExistingEnvironment(ctx, envName, azdClient)
	if env == nil {
		return ""
	}

	resp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: env.Name,
		Key:     optimizeLastJobIDKey,
	})
	if err != nil || resp == nil {
		return ""
	}
	return resp.Value
}

// printOptimizePortalLink prints the Foundry portal URL for an optimization job.
// Best-effort — silently skips if the portal prefix cannot be resolved.
func printOptimizePortalLink(ctx context.Context, out io.Writer, agentName, operationID, envName string) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return
	}
	defer azdClient.Close()

	env := getExistingEnvironment(ctx, envName, azdClient)
	if env == nil {
		return
	}

	printPortalLink(ctx, out, azdClient, env.Name, func(prefix *eval_api.PortalPrefix) string {
		return prefix.OptimizationURL(agentName, operationID)
	})
}

// isInAzdProject returns true if the current directory is inside an azd project.
func isInAzdProject(ctx context.Context) bool {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return false
	}
	defer azdClient.Close()

	resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	return err == nil && resp != nil && resp.Project != nil
}

// buildCandidateEvalURLs returns a map of candidate name → Foundry portal eval URL
// for candidates that have both EvalID and EvalRunID set.
// Best-effort — returns nil on any failure and never panics.
func buildCandidateEvalURLs(
	ctx context.Context,
	candidates []optimize_api.CandidateResult,
	explicitEnvName string,
) (result map[string]string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[debug] buildCandidateEvalURLs recovered from panic: %v", r)
			result = nil
		}
	}()

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return nil
	}
	defer azdClient.Close()

	env := getExistingEnvironment(ctx, explicitEnvName, azdClient)
	if env == nil {
		return nil
	}
	envName := env.Name

	urls := make(map[string]string)
	for _, c := range candidates {
		url := buildEvalReportURL(ctx, azdClient, envName, c.EvalID, c.EvalRunID)
		if url != "" {
			urls[c.Name] = url
		}
	}
	if len(urls) == 0 {
		return nil
	}
	return urls
}

// terminalHyperlink formats a clickable hyperlink using the OSC 8 escape sequence.
// Terminals that support it (Windows Terminal, iTerm2, etc.) render the text as
// a clickable link; unsupported terminals display only the text.
func terminalHyperlink(url, text string) string {
	return fmt.Sprintf("\033]8;;%s\033\\%s\033]8;;\033\\", url, text)
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

	// Candidate promotion is nested under the optimization job; resolve the
	// per-agent job ID (AGENT_{KEY}_OPTIMIZATION_JOB_ID), falling back to the
	// global last-job key.
	jobIDKey := fmt.Sprintf("AGENT_%s_OPTIMIZATION_JOB_ID", serviceKey)
	jobID := ""
	if jobResp, jobErr := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     jobIDKey,
	}); jobErr == nil && jobResp != nil && jobResp.Value != "" {
		jobID = jobResp.Value
	} else if jobResp, jobErr := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     optimizeLastJobIDKey,
	}); jobErr == nil && jobResp != nil {
		jobID = jobResp.Value
	}
	if jobID == "" {
		log.Printf("postdeploy: no optimization job ID for %s, skipping promotion", svc.Name)
		return
	}

	optClient := newClient(projectEndpoint)
	if err := optClient.ReportDeployment(ctx, jobID, &optimize_api.DeploymentReport{
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
