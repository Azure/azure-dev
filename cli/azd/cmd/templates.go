// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func templatesCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	root := &cobra.Command{
		Use:   "template",
		Short: "Manage templates.",
	}

	root.AddCommand(BuildCmd(rootOptions, templatesListCmdDesign, initTemplatesListAction, nil))
	root.AddCommand(BuildCmd(rootOptions, templatesShowCmdDesign, initTemplatesShowAction, nil))
	root.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", root.Name()))

	return root
}

type templatesListFlags struct {
	outputFormat string
	global       *internal.GlobalCommandOptions
}

func (tl *templatesListFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	output.AddOutputFlag(local, &tl.outputFormat, []output.Format{output.JsonFormat, output.TableFormat}, output.TableFormat)
	tl.global = global
}

func templatesListCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *templatesListFlags) {
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List templates.",
		Aliases: []string{"ls"},
	}

	flags := &templatesListFlags{}
	flags.Bind(cmd.Flags(), global)

	return cmd, flags
}

type templatesListAction struct {
	flags           templatesListFlags
	formatter       output.Formatter
	writer          io.Writer
	templateManager *templates.TemplateManager
}

func newTemplatesListAction(
	flags templatesListFlags,
	formatter output.Formatter,
	writer io.Writer,
	templateManager *templates.TemplateManager,
) *templatesListAction {
	return &templatesListAction{
		flags:           flags,
		formatter:       formatter,
		writer:          writer,
		templateManager: templateManager,
	}
}

func (i *templatesListAction) PostRun(ctx context.Context, RunResult error) error {
	return nil
}

func (tl *templatesListAction) Run(ctx context.Context) error {
	templateSet, err := tl.templateManager.ListTemplates()

	if err != nil {
		return err
	}

	templateList := make([]templates.Template, 0, len(templateSet))
	for _, template := range templateSet {
		templateList = append(templateList, template)
	}

	return formatTemplates(ctx, tl.formatter, tl.writer, templateList...)
}

type templatesShowAction actions.Action

func newTemplatesShowAction(
	formatter output.Formatter,
	writer io.Writer,
	templateManager *templates.TemplateManager,
	args []string,
) templatesShowAction {
	return actions.ActionFunc(func(ctx context.Context) error {
		templateName := args[0]
		matchingTemplate, err := templateManager.GetTemplate(templateName)

		log.Printf("Template Name: %s\n", templateName)

		if err != nil {
			return err
		}

		return formatTemplates(ctx, formatter, writer, matchingTemplate)
	})
}

func templatesShowCmdDesign(rootOptions *internal.GlobalCommandOptions) (*cobra.Command, *struct{}) {
	cmd := &cobra.Command{
		Use:   "show <template>",
		Short: "Show the template details.",
	}
	output.AddOutputParam(cmd, []output.Format{output.JsonFormat, output.TableFormat}, output.TableFormat)

	cmd.Args = cobra.ExactArgs(1)
	return cmd, &struct{}{}
}

func formatTemplates(
	ctx context.Context,
	formatter output.Formatter,
	writer io.Writer,
	templates ...templates.Template,
) error {
	var err error
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

		err = formatter.Format(templates, writer, output.TableFormatterOptions{
			Columns: columns,
		})
	} else {
		err = formatter.Format(templates, writer, nil)
	}

	if err != nil {
		return err
	}

	return nil
}
