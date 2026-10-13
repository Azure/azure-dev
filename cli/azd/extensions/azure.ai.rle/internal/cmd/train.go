// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
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
	logsRoot       string
	noBrowser      bool
	taskCount      int
	endpoint       string

	// Where the merged settings came from, for the line printed before submitting.
	optionsSource string
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

By default train also streams the run's logs and metrics to a local mirror
(--logs-root), in the layout the Loom dashboard reads, and opens a local
monitor that stays running until Ctrl+C (including in non-interactive runs).
The monitor reads RLE APIs into memory; no training files are mirrored beyond
that stream. Ctrl+C stops monitoring, not the remote job. Use --no-browser to
open the link manually.

Pass --no-follow to skip both: the command submits the job, prints the
azd ai rle monitor command that reopens it later, and exits immediately
instead of blocking.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&trainAction{cmd: cmd, flags: flags}).Run()
		},
	}

	cmd.Flags().StringVar(&flags.model, "model", "", "Base model id to fine-tune (required).")
	cmd.Flags().StringVar(&flags.trainingFile, "training-file", "",
		"Path to the local training dataset uploaded as the Loom job input (required).")
	cmd.Flags().StringVar(&flags.validationFile, "validation-file", "",
		"Path to a local validation dataset to upload.")
	cmd.Flags().StringVar(&flags.suffix, "suffix", "",
		"Suffix appended to the resulting fine-tuned model name.")
	cmd.Flags().BoolVar(&flags.noFollow, "no-follow", false,
		"Skip the local log/metric mirror and the job monitor. Submit the job, print how "+
			"to reopen its monitor later, and exit immediately instead of blocking until Ctrl+C.")
	cmd.Flags().StringVar(&flags.logsRoot, "logs-root", "",
		"Where the local log/metric mirror is written. Defaults to $LOOM_LOGS_ROOT, else "+
			"~/loom-runs. Ignored with --no-follow.")
	cmd.Flags().BoolVar(&flags.noBrowser, "no-browser", false,
		"Print the job monitor address without opening a browser.")
	cmd.Flags().IntVar(&flags.taskCount, "task-count", 0,
		"Train on only the first N tasks of the training dataset, for a smaller run. "+
			"Sets the max_train_examples training option (0 uses the whole dataset).")
	cmd.Flags().StringVar(&flags.endpoint, "endpoint", "",
		fmt.Sprintf("Fine-tuning API endpoint. Defaults to $%s, else the Foundry account endpoint.", rleTrainEndpointEnvVar))

	// model and training-file are not marked required: rle.toml can supply either,
	// and cobra would reject the run before the manifest is ever read.

	return cmd
}

// resolveTrainSettings reads the RLE name and version from rle.toml in the
// current folder, and flattens the manifest's
// [train.grpo] defaults into the hyperparameters map sent to finetunesapi.
// model, training-file, validation-file and suffix come only from their
// flags: [train] is a reusable default for the published environment, not a
// local-only training invocation.
//
// rle.toml must exist in the current folder; it is the only source of the name and version.
func (a *trainAction) resolveTrainSettings() (map[string]any, error) {
	model := strings.TrimSpace(a.flags.model)
	trainingFile := strings.TrimSpace(a.flags.trainingFile)
	validationFile := strings.TrimSpace(a.flags.validationFile)
	suffix := strings.TrimSpace(a.flags.suffix)

	options := map[string]any{}
	config, err := project.LoadRleConfig(".")
	if err != nil {
		return nil, err
	}
	name := config.Rle.Name
	version := config.Rle.Version
	options = trainDefaultsOptions(config.Train)
	if len(options) > 0 {
		a.flags.optionsSource = project.RleConfigFile
	}

	// --task-count is shorthand for one training option, so it is applied the way
	// any other flag overrides the manifest rather than being sent separately.
	if a.flags.taskCount > 0 {
		options[trainTaskCountOption] = a.flags.taskCount
		if a.flags.optionsSource == "" {
			a.flags.optionsSource = "--task-count"
		} else {
			a.flags.optionsSource = project.RleConfigFile + " and --task-count"
		}
	}

	if model == "" {
		return nil, missingTrainSettingError("model", "--model")
	}
	if trainingFile == "" {
		return nil, missingTrainSettingError("training file", "--training-file")
	}

	normalized, err := project.NormalizeRleVersion(version)
	if err != nil {
		return nil, err
	}
	a.flags.rleName = name
	a.flags.rleVersion = normalized
	a.flags.model = model
	a.flags.trainingFile = trainingFile
	a.flags.validationFile = validationFile
	a.flags.suffix = suffix
	return options, nil
}

