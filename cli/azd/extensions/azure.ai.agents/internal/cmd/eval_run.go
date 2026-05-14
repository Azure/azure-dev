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

	"azureaiagent/internal/pkg/agents/eval_api"
	"azureaiagent/internal/pkg/agents/opteval"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

type evalRunFlags struct {
	config string
}

func newEvalRunCommand() *cobra.Command {
	flags := &evalRunFlags{config: defaultEvalConfigName}
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Execute an evaluation run from eval.yaml.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			logCleanup := setupDebugLogging(cmd.Flags())
			defer logCleanup()
			return runEvalRun(ctx, flags)
		},
	}
	cmd.Flags().StringVar(&flags.config, "config", defaultEvalConfigName, "Local eval config YAML")
	return cmd
}

func runEvalRun(ctx context.Context, flags *evalRunFlags) error {
	resolved, err := resolveEvalContext(ctx, evalContextOptions{})
	if err != nil {
		return err
	}
	defer resolved.azdClient.Close()

	configPath := resolveEvalConfigPath(flags.config, resolved.agentProject)
	evalCfg, err := readEvalConfig(configPath)
	if err != nil {
		return err
	}
	if resolved.agentName == "" {
		resolved.agentName = evalCfg.Agent.Name
	}
	if resolved.version == "" {
		resolved.version = evalCfg.Agent.Version
	}

	state := loadEvalState(ctx, resolved.azdClient, resolved.envName)

	if state.InitStatus == "pending" {
		if err := resumeEvalInit(ctx, resolved, configPath, evalCfg, state); err != nil {
			return err
		}
	}

	evalID := state.EvalID
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
		Name:     evalCfg.Name,
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
	if run.ReportURL != "" {
		fmt.Printf("   Report: %s\n", run.ReportURL)
	}
	return nil
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
