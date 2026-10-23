// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/exterrors"
	"azure.ai.content-understanding/internal/schema"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type analyzerOptions struct {
	service config.Options
	name    string
	schema  string
	kind    string
	sortBy  string
	yes     bool
}

func newAnalyzerCommand(extCtx *azdext.ExtensionContext, open sessionFactory) *cobra.Command {
	group := &cobra.Command{Use: "analyzer", Short: "Manage Content Understanding analyzers"}
	group.AddCommand(newAnalyzerValidateCommand(extCtx))
	group.AddCommand(newAnalyzerCopyCommand(extCtx, open))
	group.AddCommand(newAnalyzerTestCommand(extCtx, open))
	group.AddCommand(newAnalyzerSchemaCommand(extCtx, open))
	for _, action := range []string{"list", "show", "create", "delete"} {
		options := &analyzerOptions{}
		cmd := &cobra.Command{Use: action, Args: cobra.NoArgs}
		flags := cmd.Flags()
		flags.StringVar(&options.service.Endpoint, "endpoint", "", "Microsoft Foundry resource endpoint")
		flags.StringVar(&options.service.APIVersion, "api-version", "",
			"CU API version: 2025-11-01 or YYYY-MM-DD-preview (default: resolved setting or GA)")
		if action != "list" {
			cmd.Use += " [ANALYZER_NAME]"
			flags.StringVarP(&options.name, "name", "n", "", "Analyzer name (-a is also accepted)")
			flags.StringVarP(&options.name, "a", "a", "", "Alias for --name")
			_ = flags.MarkHidden("a")
			cmd.Args = func(cmd *cobra.Command, args []string) error {
				if err := cobra.MaximumNArgs(1)(cmd, args); err != nil {
					return err
				}
				name, err := analyzerName(cmd, args, options.name)
				if err != nil {
					return exterrors.FromError(err)
				}
				if action == "create" {
					if err := schema.ValidateCustomAnalyzerID(name); err != nil {
						return exterrors.FromError(err)
					}
					if strings.TrimSpace(options.schema) == "" {
						return exterrors.FromError(commandError("missing_schema",
							"Specify the analyzer schema file.", "Use --schema or -s."))
					}
				}
				return nil
			}
		}
		switch action {
		case "list":
			cmd.Short = "List analyzers in the Microsoft Foundry resource"
			flags.StringVar(&options.kind, "kind", "all", "Filter by kind: all, prebuilt, custom")
			flags.StringVar(&options.sortBy, "sort-by", "analyzerId", "Sort by analyzerId, createdAt, lastModifiedAt")
			cmd.Example = `  azd ai cu analyzer list
    List all analyzers in the connected resource.

  azd ai cu analyzer list --kind prebuilt
    List the available prebuilt analyzers.

  azd ai cu analyzer list --kind custom --sort-by createdAt --output json
    List custom analyzers sorted by creation time.`
		case "show":
			cmd.Short = "Show a complete analyzer definition as JSON"
			cmd.Example = `  azd ai cu analyzer show prebuilt-layout
    Show the complete definition of a prebuilt analyzer.

  azd ai cu analyzer show invoice_v1 --output json
    Show a custom analyzer definition as JSON.`
		case "create":
			cmd.Short = "Create an analyzer from a JSON schema file"
			flags.Lookup("name").Usage = "Custom analyzer ID: 1-64 ASCII letters, numbers, " +
				"or underscores (-a is also accepted)"
			flags.StringVarP(&options.schema, "schema", "s", "", "Path to a JSON analyzer schema")
			_ = cmd.MarkFlagRequired("schema")
			cmd.Example = `  azd ai cu analyzer create invoice_v1 --schema schema.json
    Create a custom analyzer from a local JSON schema.`
		case "delete":
			cmd.Short = "Delete an analyzer"
			flags.BoolVarP(&options.yes, "yes", "y", false, "Confirm deletion without prompting")
			cmd.Example = `  azd ai cu analyzer delete invoice_v1
    Confirm and delete a custom analyzer.

  azd ai cu analyzer delete invoice_v1 --yes --no-prompt
    Delete an analyzer without an interactive confirmation.`
		}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			return exterrors.FromError(runAnalyzerCommand(cmd, extCtx, open, action, options, args))
		}
		group.AddCommand(azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
			Name: "output", AllowedValues: []string{"default", "json", "none"},
			Usage: "Output format: default (complete JSON), json, or none",
		}))
	}
	return group
}

