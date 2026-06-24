// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type rleInvokeFlags struct {
	recipe                     string
	projectEndpoint            string
	numTasks                   int
	modelName                  string
	rendererName               string
	maxTokens                  int
	loraRank                   int
	groupSize                  int
	groupsPerBatch             int
	maxSteps                   int
	lossFn                     string
	seed                       int
	evalEvery                  int
	saveEvery                  int
	removeConstantRewardGroups bool
}

func newInvokeCommand() *cobra.Command {
	flags := &rleInvokeFlags{
		recipe:                     defaultRecipeName,
		numTasks:                   4,
		modelName:                  "Qwen/Qwen3-32B",
		rendererName:               "qwen3_disable_thinking",
		maxTokens:                  1200,
		loraRank:                   32,
		groupSize:                  4,
		groupsPerBatch:             1,
		maxSteps:                   1,
		lossFn:                     "importance_sampling",
		seed:                       42,
		evalEvery:                  999999,
		saveEvery:                  999999,
		removeConstantRewardGroups: true,
	}

	cmd := &cobra.Command{
		Use:   "invoke",
		Short: "Run the Loom RLE training recipe",
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := loadRleState()
			if err != nil {
				return err
			}
			if state.EnvironmentId == "" {
				return &azdext.LocalError{
					Message:    "RLE environment has not been deployed.",
					Code:       "rle_environment_not_deployed",
					Category:   azdext.LocalErrorCategoryUser,
					Suggestion: "Run azd ai rle deploy from this session folder before invoking training.",
				}
			}

			if flags.projectEndpoint == "" {
				return &azdext.LocalError{
					Message:    "Azure AI project endpoint is required.",
					Code:       "rle_project_endpoint_required",
					Category:   azdext.LocalErrorCategoryUser,
					Suggestion: "Pass --project-endpoint with the Azure AI Foundry project endpoint.",
				}
			}
			recipeName, err := validateRecipeName(flags.recipe)
			if err != nil {
				return &azdext.LocalError{
					Message:    err.Error(),
					Code:       "rle_invalid_recipe_name",
					Category:   azdext.LocalErrorCategoryUser,
					Suggestion: "Use a Loom recipe folder name in snake_case, for example code_rl_with_rle.",
				}
			}

			if _, err := exec.LookPath("git"); err != nil {
				return &azdext.LocalError{
					Message:    "Could not find \"git\" on PATH.",
					Code:       "rle_git_not_found",
					Category:   azdext.LocalErrorCategoryUser,
					Suggestion: "Install Git so azd can fetch the managed Loom recipe.",
				}
			}
			rleSdkPackage, err := resolveRleSdkPackage()
			if err != nil {
				return err
			}
			loomPath, err := resolveInvokeLoomPath(cmd, recipeName, rleSdkPackage)
			if err != nil {
				return err
			}

			controlPlane := resolveControlPlaneEndpoint("")
			invokeArgs := buildLoomInvokeArgs(state, state.Project, controlPlane, flags)
			commandArgs := append([]string{"run", "--extra", "code_rl", "python", "-m", loomRecipeModule(recipeName)}, invokeArgs...)

			if _, err := exec.LookPath("uv"); err != nil {
				return &azdext.LocalError{
					Message:    "Could not find \"uv\" on PATH.",
					Code:       "rle_uv_not_found",
					Category:   azdext.LocalErrorCategoryUser,
					Suggestion: "Install uv and try again.",
				}
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Running Loom RLE training for environment %s\n", state.EnvironmentId); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Working directory: %s\n", loomPath); err != nil {
				return err
			}

			process := exec.CommandContext(cmd.Context(), "uv", commandArgs...) //nolint:gosec // Arguments are user-selected CLI parameters.
			process.Dir = loomPath
			process.Stdout = cmd.OutOrStdout()
			process.Stderr = cmd.ErrOrStderr()
			process.Stdin = os.Stdin
			process.Env = os.Environ()
			if err := process.Run(); err != nil {
				return fmt.Errorf("run Loom RLE training: %w", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&flags.recipe, "recipe", flags.recipe, "Loom cookbook recipe to fetch and run.")
	cmd.Flags().StringVar(&flags.projectEndpoint, "project-endpoint", flags.projectEndpoint, "Azure AI Foundry project endpoint.")
	cmd.Flags().IntVar(&flags.numTasks, "num-tasks", flags.numTasks, "Number of RLE tasks/seeds to train over.")
	cmd.Flags().StringVar(&flags.modelName, "model-name", flags.modelName, "Training model name.")
	return cmd
}

func buildLoomInvokeArgs(state rleState, project string, controlPlane string, flags *rleInvokeFlags) []string {
	args := []string{
		"project_endpoint=" + flags.projectEndpoint,
		"env_id=" + state.EnvironmentId,
		"project=" + project,
		"control_plane=" + controlPlane,
		"num_tasks=" + strconv.Itoa(flags.numTasks),
		"model_name=" + flags.modelName,
		"renderer_name=" + flags.rendererName,
		"max_tokens=" + strconv.Itoa(flags.maxTokens),
		"lora_rank=" + strconv.Itoa(flags.loraRank),
		"group_size=" + strconv.Itoa(flags.groupSize),
		"groups_per_batch=" + strconv.Itoa(flags.groupsPerBatch),
		"loss_fn=" + flags.lossFn,
		"seed=" + strconv.Itoa(flags.seed),
		"eval_every=" + strconv.Itoa(flags.evalEvery),
		"save_every=" + strconv.Itoa(flags.saveEvery),
		"remove_constant_reward_groups=" + strconv.FormatBool(flags.removeConstantRewardGroups),
	}
	if flags.maxSteps > 0 {
		args = append(args, "max_steps="+strconv.Itoa(flags.maxSteps))
	}
	return args
}

func resolveInvokeLoomPath(cmd *cobra.Command, recipeName string, rleSdkPackage string) (string, error) {
	return ensureManagedLoomRecipe(cmd, ".", defaultLoomRecipeRepo, defaultLoomRecipeRef, recipeName, rleSdkPackage)
}

func resolveLoomPath(path string) (string, error) {
	if path == "" {
		return "", &azdext.LocalError{
			Message:    "Loom cookbook path is required.",
			Code:       "rle_loom_path_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Run azd ai rle invoke again so azd can fetch the managed Loom recipe.",
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("Loom cookbook path %q could not be read.", abs),
			Code:       "rle_loom_path_missing",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Delete the managed checkout, then run azd ai rle invoke again.",
		}
	}
	if !info.IsDir() {
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("Loom cookbook path %q is not a directory.", abs),
			Code:       "rle_loom_path_not_directory",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Delete the managed checkout, then run azd ai rle invoke again.",
		}
	}
	return abs, nil
}

func resolveRleSdkPackage() (string, error) {
	if _, err := os.Stat(bundledRleSdkPath(".")); err != nil {
		return materializeBundledRleSdk(".")
	}
	return filepath.Abs(bundledRleSdkPath("."))
}

func ensureManagedLoomRecipe(cmd *cobra.Command, sessionDir string, repo string, ref string, recipeName string, rleSdkPackage string) (string, error) {
	repoDir := managedLoomRepoPath(sessionDir)
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err == nil {
		return prepareManagedLoomCookbook(sessionDir, recipeName, rleSdkPackage)
	}

	if _, err := os.Stat(repoDir); err == nil {
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("Managed Loom recipe path %q exists but is not a Git checkout.", repoDir),
			Code:       "rle_recipe_path_invalid",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Delete the path, then run azd ai rle init again.",
		}
	}

	if err := os.MkdirAll(filepath.Dir(repoDir), 0700); err != nil {
		return "", err
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Fetching Loom recipe %s from %s\n", ref, repo); err != nil {
		return "", err
	}
	if err := runGit(cmd, "", "clone", "--depth", "1", "--no-tags", "--branch", ref, "--single-branch", repo, repoDir); err != nil {
		return "", err
	}
	return prepareManagedLoomCookbook(sessionDir, recipeName, rleSdkPackage)
}

