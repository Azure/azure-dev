// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/spf13/cobra"
)

func templateNameCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	templateManager := templates.NewTemplateManager()
	templates, err := templateManager.ListTemplates()

	if err != nil {
		cobra.CompError(fmt.Sprintf("Error listing templates: %s", err))
		return []string{}, cobra.ShellCompDirectiveError
	}

	templateNames := make([]string, len(templates))
	for i, v := range templates {
		templateNames[i] = v.Name
	}
	return templateNames, cobra.ShellCompDirectiveDefault
}

func templatesActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("template", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short: "Find and view template details.",
		},
		HelpOptions: actions.ActionHelpOptions{
			Description: getCmdTemplateHelpDescription,
		},
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupConfig,
		},
	})

	group.Add("list", &actions.ActionDescriptorOptions{
		Command:        newTemplateListCmd(),
		ActionResolver: newTemplatesListAction,
		OutputFormats:  []output.Format{output.JsonFormat, output.TableFormat},
		DefaultFormat:  output.TableFormat,
	})

	group.Add("show", &actions.ActionDescriptorOptions{
		Command:        newTemplateShowCmd(),
		ActionResolver: newTemplatesShowAction,
		OutputFormats:  []output.Format{output.JsonFormat, output.TableFormat},
		DefaultFormat:  output.TableFormat,
	})

	return group
}

func newTemplateListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "Show list of sample azd templates.",
		Aliases: []string{"ls"},
	}
}

type templatesListAction struct {
	formatter       output.Formatter
	writer          io.Writer
	templateManager *templates.TemplateManager
}

func newTemplatesListAction(
	formatter output.Formatter,
	writer io.Writer,
	templateManager *templates.TemplateManager,
) actions.Action {
	return &templatesListAction{
		formatter:       formatter,
		writer:          writer,
		templateManager: templateManager,
	}
}

func (tl *templatesListAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	listedTemplates, err := tl.templateManager.ListTemplates()
	if err != nil {
		return nil, err
	}

	return nil, formatTemplates(ctx, tl.formatter, tl.writer, listedTemplates...)
}

type templatesShowAction struct {
	formatter       output.Formatter
	writer          io.Writer
	templateManager *templates.TemplateManager
	path            string
}

func newTemplatesShowAction(
	formatter output.Formatter,
	writer io.Writer,
	templateManager *templates.TemplateManager,
	args []string,
) actions.Action {
	return &templatesShowAction{
		formatter:       formatter,
		writer:          writer,
		templateManager: templateManager,
		path:            args[0],
	}
}

func (a *templatesShowAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	matchingTemplate, err := a.templateManager.GetTemplate(a.path)

	if err != nil {
		return nil, err
	}

	return nil, formatTemplates(ctx, a.formatter, a.writer, matchingTemplate)
}

func newTemplateShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <template>",
		Short: "Show details for a given template.",
		Args:  cobra.ExactArgs(1),
	}
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
				Heading:       "RepositoryPath",
				ValueTemplate: "{{.RepositoryPath}}",
			},
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

func getCmdTemplateHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription("View details of your current template or browse a list of curated sample templates.",
		[]string{
			formatHelpNote(fmt.Sprintf("The azd CLI includes a curated list of sample templates viewable by running %s.",
				output.WithHighLightFormat("azd template list"))),
			formatHelpNote(fmt.Sprintf("To view all available sample templates, including those submitted by the azd"+
				" community visit: %s.",
				output.WithLinkFormat("https://azure.github.io/awesome-azd"))),
			formatHelpNote(fmt.Sprintf("Running %s without a template will prompt you to start with a minimal"+
				" template or select from our curated list of samples.",
				output.WithHighLightFormat("azd init"))),
		})
}
