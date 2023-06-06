package cmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/progress"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/sample"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type sampleFlags struct {
	global *internal.GlobalCommandOptions
}

func (v *sampleFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	v.global = global
}

func newSampleFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *sampleFlags {
	flags := &sampleFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newSampleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sample",
		Short: "Pubsub sample",
	}
}

type sampleAction struct {
	flags           *sampleFlags
	projectConfig   *project.ProjectConfig
	sampler         *sample.Sampler
	progressPrinter *progress.Printer
	console         input.Console
	formatter       output.Formatter
	writer          io.Writer
}

func newSampleAction(
	flags *sampleFlags,
	projectConfig *project.ProjectConfig,
	sampler *sample.Sampler,
	projectPrinter *progress.Printer,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,

) actions.Action {
	return &sampleAction{
		flags:           flags,
		projectConfig:   projectConfig,
		sampler:         sampler,
		progressPrinter: projectPrinter,
		console:         console,
		formatter:       formatter,
		writer:          writer,
	}
}

func (sa *sampleAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// Command title
	sa.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Sampling services (azd sample)",
	})

	// Start progress reporting
	// This could also be moved to a middleware to handle commonly used progress messages
	subscription, err := sa.progressPrinter.Start(ctx)
	if err != nil {
		return nil, err
	}
	defer subscription.Close(ctx)

	startTime := time.Now()

	// Start your long running / blocking operation
	_, err = sa.sampler.LongRunningOperation(ctx)
	if err != nil {
		return nil, err
	}

	// Ensure all messages are flushed and handled before writing out success message
	if err := subscription.Flush(ctx); err != nil {
		return nil, err
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf("Your application was sampled for Azure in %s.", ux.DurationAsText(since(startTime))),
		},
	}, nil
}