func runGit(cmd *cobra.Command, dir string, args ...string) error {
	process := exec.CommandContext(cmd.Context(), "git", args...) //nolint:gosec // Arguments are fixed command shapes or user-selected repo/ref.
	process.Dir = dir
	process.Stdout = cmd.OutOrStdout()
	process.Stderr = cmd.ErrOrStderr()
	process.Stdin = os.Stdin
	process.Env = os.Environ()
	if err := process.Run(); err != nil {
		return fmt.Errorf("run git %s: %w", joinCommandArgs(args), err)
	}
	return nil
}

func managedLoomRepoPath(sessionDir string) string {
	return filepath.Join(sessionDir, rleManagedDir, "recipes", "loom")
}

func managedLoomCookbookPath(sessionDir string) string {
	return filepath.Join(managedLoomRepoPath(sessionDir), "loom-cookbook")
}

func prepareManagedLoomCookbook(sessionDir string, recipeName string, rleSdkPackage string) (string, error) {
	cookbookPath, err := resolveLoomPath(managedLoomCookbookPath(sessionDir))
	if err != nil {
		return "", err
	}
	if err := ensureLoomRecipeExists(cookbookPath, recipeName); err != nil {
		return "", err
	}
	return cookbookPath, ensureLoomLocalSources(cookbookPath, rleSdkPackage)
}

