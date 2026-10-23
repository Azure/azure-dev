// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azure.ai.content-understanding/internal/analysis"
	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/exterrors"
	"azure.ai.content-understanding/internal/localio"
	"azure.ai.content-understanding/internal/schema"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type schemaCreateOptions struct {
	author  schema.AuthorOptions
	service config.Options
	output  string
	force   bool
}

func newAnalyzerSchemaCommand(extCtx *azdext.ExtensionContext, open sessionFactory) *cobra.Command {
	group := &cobra.Command{Use: "schema", Short: "Author local analyzer schemas"}
	options := &schemaCreateOptions{}
	cmd := &cobra.Command{
		Use: "create", Short: "Create a starter schema or derive fields from one document sample",
		Long: "Create an offline extraction or classification template without a project or sign-in.\n\n" +
			"--from-sample sends one local document to Content Understanding to suggest extraction fields; " +
			"this may incur cost and requires an endpoint and azd sign-in.\n" +
			"Review the generated schema before creating an analyzer. No analyzer or service defaults are changed.",
		Example: `  azd ai cu analyzer schema create --name invoice_v1 --output-file schema.json
    Write an extraction starter schema locally without service calls.

  azd ai cu analyzer schema create --type classification --output json
    Print an offline classification starter schema as JSON.

  azd ai cu analyzer schema create --from-sample invoice.pdf --output-file suggested.json
    Suggest extraction fields from one document using the service; this may incur cost.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return exterrors.FromError(runSchemaCreate(cmd, extCtx, open, options))
		},
	}
	flags := cmd.Flags()
	flags.BoolVar(&options.author.FromTemplate, "from-template", false, "Create an offline starter schema (the default)")
	flags.StringVar(&options.author.Sample, "from-sample", "", "Suggest extraction fields from one local document")
	flags.StringVarP(&options.author.Name, "name", "n", "my_analyzer_v1", "Custom analyzer name (-a is also accepted)")
	flags.StringVarP(&options.author.Name, "a", "a", "my_analyzer_v1", "Alias for --name")
	_ = flags.MarkHidden("a")
	flags.StringVar(&options.author.Base, "base", "", "Base analyzer name; defaults from --modality")
	flags.StringVar(&options.author.Modality, "modality", "document", "Modality: document, image, audio, or video")
	flags.StringVar(&options.author.Type, "type", "extraction", "Template style: extraction or classification")
	flags.StringVar(&options.output, "output-file", "", "Save the schema as a local UTF-8 JSON file")
	flags.BoolVar(&options.force, "force", false, "Explicitly replace an existing schema file; never replace the sample")
	group.AddCommand(serviceCommand(cmd, &options.service))
	return group
}

func runSchemaCreate(
	cmd *cobra.Command, extCtx *azdext.ExtensionContext, open sessionFactory, options *schemaCreateOptions,
) error {
	ctx := cmd.Context()
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, flag := range []string{"from-sample", "base", "output-file", "name", "a", "modality", "type"} {
		if cmd.Flags().Changed(flag) && strings.TrimSpace(cmd.Flags().Lookup(flag).Value.String()) == "" {
			return commandError("empty_schema_option", "Schema-create options must not be empty.", "Omit unused options.")
		}
	}
	if err := options.author.Validate(); err != nil {
		return err
	}
	var inputs []analysis.Input
	if options.author.Sample != "" {
		if err := schema.ValidateSample(options.author.Sample); err != nil {
			return err
		}
		input, err := analysis.DiscoverFile(ctx, options.author.Sample)
		if err != nil {
			return err
		}
		inputs = []analysis.Input{input}
	}
	if err := checkSchemaOutput(options.output, inputs, options.force); err != nil {
		return err
	}
	var definition schema.Definition
	if len(inputs) == 0 {
		version := cmp.Or(options.service.APIVersion, os.Getenv("CU_API_VERSION"))
		var err error
		definition, err = schema.Starter(options.author, version)
		if err != nil {
			return err
		}
	} else {
		options.service.DefaultAnalyzer = schema.SuggestionAnalyzer
		session, err := open(cmd, extCtx, options.service)
		if err != nil {
			return err
		}
		defer session.close()
		suggestion, err := schema.Suggest(ctx, session.client, inputs[0], options.author, session.apiVersion)
		if err != nil {
			return err
		}
		if !suggestion.FoundFields {
			if _, err := fmt.Fprintln(cmd.ErrOrStderr(),
				"Warning: No suggested fields were returned; using the default extraction template."); err != nil {
				return fmt.Errorf("write schema suggestion warning: %w", err)
			}
		}
		if len(suggestion.Warnings) != 0 {
			if _, err := fmt.Fprintf(cmd.ErrOrStderr(),
				"Warning: CU returned %d schema-suggestion warning(s); review the generated schema.\n",
				len(suggestion.Warnings)); err != nil {
				return fmt.Errorf("write schema service warning: %w", err)
			}
		}
		definition = suggestion.Definition
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if options.output != "" {
		if err := checkSchemaOutput(options.output, inputs, options.force); err != nil {
			return err
		}
		body, err := json.MarshalIndent(definition, "", "  ")
		if err != nil {
			return err
		}
		if err := localio.Write(options.output, append(body, '\n'), options.force); err != nil {
			return &cuclient.Error{Kind: cuclient.ErrorService, Code: "schema_output_write_failed",
				Message: "The schema file could not be saved.", Cause: err}
		}
	}
	return writeServiceResult(cmd, extCtx.OutputFormat, definition)
}

func checkSchemaOutput(path string, inputs []analysis.Input, force bool) error {
	if path == "" {
		return nil
	}
	working, err := os.Getwd()
	if err != nil {
		return commandError("invalid_working_directory",
			"The working directory could not be resolved.", "Run from an existing directory.")
	}
	exists, err := localio.Check(filepath.Clean(path))
	if err != nil || analysis.AliasesInput(path, inputs, working) {
		return commandError("invalid_schema_output",
			"The schema output must be a regular file distinct from the sample.", "Choose a safe --output-file path.")
	}
	if exists && !force {
		return &cuclient.Error{Kind: cuclient.ErrorConflict, Code: "schema_output_exists",
			Message: "The schema output already exists.", Suggestion: "Pass --force to replace the file explicitly."}
	}
	return nil
}
