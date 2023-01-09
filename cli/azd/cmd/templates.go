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
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
)

func templateNameCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	templateManager := templates.NewTemplateManager()
	templateSet, err := templateManager.ListTemplates()

	if err != nil {
		cobra.CompError(fmt.Sprintf("Error listing templates: %s", err))
		return []string{}, cobra.ShellCompDirectiveError
	}

	templateList := maps.Values(templateSet)
	slices.SortFunc(templateList, func(a, b templates.Template) bool {
		return a.Name < b.Name
	})
	templateNames := make([]string, len(templateList))
	for i, v := range templateList {
		templateNames[i] = v.Name
	}
	return templateNames, cobra.ShellCompDirectiveDefault
}

func templatesActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("template", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short: "Manage templates.",
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
		Short:   "List templates.",
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
	templateSet, err := tl.templateManager.ListTemplates()

	if err != nil {
		return nil, err
	}

	templateList := maps.Values(templateSet)
	slices.SortFunc(templateList, func(a, b templates.Template) bool {
		return a.Name < b.Name
	})

	return nil, formatTemplates(ctx, tl.formatter, tl.writer, templateList...)
}

type templatesShowAction struct {
	formatter       output.Formatter
	writer          io.Writer
	templateManager *templates.TemplateManager
	templateName    string
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
		templateName:    args[0],
	}
}

func (a *templatesShowAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	matchingTemplate, err := a.templateManager.GetTemplate(a.templateName)

	if err != nil {
		return nil, err
	}

	return nil, formatTemplates(ctx, a.formatter, a.writer, matchingTemplate)
}

func newTemplateShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <template>",
		Short: "Show the template details.",
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
