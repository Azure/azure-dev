// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"

	"azure.ai.content-understanding/internal/analysis"
	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/exterrors"
	"azure.ai.content-understanding/internal/preview"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type analyzeOptions struct {
	service     config.Options
	input       analysis.InputOptions
	output      analysis.OutputOptions
	pattern     string
	concurrency int
	inline      bool
	usage       bool
	dryRun      bool
	yes         bool
	render      bool
}

func newAnalyzeCommand(extCtx *azdext.ExtensionContext, open sessionFactory) *cobra.Command {
	options := &analyzeOptions{}
	cmd := &cobra.Command{
		Use: "analyze [INPUTS...]", Short: "Analyze local files, directories, or HTTPS URLs",
		Long: "Analyze local files, directories, or HTTPS URLs using Microsoft Entra authentication.\n\n" +
			"Uses prebuilt-layout when no analyzer is specified or configured.\n" +
			"Results use YAML front matter and Markdown by default. Batches save source-relative result files.\n" +
			"Use --render for an opt-in human-readable preview of one streamed LLM-input result.\n" +
			"Without --render, stdout retains the original Markdown and HTML tables, including when redirected.\n" +
			"Use --output json for complete JSON, or --dry-run to inspect the plan without service calls.",
		Example: `  azd ai cu analyze invoice.pdf
    Analyze one file using the configured analyzer, or prebuilt-layout if unset.

  azd ai cu analyze invoice.pdf --analyzer prebuilt-invoice --output json
    Extract invoice fields and return the complete response as JSON.

  azd ai cu analyze --url "https://example.com/document.pdf" --analyzer prebuilt-layout
    Analyze an HTTPS URL without downloading the file locally.

  azd ai cu analyze --source ./documents --output-dir ./results
    Analyze immediate files in a directory and save results under ./results.

  azd ai cu analyze --source ./documents --pattern '*.pdf' --recursive --output-dir ./results
    Analyze matching PDF files recursively and preserve their relative paths.

  azd ai cu analyze invoice.pdf --llm-input --output-file invoice.md
    Save YAML front matter and Markdown for generative AI model input.

  azd ai cu analyze invoice.pdf --render
    Preview Markdown with supported HTML tables; wide tables use labeled cards.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return exterrors.FromError(runAnalyzeCommand(cmd, extCtx, open, options, args))
		},
	}
	flags := cmd.Flags()
	flags.StringArrayVar(&options.input.Files, "file", nil, "Local file (repeatable; cannot mix input modes)")
	flags.StringArrayVar(&options.input.Sources, "source", nil, "Directory (repeatable; cannot mix input modes)")
	flags.StringArrayVar(&options.input.URLs, "url", nil, "HTTPS input URL (repeatable; cannot mix input modes)")
	flags.StringVar(&options.pattern, "pattern", "", "Source-relative fnmatch pattern; requires --source")
	flags.BoolVarP(&options.input.Recursive, "recursive", "r", false,
		"Discover files recursively, excluding hidden directories")
	flags.StringVarP(&options.service.DefaultAnalyzer, "analyzer", "a", "",
		"Analyzer ID (default: configured analyzer or prebuilt-layout)")
	flags.StringVar(&options.service.Endpoint, "endpoint", "", "Microsoft Foundry resource endpoint")
	flags.StringVar(&options.service.APIVersion, "api-version", "",
		"CU API version (default: resolved setting or 2025-11-01)")
	flags.BoolVarP(&options.inline, "inline", "i", false, "Use preview inline analysis; never falls back to async")
	flags.BoolVar(&options.usage, "usage", false, "Include usage metadata in streamed results or per-input summaries")
	flags.BoolVar(&options.output.LLMInput, "llm-input", true,
		"Render YAML front matter and Markdown; set false for complete JSON; true conflicts with --output json")
	flags.BoolVar(&options.render, "render", false,
		"Preview one streamed LLM-input result using Markdown and table/cards layout (default: original text)")
	flags.StringVar(&options.output.File, "output-file", "", "Save one result to this file")
	flags.StringVarP(&options.output.Directory, "output-dir", "d", "", "Save source-relative result files in this directory")
	flags.StringVar(&options.output.Existing, "on-existing", "",
		"Existing results: error, skip, reanalyze (default: CU_ON_EXISTS or error)")
	flags.BoolVar(&options.dryRun, "dry-run", false,
		"Plan without analyzing, prompting, creating directories, or writing files")
	flags.BoolVarP(&options.yes, "yes", "y", false, "Confirm batches larger than 50 inputs without prompting")
	flags.StringVar(&options.output.Report, "report-file", "", "Save a new cu-cli/analyze-report/v1 report; never overwrite")
	flags.IntVarP(&options.concurrency, "concurrency", "j", 4, "Concurrent analyses (1-32)")
	return azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"default", "json", "none"},
		Usage: "Output format: default (text for LLM input, otherwise JSON), json (complete response), or none",
	})
}

func runAnalyzeCommand(
	cmd *cobra.Command, extCtx *azdext.ExtensionContext, open sessionFactory, options *analyzeOptions, args []string,
) error {
	ctx := cmd.Context()
	if err := ctx.Err(); err != nil {
		return err
	}
	if extCtx.OutputFormat == "json" {
		if cmd.Flags().Changed("llm-input") && options.output.LLMInput {
			return commandError("conflicting_analyze_options",
				"--output json cannot be combined with --llm-input=true.",
				"Omit --llm-input to return complete JSON, or omit --output json to render LLM input.")
		}
		options.output.LLMInput = false
	}
	if options.render {
		for _, conflict := range []struct {
			set  bool
			name string
		}{
			{extCtx.OutputFormat != "default", "--output " + extCtx.OutputFormat},
			{!options.output.LLMInput, "--llm-input=false"},
			{options.usage, "--usage"},
			{options.dryRun, "--dry-run"},
			{cmd.Flags().Changed("output-file"), "--output-file"},
			{cmd.Flags().Changed("output-dir"), "--output-dir"},
		} {
			if conflict.set {
				return commandError("conflicting_analyze_options",
					"--render cannot be combined with "+conflict.name+".",
					"Omit --render to keep original text, saved results, or structured output.")
			}
		}
	}
	if options.dryRun && options.yes {
		return commandError("dry_run_confirmation",
			"--dry-run cannot be combined with --yes.", "Remove --yes when planning.")
	}
	if options.concurrency < 1 || options.concurrency > 32 {
		return commandError("invalid_concurrency", "--concurrency must be between 1 and 32.", "Use -j 1 through -j 32.")
	}
	for _, name := range []string{"file", "source", "url", "analyzer", "output-file", "output-dir", "report-file"} {
		if cmd.Flags().Changed(name) && cmd.Flags().Lookup(name).Value.String() == "" {
			return commandError("empty_analyze_option",
				"Analyze options must not be empty.", "Remove the empty option or supply a value.")
		}
	}
	if options.service.DefaultAnalyzer != "" {
		if err := cuclient.ValidateAnalyzerID(options.service.DefaultAnalyzer); err != nil {
			return err
		}
	}
	options.input.Positional = args
	if cmd.Flags().Changed("pattern") {
		options.input.Pattern = &options.pattern
	}
	if !cmd.Flags().Changed("on-existing") {
		var found bool
		options.output.Existing, found = os.LookupEnv("CU_ON_EXISTS")
		if !found {
			options.output.Existing = "error"
		}
	}
	inputs, err := analysis.Discover(ctx, options.input)
	if err != nil {
		return err
	}
	plan, err := analysis.PlanOutputs(inputs, options.output)
	if err != nil {
		return err
	}
	if options.render && len(plan.Outputs) != 1 {
		return commandError("conflicting_analyze_options",
			"--render requires one input and cannot preview a batch.",
			"Select one input, or omit --render to save batch results.")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !options.dryRun && len(inputs) > 50 && !options.yes && extCtx.NoPrompt {
		return commandError("confirmation_required", "Analyzing more than 50 inputs requires cost confirmation.",
			"Pass --yes to confirm the batch in non-interactive mode.")
	}
	session, err := open(cmd, extCtx, options.service)
	if err != nil {
		return err
	}
	defer session.close()
	if session.analyzer == "" {
		return commandError("missing_analyzer",
			"No analyzer was selected.", "Pass --analyzer or configure CU_DEFAULT_ANALYZER.")
	}
	if err := cuclient.ValidateAnalyzerID(session.analyzer); err != nil {
		return err
	}
	mode := cuclient.AnalysisModeAsync
	if options.inline {
		if session.apiVersion == cuclient.DefaultAPIVersion {
			return commandError("inline_requires_preview", "Inline analysis requires a preview API version.",
				"Select a YYYY-MM-DD-preview API version or omit --inline.")
		}
		mode = cuclient.AnalysisModeInline
	}
	if options.dryRun {
		return writeServiceResult(cmd, extCtx.OutputFormat, plan.Preview(session.analyzer))
	}
	if len(inputs) > 50 && !options.yes {
		message := fmt.Sprintf("Analyze %d inputs with '%s'? This may incur cost.", len(inputs), session.analyzer)
		confirmed, err := session.confirm(ctx, message)
		if err != nil {
			return err
		}
		if !confirmed {
			return &cuclient.Error{
				Kind: cuclient.ErrorCancelled, Code: "analysis_not_confirmed", Message: "Analysis was not confirmed.",
			}
		}
	}
	execution, err := analysis.Execute(ctx, session.client, plan, analysis.RunOptions{
		Analyzer: session.analyzer, Mode: mode, Concurrency: options.concurrency, Usage: options.usage,
	})
	if err != nil {
		return err
	}
	if execution.Stream != nil {
		if text, ok := execution.Stream.(string); ok && extCtx.OutputFormat == "default" {
			if err := ctx.Err(); err != nil {
				return err
			}
			if options.render {
				width, useColor := 80, false
				if file, ok := cmd.OutOrStdout().(*os.File); ok && azdext.IsInteractiveTerminal(file) {
					var err error
					width, _, err = term.GetSize(int(file.Fd())) //nolint:gosec // File descriptor fits in int.
					if err != nil {
						return fmt.Errorf("read preview terminal size: %w", err)
					}
					useColor = !color.NoColor
				}
				return preview.Render(ctx, cmd.OutOrStdout(), text, width, useColor)
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), text); err != nil {
				return fmt.Errorf("write CU result: %w", err)
			}
			return nil
		}
		return writeServiceResult(cmd, extCtx.OutputFormat, execution.Stream)
	}
	return writeServiceResult(cmd, extCtx.OutputFormat, execution.Records)
}
