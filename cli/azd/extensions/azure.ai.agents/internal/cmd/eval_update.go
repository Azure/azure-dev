// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"azureaiagent/internal/pkg/agents/dataset_api"
	"azureaiagent/internal/pkg/agents/eval_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

type evalUpdateFlags struct {
	config        string
	datasetOnly   bool
	evaluatorOnly bool
}

func newEvalUpdateCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &evalUpdateFlags{config: defaultEvalConfigName}
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update evaluators and datasets from local files.",
		Long: `Reads the eval config and uploads new versions for:
  - Evaluators with a local_uri (rubric dimensions file)
  - Datasets with a local_uri (JSONL data directory)
The version fields in the config are updated after successful uploads.

In interactive mode, you will be prompted for each asset type that has
local changes. Use --dataset-only or --evaluator-only to skip prompts.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			logCleanup := setupDebugLogging(cmd.Flags())
			defer logCleanup()
			return runEvalUpdate(ctx, flags, extCtx.NoPrompt)
		},
	}
	cmd.Flags().StringVar(&flags.config, "config", defaultEvalConfigName, "Local eval config YAML")
	cmd.Flags().BoolVar(&flags.datasetOnly, "dataset-only", false, "Only update the dataset")
	cmd.Flags().BoolVar(&flags.evaluatorOnly, "evaluator-only", false, "Only update evaluators")
	return cmd
}

func runEvalUpdate(ctx context.Context, flags *evalUpdateFlags, noPrompt bool) error {
	resolved, err := resolveEvalContext(ctx, evalContextOptions{})
	if err != nil {
		return err
	}
	defer resolved.azdClient.Close()

	configPath := eval_api.ResolveEvalConfigPath(flags.config, resolved.agentProject)
	evalCfg, err := eval_api.LoadEvalConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load eval config: %w", err)
	}

	// Detect what has local changes.
	hasDataset := evalCfg.DatasetReference != nil &&
		evalCfg.DatasetReference.Name != "" &&
		evalCfg.DatasetReference.LocalURI != ""
	hasEvaluators := len(evalCfg.Evaluators.FindByLocalURI()) > 0

	// Determine what to update based on flags and interactive prompts.
	updateDS := hasDataset && !flags.evaluatorOnly
	updateEval := hasEvaluators && !flags.datasetOnly

	// In interactive mode (no exclusive flags), prompt for each detected type.
	if !noPrompt && !flags.datasetOnly && !flags.evaluatorOnly {
		if hasDataset {
			updateDS = confirmUpdate(ctx, resolved, fmt.Sprintf(
				"Dataset %s has local changes. Upload new version?",
				evalCfg.DatasetReference.Name,
			))
		}
		if hasEvaluators {
			updateEval = confirmUpdate(ctx, resolved, "Evaluator(s) have local changes. Upload new version(s)?")
		}
	}

	var totalUpdated int

	if updateDS {
		dsUpdated, err := updateDataset(ctx, resolved.datasetClient, evalCfg, configPath)
		if err != nil {
			return err
		}
		totalUpdated += dsUpdated
	}

	if updateEval {
		evalUpdated, err := updateEvaluators(ctx, resolved.evalClient, evalCfg, configPath)
		if err != nil {
			return err
		}
		totalUpdated += evalUpdated
	}

	if totalUpdated > 0 {
		if err := eval_api.WriteEvalConfig(configPath, evalCfg); err != nil {
			return fmt.Errorf("failed to save updated config: %w", err)
		}
		fmt.Printf("\n%s Updated config saved to %s\n", color.GreenString("Done."), flags.config)
	} else {
		fmt.Println("\nNo updates were made.")
	}

	return nil
}

// confirmUpdate prompts the user with a yes/no question, defaulting to yes.
func confirmUpdate(ctx context.Context, resolved *evalResolvedContext, message string) bool {
	resp, err := resolved.azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      message,
			DefaultValue: new(true),
		},
	})
	if err != nil {
		return true // on error, default to updating
	}
	return resp.Value != nil && *resp.Value
}

// updateDataset uploads local dataset files as a new dataset version.
// Returns the number of datasets updated (0 or 1).
func updateDataset(
	ctx context.Context,
	client *dataset_api.DatasetClient,
	evalCfg *evalConfig,
	configPath string,
) (int, error) {
	ref := evalCfg.DatasetReference
	if ref == nil || ref.Name == "" || ref.LocalURI == "" {
		return 0, nil
	}

	localDir := ref.LocalURI
	if !filepath.IsAbs(localDir) {
		localDir = filepath.Join(filepath.Dir(configPath), localDir)
	}

	resp, err := client.UploadNewVersion(ctx, ref.Name, ref.Version, localDir, DefaultAgentAPIVersion)
	if err != nil {
		fmt.Printf("  %s Failed to update dataset %s: %v\n", color.RedString("x"), ref.Name, err)
		return 0, nil
	}

	ref.Version = resp.Version
	fmt.Printf("  %s Dataset %s → version %s\n", color.GreenString("✓"), ref.Name, resp.Version)
	return 1, nil
}

// updateEvaluators uploads local evaluator dimensions as new evaluator versions.
// Returns the number of evaluators updated.
func updateEvaluators(
	ctx context.Context,
	client *eval_api.EvalClient,
	evalCfg *evalConfig,
	configPath string,
) (int, error) {
	localEvals := evalCfg.Evaluators.FindByLocalURI()
	if len(localEvals) == 0 {
		return 0, nil
	}

	var updated int
	for _, ref := range localEvals {
		localPath := ref.LocalURI
		if !filepath.IsAbs(localPath) {
			localPath = filepath.Join(filepath.Dir(configPath), localPath)
		}

		data, err := os.ReadFile(localPath) //nolint:gosec // user-provided local config path
		if err != nil {
			fmt.Printf("  %s Skipping %s: %v\n", color.YellowString("!"), ref.Name, err)
			continue
		}

		if !json.Valid(data) {
			fmt.Printf("  %s Skipping %s: file is not valid JSON\n", color.YellowString("!"), ref.Name)
			continue
		}

		current, err := client.GetEvaluatorRaw(ctx, ref.Name, ref.Version, DefaultAgentAPIVersion)
		if err != nil {
			fmt.Printf("  %s Failed to get evaluator %s: %v\n", color.RedString("x"), ref.Name, err)
			continue
		}

		var obj map[string]json.RawMessage
		if err := json.Unmarshal(current, &obj); err != nil {
			fmt.Printf("  %s Failed to parse evaluator %s: %v\n", color.RedString("x"), ref.Name, err)
			continue
		}

		// Patch dimensions into the existing definition.
		var defObj map[string]json.RawMessage
		if raw, ok := obj["definition"]; ok {
			if err := json.Unmarshal(raw, &defObj); err != nil {
				defObj = make(map[string]json.RawMessage)
			}
		} else {
			defObj = make(map[string]json.RawMessage)
		}
		defObj["dimensions"] = json.RawMessage(data)
		updatedDef, err := json.Marshal(defObj)
		if err != nil {
			fmt.Printf("  %s Failed to build definition for %s: %v\n", color.RedString("x"), ref.Name, err)
			continue
		}
		obj["definition"] = json.RawMessage(updatedDef)

		body, err := json.Marshal(obj)
		if err != nil {
			fmt.Printf("  %s Failed to build request for %s: %v\n", color.RedString("x"), ref.Name, err)
			continue
		}

		resp, err := client.CreateEvaluatorVersion(ctx, ref.Name, body, DefaultAgentAPIVersion)
		if err != nil {
			fmt.Printf("  %s Failed to update %s: %v\n", color.RedString("x"), ref.Name, err)
			continue
		}

		evalCfg.Evaluators.SetVersion(ref.Name, resp.Version)
		updated++
		fmt.Printf("  %s Evaluator %s → version %s\n", color.GreenString("✓"), ref.Name, resp.Version)
	}

	return updated, nil
}
