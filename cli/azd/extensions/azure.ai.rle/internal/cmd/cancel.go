// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newCancelCommand(outputFormat *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel <job-id>",
		Short: "Cancel an RLE-backed fine-tuning job (experimental)",
		Long: `Cancel an RLE-backed fine-tuning job (experimental).

The command calls the Foundry Azure OpenAI fine-tuning cancel API for the
high-level training job, not an individual RLE rollout or Loom session.
Use the job ID returned by train or jobs. Sign-in and FOUNDRY_PROJECT_ENDPOINT
are required; no local source folder or running monitor is needed.

The fine-tuning endpoint is the Azure OpenAI account endpoint derived from
FOUNDRY_PROJECT_ENDPOINT, exactly as for train. The command prints the returned job status and exits
without polling. It does not delete saved artifacts or stop a local monitor.
Use --output json to return the job as JSON.`,
		Example: `  azd ai rle jobs cancel ftjob-7898670d3ae8453e89e591f9
  azd ai rle jobs cancel ftjob-7898670d3ae8453e89e591f9 --output json`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 || (len(args) == 1 && strings.TrimSpace(args[0]) == "") {
				return &azdext.LocalError{
					Message: "A job ID is required to cancel a training job.",
					Code:    "rle_cancel_job_id_required", Category: azdext.LocalErrorCategoryUser,
					Suggestion: "Run azd ai rle jobs cancel <job-id> using the ID returned by azd ai rle train or jobs.",
				}
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID := strings.TrimSpace(args[0])
			if err := validateRleJobID(jobID); err != nil {
				return err
			}
			format, err := azdext.ParseOutputFormat(*outputFormat)
			if err != nil {
				return err
			}
			projectEndpoint, err := resolveFoundryProjectEndpoint()
			if err != nil {
				return err
			}
			if projectEndpoint == "" {
				return &azdext.LocalError{
					Message: "Foundry project endpoint is required to cancel a training job.",
					Code:    "rle_project_required", Category: azdext.LocalErrorCategoryUser,
					Suggestion: fmt.Sprintf(
						"Set %s=https://<account>.services.ai.azure.com/api/projects/<project>.",
						foundryProjectEndpointEnvVar,
					),
				}
			}
			finetuneEndpoint, err := finetuneEndpointFromFoundryProject(projectEndpoint)
			if err != nil {
				return err
			}
			azureAIProject, err := projectRouteSegment(projectEndpoint)
			if err != nil {
				return err
			}
			client, err := createFinetuneClient(finetuneEndpoint)
			if err != nil {
				return err
			}
			job, err := client.cancelJob(cmd.Context(), jobID, azureAIProject)
			if err != nil {
				return finetuneCancelServiceError(err)
			}
			output := azdext.NewOutput(azdext.OutputOptions{
				Format: format, Writer: cmd.OutOrStdout(), ErrWriter: cmd.ErrOrStderr(),
			})
			if output.IsJSON() {
				return output.JSON(job)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(),
				"Cancellation requested for fine-tuning job %s (status=%s).\n", job.Id, job.Status)
			return err
		},
	}
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"default", "json"},
	})
	return cmd
}
