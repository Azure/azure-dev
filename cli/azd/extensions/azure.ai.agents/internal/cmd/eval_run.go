// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"azureaiagent/internal/pkg/agents/eval_api"
	"azureaiagent/internal/pkg/agents/opteval"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

type evalRunFlags struct {
	config string
	name   string
	noWait bool
}

func newEvalRunCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &evalRunFlags{config: defaultEvalConfigName}
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Execute an evaluation run from eval.yaml.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			logCleanup := setupDebugLogging(cmd.Flags())
			defer logCleanup()
			return runEvalRun(ctx, flags, extCtx.NoPrompt)
		},
	}
	cmd.Flags().StringVar(&flags.config, "config", defaultEvalConfigName, "Local eval config YAML")
	cmd.Flags().StringVar(&flags.name, "name", "", "Name for the eval run (defaults to eval config name)")
	cmd.Flags().BoolVar(&flags.noWait, "no-wait", false, "Start the run and return immediately without waiting for results")
	return cmd
}

func runEvalRun(ctx context.Context, flags *evalRunFlags, noPrompt bool) error {
	resolved, err := resolveEvalContext(ctx, evalContextOptions{})
	if err != nil {
		return err
	}
	defer resolved.azdClient.Close()

	configPath := eval_api.ResolveEvalConfigPath(flags.config, resolved.agentProject)
	evalCfg, err := eval_api.LoadEvalConfig(configPath)
	if err != nil {
		return err
	}

	// Reconcile agent name/version between environment and eval.yaml.
	// Environment values take precedence; warn and update the config if they differ.
	configChanged := false
	if resolved.agentName == "" {
		resolved.agentName = evalCfg.Agent.Name
	} else if evalCfg.Agent.Name != "" && evalCfg.Agent.Name != resolved.agentName {
		fmt.Printf("  %s agent name in %s (%q) differs from environment (%q) — using environment value\n",
			color.YellowString("warning:"), flags.config, evalCfg.Agent.Name, resolved.agentName)
		evalCfg.Agent.Name = resolved.agentName
		configChanged = true
	}
	if resolved.version == "" {
		resolved.version = evalCfg.Agent.Version
	} else if evalCfg.Agent.Version != "" && evalCfg.Agent.Version != resolved.version {
		fmt.Printf("  %s agent version in %s (%q) differs from environment (%q) — using environment value\n",
			color.YellowString("warning:"), flags.config, evalCfg.Agent.Version, resolved.version)
		evalCfg.Agent.Version = resolved.version
		configChanged = true
	}
	if configChanged {
		if err := eval_api.WriteEvalConfig(configPath, evalCfg); err != nil {
			fmt.Printf("  %s failed to update %s: %s\n", color.YellowString("warning:"), flags.config, err)
		} else {
			fmt.Printf("  Updated %s with current environment values\n", flags.config)
		}
	}

	state := loadEvalState(ctx, resolved.azdClient, resolved.envName)

	if state.InitStatus == "pending" {
		if err := resumeEvalInit(ctx, resolved, configPath, evalCfg, state); err != nil {
			return err
		}
	}

	evalID := state.EvalID
	if evalID != "" && !noPrompt {
		// Ask whether to reuse the existing eval or create a new one.
		resp, promptErr := resolved.azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
			Options: &azdext.ConfirmOptions{
				Message:      fmt.Sprintf("Found existing eval %s. Reuse it?", evalID),
				DefaultValue: new(false),
			},
		})
		if promptErr == nil && resp.Value != nil && !*resp.Value {
			evalID = "" // user chose to create a new eval
		}
	}

	if evalID == "" {
		created, err := resolved.evalClient.CreateOpenAIEval(
			ctx, buildOpenAIEvalRequest(evalCfg), DefaultAgentAPIVersion,
		)
		if err != nil {
			return fmt.Errorf("failed to create eval: %w", err)
		}
		evalID = created.ResolvedID()
		if evalID == "" {
			evalID = evalCfg.Name
		}
		state.EvalID = evalID
		if err := saveEvalState(ctx, resolved.azdClient, resolved.envName, state); err != nil {
			return err
		}
	}

	runReq := &eval_api.CreateOpenAIEvalRunRequest{
		Name:     resolveRunName(ctx, resolved.azdClient, flags.name, evalCfg.Name, noPrompt),
		Metadata: map[string]string{"azd_agent": evalCfg.Agent.Name},
	}

	// Build agent target data source.
	dataSource := eval_api.NewAgentTargetDataSource(
		resolved.agentName, agentVersionPtr(resolved.version),
	)

	// Set source from local dataset file or remote dataset reference.
	if evalCfg.DatasetFile != "" {
		items, err := loadEvalDatasetFile(evalCfg.DatasetFile)
		if err != nil {
			return err
		}
		dataSource.SetFileContent(items)
	} else if evalCfg.DatasetReference != nil {
		fileID := buildDatasetFileID(resolved.projectEndpoint, evalCfg.DatasetReference)
		dataSource.SetFileID(fileID)
	} else {
		return fmt.Errorf("no dataset configured; run 'azd ai agent eval init' or specify dataset_file / dataset_reference in the eval config")
	}

	runReq.DataSource = dataSource

	run, err := resolved.evalClient.CreateOpenAIEvalRun(
		ctx,
		evalID,
		runReq,
		DefaultAgentAPIVersion,
	)
	if err != nil {
		return fmt.Errorf("failed to start eval run: %w", err)
	}

	fmt.Println(color.GreenString("Eval run started"))
	fmt.Printf("   Eval: %s\n", evalID)
	if run.ID != "" {
		fmt.Printf("   Run:  %s\n", run.ID)
	}

	reportURL := buildEvalReportURL(ctx, resolved.azdClient, resolved.envName, evalID, run.ID)
	if reportURL != "" {
		fmt.Printf("   Report: %s\n", color.CyanString(reportURL))
	}

	if flags.noWait {
		fmt.Printf("\n   To view result summary, run:\n     %s\n     %s\n",
			color.CyanString("azd ai agent eval list"),
			color.CyanString("azd ai agent eval show"),
		)
		return nil
	}

	// Poll until the eval run reaches a terminal state.
	completed, err := pollEvalRun(ctx, resolved.evalClient, evalID, run.ID)
	if err != nil {
		return err
	}

	// Report URL was already printed above; clear it to avoid duplication.
	completed.ReportURL = ""

	fmt.Println()
	return printEvalRunSummary(evalID, completed)
}