func ensureLoomRecipeExists(cookbookPath string, recipeName string) error {
	recipeEntrypoint := filepath.Join(cookbookPath, "loom_cookbook", "recipes", recipeName, "train_azure.py")
	if _, err := os.Stat(recipeEntrypoint); err != nil {
		return &azdext.LocalError{
			Message:    fmt.Sprintf("Loom recipe %q does not have a train_azure.py entrypoint.", recipeName),
			Code:       "rle_loom_recipe_missing",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Choose a Loom cookbook recipe that supports Azure/RLE training.",
		}
	}
	return nil
}

func loomRecipeModule(recipeName string) string {
	return fmt.Sprintf("loom_cookbook.recipes.%s.train_azure", recipeName)
}

func ensureLoomLocalSources(cookbookPath string, rleSdkPackage string) error {
	pyprojectPath := filepath.Join(cookbookPath, "pyproject.toml")
	data, err := os.ReadFile(pyprojectPath)
	if err != nil {
		return err
	}
	contents := string(data)
	lines := []string{}
	if !strings.Contains(contents, "[tool.uv.sources]") {
		lines = append(lines, "[tool.uv.sources]")
	}
	if !strings.Contains(contents, "azure-ai-finetuning-sessions = { path = \"../azure-ai-finetuning-sessions\" }") {
		lines = append(lines, "azure-ai-finetuning-sessions = { path = \"../azure-ai-finetuning-sessions\" }")
	}
	if !strings.Contains(contents, "rle-sdk = { path = ") {
		relativeRleSdkPackage, err := filepath.Rel(cookbookPath, rleSdkPackage)
		if err != nil {
			return err
		}
		lines = append(lines, fmt.Sprintf("rle-sdk = { path = %q }", filepath.ToSlash(relativeRleSdkPackage)))
	}
	if len(lines) == 0 {
		return nil
	}

	contents = strings.TrimRight(contents, "\r\n") + "\n\n" + strings.Join(lines, "\n") + "\n"
	return os.WriteFile(pyprojectPath, []byte(contents), 0600)
}

func joinCommandArgs(args []string) string {
	result := ""
	for i, arg := range args {
		if i > 0 {
			result += " "
		}
		result += strconv.Quote(arg)
	}
	return result
}