func analyzerName(cmd *cobra.Command, args []string, named string) (string, error) {
	if len(args) != 0 && (cmd.Flags().Changed("name") || cmd.Flags().Changed("a")) {
		return "", commandError("conflicting_analyzer_name",
			"Specify the analyzer name only once.", "Use a positional name or --name, not both.")
	}
	name := named
	if len(args) != 0 {
		name = args[0]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", commandError("missing_analyzer_name", "Specify an analyzer name.", "Use a positional name or --name.")
	}
	if err := cuclient.ValidateAnalyzerID(name); err != nil {
		return "", err
	}
	return name, nil
}

func runAnalyzerCommand(
	cmd *cobra.Command, extCtx *azdext.ExtensionContext, open sessionFactory,
	action string, options *analyzerOptions, args []string,
) error {
	ctx := cmd.Context()
	if err := ctx.Err(); err != nil {
		return err
	}
	var name string
	var body json.RawMessage
	var pinned string
	var err error
	if action == "list" {
		if !slices.Contains([]string{"all", "prebuilt", "custom"}, options.kind) ||
			!slices.Contains([]string{"analyzerId", "createdAt", "lastModifiedAt"}, options.sortBy) {
			return commandError("invalid_analyzer_filter",
				"Select a supported analyzer kind and sort field.", "See analyzer list --help.")
		}
	} else {
		if name, err = analyzerName(cmd, args, options.name); err != nil {
			return err
		}
	}
	if action == "create" {
		content, err := os.ReadFile(options.schema)
		if err != nil {
			return commandError("schema_read_failed", "The analyzer schema file could not be read.",
				"Check that --schema names a readable UTF-8 JSON file, relative to --cwd when specified.")
		}
		if body, pinned, err = schema.PrepareDefinition(content); err != nil {
			return err
		}
	}
	if action == "delete" && !options.yes && extCtx.NoPrompt {
		return commandError("confirmation_required", "Analyzer deletion requires confirmation in non-interactive mode.",
			"Pass --yes to confirm deletion.")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	session, err := open(cmd, extCtx, options.service)
	if err != nil {
		return err
	}
	defer session.close()
	var result any
	switch action {
	case "list":
		items, err := session.client.ListAnalyzers(ctx)
		if err != nil {
			return err
		}
		items = slices.Clone(items)
		items = slices.DeleteFunc(items, func(item cuclient.Analyzer) bool {
			prebuilt := strings.HasPrefix(item.ID, "prebuilt-")
			return (options.kind == "custom" && prebuilt) || (options.kind == "prebuilt" && !prebuilt)
		})
		slices.SortStableFunc(items, func(a, b cuclient.Analyzer) int {
			switch options.sortBy {
			case "createdAt":
				return a.CreatedAt.Compare(b.CreatedAt)
			case "lastModifiedAt":
				return a.LastModifiedAt.Compare(b.LastModifiedAt)
			default:
				return cmp.Compare(a.ID, b.ID)
			}
		})
		raw := make([]json.RawMessage, 0, len(items))
		for _, item := range items {
			value, err := analyzerJSON(item)
			if err != nil {
				return err
			}
			raw = append(raw, value)
		}
		result = raw
	case "show":
		analyzer, err := session.client.GetAnalyzer(ctx, name)
		if err != nil {
			return err
		}
		if result, err = analyzerJSON(analyzer); err != nil {
			return err
		}
	case "create":
		if pinned != "" && pinned != session.apiVersion {
			return commandError("schema_api_version_mismatch",
				"The schema apiVersion does not match the resolved CU API version.",
				"Align the schema apiVersion with --api-version or the selected environment.")
		}
		response, err := session.client.CreateAnalyzer(ctx, cuclient.CreateAnalyzerRequest{
			AnalyzerID: name, Definition: body,
		})
		if err != nil {
			return err
		}
		if strings.EqualFold(response.Analyzer.Status, "failed") {
			return &cuclient.Error{Kind: cuclient.ErrorService, Code: "analyzer_creation_failed",
				Message: "The created analyzer has failed status."}
		}
		if result, err = analyzerJSON(response.Analyzer); err != nil {
			return err
		}
	case "delete":
		if !options.yes {
			confirmed, err := session.confirm(ctx, fmt.Sprintf("Delete analyzer '%s'?", name))
			if err != nil {
				return err
			}
			if !confirmed {
				return context.Canceled
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := session.client.GetAnalyzer(ctx, name); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := session.client.DeleteAnalyzer(ctx, name); err != nil {
			return err
		}
		result = struct {
			Deleted    bool   `json:"deleted"`
			AnalyzerID string `json:"analyzerId"`
		}{true, name}
	}
	return writeServiceResult(cmd, extCtx.OutputFormat, result)
}