// resolveRunName determines the eval run name from the flag, interactive
// prompt, or config default (in that priority order).
func resolveRunName(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	flagName, configName string,
	noPrompt bool,
) string {
	if flagName != "" {
		return flagName
	}

	defaultName := configName
	if defaultName == "" {
		defaultName = defaultEvalName
	}

	if !noPrompt {
		resp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:        "Eval run name",
				DefaultValue:   defaultName,
				IgnoreHintKeys: true,
			},
		})
		if err == nil {
			if value := strings.TrimSpace(resp.Value); value != "" {
				return value
			}
		}
	}

	return defaultName
}

// pollEvalRun polls an eval run until it reaches a terminal status.
// Terminal statuses: "completed", "failed", "canceled".
func pollEvalRun(
	ctx context.Context,
	client *eval_api.EvalClient,
	evalID, runID string,
) (*eval_api.OpenAIEvalRun, error) {
	const (
		interval    = 5 * time.Second
		maxAttempts = 360 // ~30 minutes
	)

	progress := newEvalProgress()
	progress.Start()
	defer progress.Stop()

	progress.setRunning("Eval run", runID)

	for range maxAttempts {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}

		run, err := client.GetOpenAIEvalRun(ctx, evalID, runID, DefaultAgentAPIVersion)
		if err != nil {
			progress.setFailed("Eval run")
			return nil, fmt.Errorf("failed to poll eval run: %w", err)
		}

		switch run.Status {
		case "completed":
			progress.setDone("Eval run")
			return run, nil
		case "failed":
			progress.setFailed("Eval run")
			errMsg := "eval run failed"
			if run.Error != nil {
				errMsg = fmt.Sprintf("eval run failed: %v", run.Error)
			}
			return nil, fmt.Errorf("%s", errMsg)
		case "canceled", "cancelled":
			progress.setFailed("Eval run")
			return nil, fmt.Errorf("eval run was canceled")
		}
	}

	progress.setTimedOut("Eval run")
	return nil, fmt.Errorf("eval run %s did not complete within %d attempts", runID, maxAttempts)
}

// loadEvalDatasetFile reads a JSONL file and returns each line as a map.
func loadEvalDatasetFile(path string) ([]map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open dataset file %s: %w", path, err)
	}
	defer f.Close()

	var items []map[string]any
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if line == "" {
			continue
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, fmt.Errorf("failed to parse dataset line %d: %w", lineNum, err)
		}
		items = append(items, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading dataset file %s: %w", path, err)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("dataset file %s contains no items", path)
	}
	return items, nil
}

// buildDatasetFileID constructs an azureai:// URI for a remote dataset reference.
// Format: azureai://accounts/<account>/projects/<project>/data/<name>/versions/<version>
// The account and project are extracted from the project endpoint URL
// (https://<account>.services.ai.azure.com/api/projects/<project>).
func buildDatasetFileID(projectEndpoint string, ref *opteval.DatasetRef) string {
	account, project := parseProjectEndpoint(projectEndpoint)
	version := ref.Version
	if version == "" {
		version = "1"
	}
	return fmt.Sprintf("azureai://accounts/%s/projects/%s/data/%s/versions/%s",
		account, project, ref.Name, version)
}

// parseProjectEndpoint extracts account and project names from a Foundry project endpoint URL.
func parseProjectEndpoint(endpoint string) (account, project string) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", ""
	}
	// Host format: <account>.services.ai.azure.com
	host := u.Hostname()
	if idx := strings.Index(host, "."); idx > 0 {
		account = host[:idx]
	}
	// Path format: /api/projects/<project>
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if p == "projects" && i+1 < len(parts) {
			project = parts[i+1]
			break
		}
	}
	return account, project
}

// agentVersionPtr returns a pointer to the version string, or nil if empty.
func agentVersionPtr(version string) *string {
	if version == "" {
		return nil
	}
	return &version
}
