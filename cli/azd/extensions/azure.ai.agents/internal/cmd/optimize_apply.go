// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"

	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

type optimizeApplyFlags struct {
	candidate string
	agent     string
	optimizeConnectionFlags
}

func newOptimizeApplyCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &optimizeApplyFlags{}
	action := &OptimizeApplyAction{flags: flags, noPrompt: extCtx.NoPrompt}

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply optimized candidate configuration locally to your azd project.",
		Long: `Download the optimized configuration and skill files from an optimization
candidate and write them into your local azd project.

After applying, run 'azd deploy' to deploy the optimized agent version.`,
		Example: `  # Apply candidate config locally, then deploy
  azd ai agent optimize apply --candidate cand_abc123
  azd deploy`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())
			return action.Run(ctx, cmd)
		},
	}

	cmd.Flags().StringVar(&flags.candidate, "candidate", "", "Candidate ID from optimization results (required)")
	cmd.Flags().StringVar(&flags.agent, "agent", "", "Agent service name (auto-detected from azure.yaml)")
	_ = cmd.MarkFlagRequired("candidate")
	flags.optimizeConnectionFlags.register(cmd)

	return cmd
}

// OptimizeApplyAction implements the optimize apply command.
type OptimizeApplyAction struct {
	flags    *optimizeApplyFlags
	noPrompt bool
}

func (a *OptimizeApplyAction) Run(ctx context.Context, cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	bold := color.New(color.Bold)

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w\n\n"+
			"'optimize apply' requires an azd project. Use 'optimize deploy' for standalone API deployment", err)
	}
	defer azdClient.Close()

	svc, project, err := resolveAgentService(ctx, azdClient, a.flags.agent, a.noPrompt)
	if err != nil || project == nil || svc == nil {
		return fmt.Errorf("could not resolve agent service in azd project: %w\n\n"+
			"Run 'azd ai agent init' first, or use 'optimize deploy' for standalone API deployment", err)
	}

	return a.apply(ctx, azdClient, svc, project, out, bold)
}

func (a *OptimizeApplyAction) apply(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	svc *azdext.ServiceConfig,
	project *azdext.ProjectConfig,
	out io.Writer,
	bold *color.Color,
) error {
	projectEndpoint, err := resolveProjectEndpointForDeploy(ctx, &a.flags.optimizeConnectionFlags)
	if err != nil {
		return err
	}
	agentYamlPath := filepath.Join(project.Path, svc.RelativePath, "agent.yaml")

	bold.Fprintf(out, "Applying optimization candidate %s...\n\n", a.flags.candidate)

	credential, err := newAgentCredential()
	if err != nil {
		return err
	}
	optClient := optimize_api.NewOptimizeClient(projectEndpoint, credential)

	// Step 1: Fetch candidate config.
	fmt.Fprintf(out, "  Fetching candidate config...\n")
	candidateConfig, err := optClient.GetCandidateConfig(ctx, a.flags.candidate)
	if err != nil {
		return fmt.Errorf("failed to fetch candidate config: %w", err)
	}

	configJSON, err := json.Marshal(candidateConfig)
	if err != nil {
		return fmt.Errorf("failed to serialize candidate config: %w", err)
	}

	// Step 2: Write OPTIMIZATION_CONFIG and OPTIMIZATION_CANDIDATE_ID into agent.yaml.
	fmt.Fprintf(out, "  Updating %s...\n", agentYamlPath)
	if err := upsertAgentYamlEnvVar(agentYamlPath, "OPTIMIZATION_CONFIG", string(configJSON)); err != nil {
		return fmt.Errorf("failed to update agent.yaml: %w", err)
	}
	if err := upsertAgentYamlEnvVar(agentYamlPath, "OPTIMIZATION_CANDIDATE_ID", a.flags.candidate); err != nil {
		return fmt.Errorf("failed to update agent.yaml: %w", err)
	}

	// Step 3: Download skill files from the candidate manifest.
	serviceDir := filepath.Join(project.Path, svc.RelativePath)
	if n, dlErr := downloadSkillFiles(ctx, optClient, a.flags.candidate, serviceDir, out); dlErr != nil {
		fmt.Fprintf(out, "  warning: failed to download skill files: %s\n", dlErr)
	} else if n > 0 {
		fmt.Fprintf(out, "  Downloaded %d skill file(s)\n", n)
	}

	// Step 4: Store candidate ID in the azd environment for postdeploy tracking.
	serviceKey := toServiceKey(svc.Name)
	candidateKey := fmt.Sprintf("AGENT_%s_OPTIMIZATION_CANDIDATE_ID", serviceKey)

	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("failed to get current environment: %w", err)
	}
	if _, err := azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envResp.Environment.Name,
		Key:     candidateKey,
		Value:   a.flags.candidate,
	}); err != nil {
		return fmt.Errorf("failed to store candidate ID in azd environment: %w", err)
	}

	// Done — prompt the user to deploy.
	fmt.Fprintln(out)
	color.New(color.FgGreen, color.Bold).Fprintf(out,
		"  ✓ Candidate %s applied successfully\n\n", a.flags.candidate)
	fmt.Fprintf(out, "  Run %s to deploy the optimized agent.\n",
		color.CyanString("azd deploy --service %s", svc.Name))

	return nil
}
