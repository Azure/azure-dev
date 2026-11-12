// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"path/filepath"

	"azure.ai.content-understanding/internal/analysis"
	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type analyzerTestOptions struct {
	service               config.Options
	input                 analysis.InputOptions
	name, pattern, output string
	concurrency           int
	dryRun, force, yes    bool
}

func newAnalyzerTestCommand(extCtx *azdext.ExtensionContext, open sessionFactory) *cobra.Command {
	options := &analyzerTestOptions{}
	cmd := &cobra.Command{
		Use: "test [ANALYZER_NAME] [INPUTS...]", Short: "Report field coverage and confidence over local samples",
		Long: "Run an analyzer over local samples and report extracted-field coverage and confidence.\n\n" +
			"This is not an accuracy benchmark. Dry run works offline without a project, endpoint, or sign-in.",
		Example: `  azd ai cu analyzer test invoice_v1 --file invoice.pdf
    Report extracted-field coverage and confidence for one sample.

  azd ai cu analyzer test invoice_v1 --source ./samples --pattern '*.pdf' --output-file test-report.json
    Test matching PDF samples and save the complete report.

  azd ai cu analyzer test invoice_v1 --source ./samples --recursive --dry-run
    Preview local sample discovery without service calls or writes.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return exterrors.FromError(runAnalyzerTest(cmd, extCtx, open, options, args))
		},
	}
	flags := cmd.Flags()
	flags.StringVarP(&options.name, "name", "n", "", "Analyzer name (-a is also accepted)")
	flags.StringVarP(&options.name, "a", "a", "", "Alias for --name")
	_ = flags.MarkHidden("a")
	flags.StringArrayVar(&options.input.Files, "file", nil, "Local sample file (repeatable)")
	flags.StringArrayVar(&options.input.Sources, "source", nil, "Local sample directory (repeatable)")
	flags.StringVar(&options.pattern, "pattern", "", "Source-relative fnmatch pattern; requires --source")
	flags.BoolVarP(&options.input.Recursive, "recursive", "r", false, "Discover samples recursively")
	flags.BoolVar(&options.dryRun, "dry-run", false,
		"Show local samples without configuration, prompts, service calls, or writes")
	flags.StringVar(&options.output, "output-file", "", "Save the complete structured test report")
	flags.BoolVar(&options.force, "force", false, "Explicitly replace an existing test report; never replace sample inputs")
	flags.BoolVarP(&options.yes, "yes", "y", false, "Confirm billed batches larger than 50 samples without prompting")
	flags.IntVarP(&options.concurrency, "concurrency", "j", 4, "Concurrent sample analyses (1-16)")
	return serviceCommand(cmd, &options.service)
}

func runAnalyzerTest(
	cmd *cobra.Command, extCtx *azdext.ExtensionContext, open sessionFactory, options *analyzerTestOptions, args []string,
) error {
	ctx := cmd.Context()
	if err := ctx.Err(); err != nil {
		return err
	}
	if options.dryRun && options.yes {
		return commandError("dry_run_confirmation",
			"--dry-run cannot be combined with --yes.", "Remove --yes when planning.")
	}
	if options.concurrency < 1 || options.concurrency > 16 {
		return commandError("invalid_test_concurrency", "--concurrency must be between 1 and 16.", "Use -j 1 through -j 16.")
	}
	var nameArgs []string
	if !cmd.Flags().Changed("name") && !cmd.Flags().Changed("a") && len(args) != 0 {
		nameArgs, args = args[:1], args[1:]
	}
	name, err := analyzerName(cmd, nameArgs, options.name)
	if err != nil {
		return err
	}
	for _, flag := range []string{"file", "source", "output-file"} {
		if cmd.Flags().Changed(flag) && cmd.Flags().Lookup(flag).Value.String() == "" {
			return commandError("empty_test_option", "Analyzer test options must not be empty.", "Omit unused options.")
		}
	}
	options.input.Positional = args
	if cmd.Flags().Changed("pattern") {
		options.input.Pattern = &options.pattern
	}
	inputs, err := analysis.Discover(ctx, options.input)
	if err != nil {
		return err
	}
	for _, input := range inputs {
		if input.URL != "" {
			return commandError("local_samples_required",
				"Analyzer test accepts local samples, not URLs.", "Use analyze for URLs.")
		}
	}
	if options.output != "" {
		options.output = filepath.Clean(options.output)
	}
	if err := analysis.CheckTestReport(options.output, inputs, options.force); err != nil {
		return err
	}
	if options.dryRun {
		refs := make([]string, len(inputs))
		for i, input := range inputs {
			refs[i] = input.Reference
		}
		return writeServiceResult(cmd, extCtx.OutputFormat, struct {
			DryRun   bool     `json:"dryRun"`
			Analyzer string   `json:"analyzerId"`
			Inputs   []string `json:"inputs"`
		}{true, name, refs})
	}
	if len(inputs) > 50 && !options.yes && extCtx.NoPrompt {
		return commandError("confirmation_required", "Analyzer test requires confirmation for more than 50 samples.",
			"Pass --yes to confirm the billed operation in non-interactive mode.")
	}
	options.service.DefaultAnalyzer = name
	session, err := open(cmd, extCtx, options.service)
	if err != nil {
		return err
	}
	defer session.close()
	if len(inputs) > 50 && !options.yes {
		message := fmt.Sprintf("Analyze %d samples with '%s'? This may incur cost.", len(inputs), name)
		confirmed, err := session.confirm(ctx, message)
		if err != nil {
			return err
		}
		if !confirmed {
			return &cuclient.Error{Kind: cuclient.ErrorCancelled, Code: "analyzer_test_not_confirmed",
				Message: "Analyzer test was not confirmed."}
		}
	}
	report, runErr := analysis.RunTest(ctx, session.client, inputs, name, options.concurrency)
	if report.Samples != nil && options.output != "" {
		if err := analysis.SaveTestReport(options.output, report, options.force); err != nil {
			return err
		}
	}
	if runErr != nil {
		return runErr
	}
	if report.Summary.Failed != 0 {
		return &cuclient.Error{Kind: cuclient.ErrorService, Code: "analyzer_test_failed",
			Message:    fmt.Sprintf("%d analyzer test sample(s) failed.", report.Summary.Failed),
			Suggestion: "Use --output-file to retain the complete per-sample report."}
	}
	return writeServiceResult(cmd, extCtx.OutputFormat, report)
}
