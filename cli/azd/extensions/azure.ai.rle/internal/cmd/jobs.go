// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

const (
	finetuneJobsPageSize = 100
	finetuneJobsMaxPages = 100
	noRleJobsMessage     = "No RLE fine-tuning jobs found."
)

type jobsAction struct {
	cmd          *cobra.Command
	endpoint     string
	outputFormat *string
}

func newJobsCommand(outputFormat *string) *cobra.Command {
	action := &jobsAction{outputFormat: outputFormat}
	cmd := &cobra.Command{
		Use:   "jobs",
		Short: "List RLE-backed fine-tuning jobs (experimental)",
		Long: `List RLE-backed fine-tuning jobs (experimental).

The command retrieves fine-tuning jobs from the Azure OpenAI resource derived from
FOUNDRY_PROJECT_ENDPOINT and returns only jobs that use the rl_environment method.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			action.cmd = cmd
			return action.Run()
		},
	}
	cmd.Flags().StringVar(
		&action.endpoint,
		"endpoint",
		"",
		fmt.Sprintf("Fine-tuning API endpoint. Defaults to the account in %s.", foundryProjectEndpointEnvVar),
	)
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"default", "json"},
	})
	return cmd
}

func (a *jobsAction) Run() error {
	projectEndpoint, err := resolveJobsProjectEndpoint()
	if err != nil {
		return err
	}
	endpoint, err := resolveFinetuneEndpoint(a.endpoint, projectEndpoint)
	if err != nil {
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
	jobs, err := listAllRleJobs(a.cmd.Context(), client, azureAIProject)
	if err != nil {
		return err
	}

	format, err := azdext.ParseOutputFormat(*a.outputFormat)
	if err != nil {
		return err
	}
	output := azdext.NewOutput(azdext.OutputOptions{
		Format:    format,
		Writer:    a.cmd.OutOrStdout(),
		ErrWriter: a.cmd.ErrOrStderr(),
	})
	if output.IsJSON() {
		return output.JSON(jobs)
	}

	rows := make([][]string, 0, len(jobs))
	for _, job := range jobs {
		rows = append(rows, []string{
			job.Id,
			job.Status,
			job.Model,
			job.Method.RleEnvironment.Name,
			job.Method.RleEnvironment.Version,
		})
	}
	renderTableOrNoResults(
		output,
		[]string{"JOB ID", "STATUS", "MODEL", "RLE", "VERSION"},
		rows,
		noRleJobsMessage,
	)
	return nil
}

func listAllRleJobs(
	ctx context.Context,
	client *finetuneClient,
	azureAIProject string,
) ([]finetuneJobResource, error) {
	jobs := make([]finetuneJobResource, 0)
	after := ""
	seenCursors := map[string]struct{}{}
	for range finetuneJobsMaxPages {
		page, err := client.listRleJobs(ctx, after, finetuneJobsPageSize, azureAIProject)
		if err != nil {
			return nil, finetuneServiceError(err)
		}
		for _, job := range page.Data {
			if job.isRleEnvironment() {
				jobs = append(jobs, job)
			}
		}
		if !page.HasMore {
			return jobs, nil
		}
		if len(page.Data) == 0 {
			return nil, invalidJobsPaginationCursorError()
		}
		after, err = nextPaginationCursor(seenCursors, page.Data[len(page.Data)-1].Id, invalidJobsPaginationCursorError)
		if err != nil {
			return nil, err
		}
	}
	return nil, paginationSafetyLimitError("RLE job list", "rle_job_list_safety_limit")
}

func resolveJobsProjectEndpoint() (string, error) {
	endpoint, err := resolveFoundryProjectEndpoint()
	if err != nil {
		return "", err
	}
	if endpoint != "" {
		return endpoint, nil
	}

	return "", &azdext.LocalError{
		Message:  "Foundry project endpoint is required to list RLE fine-tuning jobs.",
		Code:     "rle_project_required",
		Category: azdext.LocalErrorCategoryUser,
		Suggestion: fmt.Sprintf(
			"Set %s=https://<account>.services.ai.azure.com/api/projects/<project>.",
			foundryProjectEndpointEnvVar,
		),
	}
}

func invalidJobsPaginationCursorError() error {
	return &azdext.LocalError{
		Message:  "RLE job list pagination did not return a new job identifier.",
		Code:     "rle_job_list_cursor_invalid",
		Category: azdext.LocalErrorCategoryInternal,
	}
}

func (job finetuneJobResource) isRleEnvironment() bool {
	return job.Method != nil &&
		job.Method.RleEnvironment != nil &&
		strings.EqualFold(strings.TrimSpace(job.Method.Type), finetuneMethodTypeRleEnvironment)
}
