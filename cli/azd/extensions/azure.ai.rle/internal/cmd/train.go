// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"azure.ai.rle/internal/project"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type rleTrainFlags struct {
	rleName        string
	rleVersion     string
	model          string
	trainingFile   string
	validationFile string
	suffix         string
	noFollow       bool
}

type trainAction struct {
	cmd   *cobra.Command
	flags *rleTrainFlags
}

// newTrainCommand submits a reinforcement fine-tuning job that uses a published RLE
// environment as the reward source (finetunesapi method type rl_environment) instead of
// a grader. This method is currently hidden from finetunesapi's public Swagger surface
// and only completes for base models the service has enabled for Loom-backed
// RL-environment training, so it is gated behind AZD_AI_RLE_ENABLE_ALL for the RLE
// team's own iteration.
func newTrainCommand() *cobra.Command {
	flags := &rleTrainFlags{}

	cmd := &cobra.Command{
		Use:   "train",
		Short: "Submit an RLE-backed reinforcement fine-tuning job (experimental)",
		Long: `Submit an RLE-backed reinforcement fine-tuning job (experimental).

This uses finetunesapi's rl_environment fine-tuning method: the named, published RLE
environment supplies the reward signal instead of a grader. The command uploads the local
training file to the fine-tuning resource before Loom mounts it as the job input. rl_environment
is currently hidden from finetunesapi's public API surface and only completes for base models
enabled for Loom-backed RL-environment training. Job creation fails if the base model is not
enabled, or if the RLE version is not published and ready in the project set by
FOUNDRY_PROJECT_ENDPOINT.

The current folder's rle.toml must explicitly supply schema_version, [harness],
[model].renderer_name, and all required [train.grpo] settings. No registered
environment or recipe defaults are fetched or filled in. Optional [rollout]
settings are sent beside train; when omitted, rollout is an empty object.
--model selects the training model independently of [model].name.

By default train opens a local monitor that stays running until Ctrl+C
(including in non-interactive runs). Job status comes from the fine-tuning API;
metrics, logs and rollout data are fetched from the RLE backend into memory.
No artifact stream or local disk mirror is used. Ctrl+C stops local monitoring,
not the remote job.

Pass --no-follow to skip monitoring: the command submits the job, prints the
azd ai rle monitor command that reopens it later, and exits immediately
instead of blocking.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&trainAction{cmd: cmd, flags: flags}).Run()
		},
	}

	cmd.Flags().StringVar(&flags.model, "model", "",
		"Base model id to fine-tune (required). Run `azd ai rle models` to list supported values.")
	cmd.Flags().StringVar(&flags.trainingFile, "training-file", "",
		"Path to the local training dataset uploaded as the Loom job input (required).")
	cmd.Flags().StringVar(&flags.validationFile, "validation-file", "",
		"Path to a local validation dataset to upload.")
	cmd.Flags().StringVar(&flags.suffix, "suffix", "",
		"Suffix appended to the resulting fine-tuned model name.")
	cmd.Flags().BoolVar(&flags.noFollow, "no-follow", false,
		"Skip the job monitor. Submit the job, print how "+
			"to reopen its monitor later, and exit immediately instead of blocking until Ctrl+C.")

	return cmd
}

// resolveTrainSettings resolves the local snapshot before any authentication, upload,
// or FT submission. Invocation flags remain independent of the registration manifest.
func (a *trainAction) resolveTrainSettings() (finetuneRleEnvironmentConfig, error) {
	model := strings.TrimSpace(a.flags.model)
	trainingFile := strings.TrimSpace(a.flags.trainingFile)
	validationFile := strings.TrimSpace(a.flags.validationFile)
	suffix := strings.TrimSpace(a.flags.suffix)

	config, err := project.LoadRleConfig(".")
	if err != nil {
		return finetuneRleEnvironmentConfig{}, err
	}

	if model == "" {
		return finetuneRleEnvironmentConfig{}, missingTrainSettingError("model", "--model")
	}
	if trainingFile == "" {
		return finetuneRleEnvironmentConfig{}, missingTrainSettingError("training file", "--training-file")
	}

	resolved, err := resolveFinetuneRleConfig(config)
	if err != nil {
		return finetuneRleEnvironmentConfig{}, err
	}
	a.flags.rleName = config.Rle.Name
	a.flags.rleVersion = config.Rle.Version
	a.flags.model = model
	a.flags.trainingFile = trainingFile
	a.flags.validationFile = validationFile
	a.flags.suffix = suffix
	return resolved, nil
}

func resolveFinetuneRleConfig(config project.RleConfig) (finetuneRleEnvironmentConfig, error) {
	grpo := &project.RleGrpoDefaults{}
	if config.Train != nil && config.Train.Grpo != nil {
		grpo = config.Train.Grpo
	}
	var missing []string
	if config.SchemaVersion == nil {
		missing = append(missing, "root-level schema_version")
	}
	if config.Model == nil || config.Model.RendererName == nil {
		missing = append(missing, "[model]: renderer_name")
	}
	var missingGrpo []string
	for _, field := range []struct {
		name    string
		present bool
	}{
		{"learning_rate", grpo.LearningRate != nil},
		{"group_size", grpo.GroupSize != nil},
		{"groups_per_batch", grpo.GroupsPerBatch != nil},
		{"lora_rank", grpo.LoraRank != nil},
		{"num_epochs", grpo.NumEpochs != nil},
		{"eval_every", grpo.EvalEvery != nil},
		{"save_every", grpo.SaveEvery != nil},
		{"remove_constant_reward_groups", grpo.RemoveConstantRewardGroups != nil},
		{"min_trainable_group_members", grpo.MinTrainableGroupMembers != nil},
	} {
		if !field.present {
			missingGrpo = append(missingGrpo, field.name)
		}
	}
	if len(missingGrpo) > 0 {
		missing = append(missing, "[train.grpo]: "+strings.Join(missingGrpo, ", "))
	}
	if len(missing) > 0 {
		return finetuneRleEnvironmentConfig{}, &azdext.LocalError{
			Message:  "rle.toml is missing required training settings: " + strings.Join(missing, "; ") + ".",
			Code:     "rle_train_configuration_required",
			Category: azdext.LocalErrorCategoryUser,
			Suggestion: "Add the listed fields to the indicated TOML sections in rle.toml. " +
				"Training requires explicit values; registered environment and recipe defaults are not used.",
		}
	}

	rollout := project.RleRolloutDefaults{}
	if config.Rollout != nil {
		rollout = *config.Rollout
	}
	if rollout.GymOpenEnv != nil {
		if config.Harness.Type != project.HarnessTypeGym {
			return finetuneRleEnvironmentConfig{}, &azdext.LocalError{
				Message:    "[rollout.gym_openenv] requires a gym harness.",
				Code:       "rle_train_configuration_invalid",
				Category:   azdext.LocalErrorCategoryUser,
				Suggestion: "Remove [rollout.gym_openenv] from rle.toml or set type = \"gym\" in [harness].",
			}
		}
		if rollout.GymOpenEnv.ModelResponseField == nil {
			return finetuneRleEnvironmentConfig{}, &azdext.LocalError{
				Message:  "[rollout.gym_openenv].model_response_field must be a non-empty value.",
				Code:     "rle_train_configuration_invalid",
				Category: azdext.LocalErrorCategoryUser,
				Suggestion: "Set model_response_field in [rollout.gym_openenv] in rle.toml, " +
					"or omit that section if it is not needed.",
			}
		}
	}
	return finetuneRleEnvironmentConfig{
		Name:    config.Rle.Name,
		Version: config.Rle.Version,
		Harness: rleHarness{
			Type:     string(config.Harness.Type),
			Endpoint: config.Harness.Endpoint,
			Protocol: string(config.Harness.Protocol),
		},
		SchemaVersion: *config.SchemaVersion,
		Model:         finetuneRleModelConfig{RendererName: *config.Model.RendererName},
		Train:         *config.Train,
		Rollout:       rollout,
	}, nil
}

func missingTrainSettingError(what string, flag string) error {
	return &azdext.LocalError{
		Message:  fmt.Sprintf("A %s is required for train.", what),
		Code:     "rle_train_setting_required",
		Category: azdext.LocalErrorCategoryUser,
		Suggestion: fmt.Sprintf(
			"Pass %s.", flag,
		),
	}
}

func (a *trainAction) Run() error {
	resolvedConfig, err := a.resolveTrainSettings()
	if err != nil {
		return err
	}

	trainingFilePath, err := resolveLocalFilePath(a.flags.trainingFile, "training", true)
	if err != nil {
		return err
	}
	validationFilePath, err := resolveLocalFilePath(a.flags.validationFile, "validation", false)
	if err != nil {
		return err
	}

	projectEndpoint, err := resolveFoundryProjectEndpoint()
	if err != nil {
		return err
	}
	if projectEndpoint == "" {
		return &azdext.LocalError{
			Message:  "Foundry project endpoint is required for train.",
			Code:     "rle_project_required",
			Category: azdext.LocalErrorCategoryUser,
			Suggestion: fmt.Sprintf(
				"Set %s=https://<account>.services.ai.azure.com/api/projects/<project>.",
				foundryProjectEndpointEnvVar,
			),
		}
	}
	endpoint, err := finetuneEndpointFromFoundryProject(projectEndpoint)
	if err != nil {
		return err
	}
	if err := rejectRealMonitorFlags(a.cmd, "output"); err != nil {
		return err
	}
	azureAIProject, err := projectRouteSegment(projectEndpoint)
	if err != nil {
		return err
	}

	client, err := createFinetuneClient(endpoint)
	if err != nil {
		return err
	}

	trainingFileID, err := a.uploadInputFile(client, trainingFilePath, "training")
	if err != nil {
		return err
	}
	validationFileID := ""
	if validationFilePath != "" {
		validationFileID, err = a.uploadInputFile(client, validationFilePath, "validation")
		if err != nil {
			return err
		}
	}

	request := buildFinetuneJobRequest(a.flags, trainingFileID, validationFileID, resolvedConfig)

	if _, err := fmt.Fprintf(
		a.cmd.OutOrStdout(),
		"Submitting rl_environment fine-tuning job for RLE '%s' version %s (model=%s) ...\n",
		a.flags.rleName,
		a.flags.rleVersion,
		a.flags.model,
	); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(a.cmd.OutOrStdout(),
		"Applying resolved training configuration from rle.toml."); err != nil {
		return err
	}

	job, err := client.createJob(a.cmd.Context(), request, azureAIProject)
	if err != nil {
		return finetuneServiceError(err)
	}

	if _, err := fmt.Fprintf(
		a.cmd.OutOrStdout(),
		"\nSubmitted fine-tuning job %s (status=%s).\n",
		job.Id,
		job.Status,
	); err != nil {
		return err
	}

	body, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(a.cmd.OutOrStdout(), string(body)); err != nil {
		return err
	}

	// Printed either way, so the dashboard this run opened (or skipped) is
	// always reproducible later from just the job id.
	if _, err := fmt.Fprintf(
		a.cmd.OutOrStdout(),
		"\nMonitor this job anytime with: azd ai rle monitor --job-id %s\n",
		job.Id,
	); err != nil {
		return err
	}

	if a.flags.noFollow {
		_, err := fmt.Fprintln(
			a.cmd.OutOrStdout(),
			"--no-follow was set; exiting without opening the job monitor.",
		)
		return err
	}

	ctx, stop := signal.NotifyContext(a.cmd.Context(), os.Interrupt)
	defer stop()
	return a.followJob(ctx, projectEndpoint, job.Id)
}

// followJob monitors the submitted job through the backend APIs without changing
// the remote job's lifecycle.
func (a *trainAction) followJob(
	ctx context.Context, projectEndpoint string, jobID string,
) error {
	err := runRealJobMonitor(ctx, a.cmd, projectEndpoint, jobID, false)
	if ctx.Err() != nil && (err == nil || errors.Is(err, ctx.Err())) {
		_, writeErr := fmt.Fprintf(a.cmd.OutOrStdout(),
			"Stopped local monitoring for job %s. The remote job was not canceled.\n", jobID)
		return writeErr
	}
	if err != nil {
		return fmt.Errorf(
			"fine-tuning job %s was submitted, but local monitoring failed; the remote job was not canceled: %w",
			jobID, err)
	}
	return nil
}

func (a *trainAction) uploadInputFile(client *finetuneClient, filePath string, fileType string) (string, error) {
	if _, err := fmt.Fprintf(a.cmd.OutOrStdout(), "Uploading %s file %q ...\n", fileType, filepath.Base(filePath)); err != nil {
		return "", err
	}

	uploadedFile, err := client.uploadFile(a.cmd.Context(), filePath)
	if err != nil {
		return "", finetuneUploadServiceError(err)
	}

	if _, err := fmt.Fprintf(a.cmd.OutOrStdout(), "Uploaded %s file as %s.\n", fileType, uploadedFile.Id); err != nil {
		return "", err
	}
	if strings.ToLower(strings.TrimSpace(uploadedFile.Status)) != "processed" {
		if _, err := fmt.Fprintf(a.cmd.OutOrStdout(), "Waiting for the %s file import to finish processing ...\n", fileType); err != nil {
			return "", err
		}
	}
	if err := client.waitForFileProcessed(
		a.cmd.Context(),
		uploadedFile,
		finetuneFileImportTimeout,
		finetuneFileImportInitialPollInterval,
	); err != nil {
		return "", finetuneUploadServiceError(err)
	}
	return uploadedFile.Id, nil
}

func resolveLocalFilePath(raw string, fileType string, required bool) (string, error) {
	filePath := strings.TrimSpace(raw)
	if filePath == "" {
		if !required {
			return "", nil
		}
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("A local %s file path is required for train.", fileType),
			Code:       "rle_train_training_file_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: fmt.Sprintf("Pass the path to a local %s dataset using --%s-file.", fileType, fileType),
		}
	}

	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("Unable to access the %s file.", fileType),
			Code:       "rle_train_file_unavailable",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: fmt.Sprintf("Verify that --%s-file points to a readable local file.", fileType),
		}
	}
	if !fileInfo.Mode().IsRegular() {
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("The %s file must be a regular file.", fileType),
			Code:       "rle_train_file_not_regular",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: fmt.Sprintf("Pass the path to a local %s dataset file using --%s-file.", fileType, fileType),
		}
	}
	return filePath, nil
}

func buildFinetuneJobRequest(
	flags *rleTrainFlags,
	trainingFileID string,
	validationFileID string,
	rleEnvironment finetuneRleEnvironmentConfig,
) finetuneJobCreationRequest {
	request := finetuneJobCreationRequest{
		Model:        flags.model,
		TrainingFile: trainingFileID,
		TrainingType: finetuneTrainingTypeGlobalStandard,
		Method: &finetuneMethodRequest{
			Type:           finetuneMethodTypeRleEnvironment,
			RleEnvironment: rleEnvironment,
		},
	}
	if validationFileID != "" {
		request.ValidationFile = &validationFileID
	}
	if flags.suffix != "" {
		request.Suffix = &flags.suffix
	}
	return request
}
