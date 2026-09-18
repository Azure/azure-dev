// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type rleTrainFlags struct {
	rleName         string
	rleVersion      string
	model           string
	trainingFile    string
	validationFile  string
	suffix          string
	maxEpisodeSteps int
	endpoint        string
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
environment supplies the reward signal instead of a grader. A training file is still
required because Loom mounts it as the job input. rl_environment is currently hidden from
finetunesapi's public API surface and only completes for base models enabled for Loom-backed
RL-environment training. Job creation fails if the base model is not enabled, or if the RLE
version is not published and ready in the project set by FOUNDRY_PROJECT_ENDPOINT.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&trainAction{cmd: cmd, flags: flags}).Run()
		},
	}

	cmd.Flags().StringVar(&flags.rleName, "rle-name", "", "Name of the published RLE environment to train against.")
	cmd.Flags().StringVar(&flags.rleVersion, "rle-version", "", "Version of the published RLE environment.")
	cmd.Flags().StringVar(&flags.model, "model", "", "Base model id to fine-tune.")
	cmd.Flags().StringVar(&flags.trainingFile, "training-file", "",
		"File id of the uploaded training dataset used as the Loom job input.")
	cmd.Flags().StringVar(&flags.validationFile, "validation-file", "",
		"File id of an uploaded validation dataset.")
	cmd.Flags().StringVar(&flags.suffix, "suffix", "", "Suffix appended to the resulting fine-tuned model name.")
	cmd.Flags().IntVar(&flags.maxEpisodeSteps, "max-episode-steps", 0,
		"Maximum steps the RLE executes per rollout (0 uses the service default).")
	cmd.Flags().StringVar(&flags.endpoint, "endpoint", "",
		fmt.Sprintf("Fine-tuning API endpoint. Defaults to %s.", finetuneEndpointEnvVar))

	for _, name := range []string{"rle-name", "rle-version", "model", "training-file"} {
		_ = cmd.MarkFlagRequired(name)
	}

	return cmd
}

func (a *trainAction) Run() error {
	trainingFile, err := resolveTrainingFile(a.flags.trainingFile)
	if err != nil {
		return err
	}
	a.flags.trainingFile = trainingFile

	endpoint, err := resolveFinetuneEndpoint(a.flags.endpoint)
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
	azureAIProject, err := projectRouteSegment(projectEndpoint)
	if err != nil {
		return err
	}

	client, err := createFinetuneClient(endpoint)
	if err != nil {
		return err
	}

	request := buildFinetuneJobRequest(a.flags)

	if _, err := fmt.Fprintf(
		a.cmd.OutOrStdout(),
		"Submitting rl_environment fine-tuning job for RLE '%s' version %s (model=%s) ...\n",
		a.flags.rleName,
		a.flags.rleVersion,
		a.flags.model,
	); err != nil {
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
	return nil
}

func resolveTrainingFile(raw string) (string, error) {
	trainingFile := strings.TrimSpace(raw)
	if trainingFile == "" {
		return "", &azdext.LocalError{
			Message:    "A non-empty training file ID is required for train.",
			Code:       "rle_train_training_file_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Upload a training file to the fine-tuning resource, then pass its file-... ID using --training-file.",
		}
	}
	if !strings.HasPrefix(trainingFile, "file-") {
		return "", &azdext.LocalError{
			Message:    "The training file must be a file-... ID.",
			Code:       "rle_invalid_training_file",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Use the ID of a training file uploaded to the fine-tuning resource.",
		}
	}
	return trainingFile, nil
}

func buildFinetuneJobRequest(flags *rleTrainFlags) finetuneJobCreationRequest {
	rleEnvironment := finetuneRleEnvironmentConfig{
		Name:    flags.rleName,
		Version: flags.rleVersion,
	}
	if flags.maxEpisodeSteps > 0 {
		steps := flags.maxEpisodeSteps
		rleEnvironment.MaxEpisodeSteps = &steps
	}

	request := finetuneJobCreationRequest{
		Model:        flags.model,
		TrainingFile: flags.trainingFile,
		TrainingType: finetuneTrainingTypeGlobalStandard,
		Method: &finetuneMethodRequest{
			Type:           finetuneMethodTypeRleEnvironment,
			RleEnvironment: rleEnvironment,
		},
	}
	if flags.validationFile != "" {
		request.ValidationFile = &flags.validationFile
	}
	if flags.suffix != "" {
		request.Suffix = &flags.suffix
	}
	return request
}
