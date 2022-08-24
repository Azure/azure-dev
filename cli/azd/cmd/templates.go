// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/spf13/cobra"
)

func templatesCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	root := &cobra.Command{
		Use:   "template",
		Short: "Manage templates.",
	}

	root.AddCommand(output.AddOutputParam(
		templatesListCmd(rootOptions),
		[]output.Format{output.JsonFormat, output.TableFormat},
		output.TableFormat,
	))
	root.AddCommand(output.AddOutputParam(
		templatesShowCmd(rootOptions),
		[]output.Format{output.JsonFormat, output.TableFormat},
		output.TableFormat,
	))
	root.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", root.Name()))

	return root
}

func templatesListCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	action := commands.ActionFunc(func(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
		templateManager := templates.NewTemplateManager()
		templateSet, err := templateManager.ListTemplates()

		if err != nil {
			return err
		}

		templateList := make([]templates.Template, 0, len(templateSet))
		for _, template := range templateSet {
			templateList = append(templateList, template)
		}

		return formatTemplates(ctx, cmd, templateList...)
	})

	return commands.Build(
		action,
		rootOptions,
		"list",
		"List templates.",
		"",
	)
}

func templatesShowCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	action := commands.ActionFunc(
		func(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
			templateName := args[0]
			templateManager := templates.NewTemplateManager()
			matchingTemplate, err := templateManager.GetTemplate(templateName)

			log.Printf("Template Name: %s\n", templateName)

			if err != nil {
				return err
			}

			return formatTemplates(ctx, cmd, matchingTemplate)
		},
	)
	cmd := commands.Build(
		action,
		rootOptions,
		"show <template>",
		"Show the template details.",
		"",
	)
	cmd.Args = cobra.ExactArgs(1)
	return cmd
}

func formatTemplates(ctx context.Context, cmd *cobra.Command, templates ...templates.Template) error {
	var err error
	formatter := output.GetFormatter(ctx)

	if formatter.Kind() == output.TableFormat {
		columns := []output.Column{
			{
				Heading:       "Name",
				ValueTemplate: "{{.Name}}",
			},
			{
				Heading:       "Description",
				ValueTemplate: "{{.Description}}",
			},
		}

		err = formatter.Format(templates, cmd.OutOrStdout(), output.TableFormatterOptions{
			Columns: columns,
		})
	} else {
		err = formatter.Format(templates, cmd.OutOrStdout(), nil)
	}

	if err != nil {
		return err
	}

	return nil
}