// trainDefaultsOptions flattens the manifest's [train.grpo] defaults into the
// hyperparameters map, field by field, so each entry's wire key stays obviously
// identical to its rle.toml key. Rollout is deliberately excluded: its only field,
// GymOpenEnv, is part of the rollout contract the environment already knows, not a
// training hyperparameter.
func trainDefaultsOptions(train *project.RleTrainDefaults) map[string]any {
	options := map[string]any{}
	if train == nil || train.Grpo == nil {
		return options
	}

	grpo := train.Grpo
	if grpo.LearningRate != nil {
		options["learning_rate"] = *grpo.LearningRate
	}
	if grpo.GroupSize != nil {
		options["group_size"] = *grpo.GroupSize
	}
	if grpo.GroupsPerBatch != nil {
		options["groups_per_batch"] = *grpo.GroupsPerBatch
	}
	if grpo.LoraRank != nil {
		options["lora_rank"] = *grpo.LoraRank
	}
	if grpo.NumEpochs != nil {
		options["num_epochs"] = *grpo.NumEpochs
	}
	if grpo.MaxSteps != nil {
		options["max_steps"] = *grpo.MaxSteps
	}
	if grpo.EvalEvery != nil {
		options["eval_every"] = *grpo.EvalEvery
	}
	if grpo.SaveEvery != nil {
		options["save_every"] = *grpo.SaveEvery
	}
	if grpo.RemoveConstantRewardGroups != nil {
		options["remove_constant_reward_groups"] = *grpo.RemoveConstantRewardGroups
	}
	if grpo.SamplingSeed != nil {
		options["sampling_seed"] = *grpo.SamplingSeed
	}
	if grpo.MinTrainableGroupMembers != nil {
		options["min_trainable_group_members"] = *grpo.MinTrainableGroupMembers
	}

	return options
}

// trainTaskCountOption is the training option --task-count sets. The service
// applies it as the dataset limit for the run; the CLI only forwards it.
const trainTaskCountOption = "max_train_examples"

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
	trainingOptions, err := a.resolveTrainSettings()
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
	endpoint, err := resolveFinetuneEndpoint(a.flags.endpoint, projectEndpoint)
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

	request := buildFinetuneJobRequest(a.flags, trainingFileID, validationFileID, trainingOptions)

	if _, err := fmt.Fprintf(
		a.cmd.OutOrStdout(),
		"Submitting rl_environment fine-tuning job for RLE '%s' version %s (model=%s) ...\n",
		a.flags.rleName,
		a.flags.rleVersion,
		a.flags.model,
	); err != nil {
		return err
	}

	if len(trainingOptions) > 0 {
		names := make([]string, 0, len(trainingOptions))
		for name := range trainingOptions {
			names = append(names, name)
		}
		sort.Strings(names)
		if _, err := fmt.Fprintf(
			a.cmd.OutOrStdout(),
			"Applying %d training option(s) from %s: %s\n",
			len(names),
			a.flags.optionsSource,
			strings.Join(names, ", "),
		); err != nil {
			return err
		}
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
			"--no-follow was set; exiting without streaming logs or opening the job monitor.",
		)
		return err
	}

	ctx, stop := signal.NotifyContext(a.cmd.Context(), os.Interrupt)
	defer stop()
	return a.followJob(ctx, client, projectEndpoint, job.Id)
}

// followJob mirrors the run's artifacts locally and serves its rollouts, until
// the run finishes and the user stops watching.
//
// A streaming failure is reported but does not fail the command: the job was
// accepted and is running on the service, and exiting non-zero would suggest it
// was not. The job id is printed above, so it stays recoverable.
func (a *trainAction) followJob(
	ctx context.Context, client *finetuneClient, projectEndpoint string, jobID string,
) error {
	authorization, err := client.authorizationHeader(ctx)
	if err != nil {
		return fmt.Errorf("acquire a token for the run stream: %w", err)
	}

	logsRoot := strings.TrimSpace(a.flags.logsRoot)
	if logsRoot == "" {
		logsRoot = defaultLogsRoot()
	}

	// The dashboard runs alongside the stream rather than after it. A run
	// records rollouts for as long as it lasts, and the point of following one
	// is to watch them land, not to read them once it is over. It reads RLE
	// APIs directly, so it does not depend on the local mirror the stream below
	// writes -- following a run fetches its artifacts once, not twice.
	dashboard := make(chan struct{})
	go func() {
		defer close(dashboard)
		if err := runRealJobMonitor(ctx, a.cmd, projectEndpoint, jobID, a.flags.noBrowser); err != nil {
			// The stream is the part that must keep working; a dashboard that
			// cannot start is worth saying once and no more.
			fmt.Fprintf(a.cmd.ErrOrStderr(), "The job monitor did not start: %v\n", err)
		}
	}()

	status, streamErr := followTrainingRunFunc(
		ctx,
		client.baseUrl,
		authorization,
		jobID,
		logsRoot,
		a.cmd.OutOrStdout(),
	)
	if streamErr != nil {
		fmt.Fprintf(a.cmd.ErrOrStderr(),
			"\nStopped following %s: %v\nThe job is still running on the service; "+
				"check it with: azd ai rle jobs\n", jobID, streamErr)
	} else if status != "" {
		fmt.Fprintf(a.cmd.OutOrStdout(), "Final status: %s\n", status)
	}

	// The run is over but its rollouts are not read yet. Hold the dashboard open
	// until the user stops it, rather than closing the window they were sent to.
	fmt.Fprintf(a.cmd.OutOrStdout(),
		"\nThe job monitor is still running. Press Ctrl+C to stop it.\n")
	<-dashboard
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
	options map[string]any,
) finetuneJobCreationRequest {
	rleEnvironment := finetuneRleEnvironmentConfig{
		Name:    flags.rleName,
		Version: flags.rleVersion,
	}
	if len(options) > 0 {
		rleEnvironment.Hyperparameters = options
	}

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
