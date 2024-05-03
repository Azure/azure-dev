// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/spf13/cobra"
)

func templatesActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("template", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short: fmt.Sprintf("Find and view template details. %s", output.WithWarningFormat("(Beta)")),
		},
		HelpOptions: actions.ActionHelpOptions{
			Description: getCmdTemplateHelpDescription,
			Footer:      getCmdTemplateHelpFooter,
		},
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupConfig,
		},
	})

	group.Add("list", &actions.ActionDescriptorOptions{
		Command:        newTemplateListCmd(),
		ActionResolver: newTemplateListAction,
		FlagsResolver:  newTemplateListFlags,
		OutputFormats:  []output.Format{output.JsonFormat, output.TableFormat},
		DefaultFormat:  output.TableFormat,
	})

	group.Add("show", &actions.ActionDescriptorOptions{
		Command:        newTemplateShowCmd(),
		ActionResolver: newTemplateShowAction,
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
	})

	_ = templateSourceActions(group)

	return group
}

type templateListFlags struct {
	source string
	tags   []string
}

func newTemplateListFlags(cmd *cobra.Command) *templateListFlags {
	flags := &templateListFlags{}
	cmd.Flags().StringVarP(&flags.source, "source", "s", "", "Filters templates by source.")

	cmd.Flags().StringSliceVarP(
		&flags.tags,
		"filter",
		"f",
		[]string{},
		"The tag(s) used to filter template results. Supports comma-separated values.",
	)

	return flags
}

func newTemplateListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   fmt.Sprintf("Show list of sample azd templates. %s", output.WithWarningFormat("(Beta)")),
		Aliases: []string{"ls"},
	}
}

type templateListAction struct {
	flags           *templateListFlags
	formatter       output.Formatter
	writer          io.Writer
	templateManager *templates.TemplateManager
}

func newTemplateListAction(
	flags *templateListFlags,
	formatter output.Formatter,
	writer io.Writer,
	templateManager *templates.TemplateManager,
) actions.Action {
	return &templateListAction{
		flags:           flags,
		formatter:       formatter,
		writer:          writer,
		templateManager: templateManager,
	}
}

func (tl *templateListAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	options := &templates.ListOptions{
		Source: tl.flags.source,
		Tags:   tl.flags.tags,
	}
	listedTemplates, err := tl.templateManager.ListTemplates(ctx, options)
	if err != nil {
		return nil, err
	}

	if tl.formatter.Kind() == output.TableFormat {
		columns := []output.Column{
			{
				Heading:       "Name",
				ValueTemplate: "{{.Name}}",
			},
			{
				Heading:       "Source",
				ValueTemplate: "{{.Source}}",
			},
			{
				Heading:       "Repository Path",
				ValueTemplate: "{{.RepositoryPath}}",
				Transformer:   templates.Hyperlink,
			},
		}

		err = tl.formatter.Format(listedTemplates, tl.writer, output.TableFormatterOptions{
			Columns: columns,
		})
	} else {
		err = tl.formatter.Format(listedTemplates, tl.writer, nil)
	}

	return nil, err
}

type templateShowAction struct {
	formatter       output.Formatter
	writer          io.Writer
	templateManager *templates.TemplateManager
	path            string
}

func newTemplateShowAction(
	formatter output.Formatter,
	writer io.Writer,
	templateManager *templates.TemplateManager,
	args []string,
) actions.Action {
	return &templateShowAction{
		formatter:       formatter,
		writer:          writer,
		templateManager: templateManager,
		path:            args[0],
	}
}

func (a *templateShowAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	matchingTemplate, err := a.templateManager.GetTemplate(ctx, a.path)

	if err != nil {
		return nil, err
	}

	if a.formatter.Kind() == output.NoneFormat {
		err = matchingTemplate.Display(a.writer)
	} else {
		err = a.formatter.Format(matchingTemplate, a.writer, nil)
	}

	return nil, err
}

func newTemplateShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <template>",
		Short: fmt.Sprintf("Show details for a given template. %s", output.WithWarningFormat("(Beta)")),
		Args:  cobra.ExactArgs(1),
	}
}

func getCmdTemplateHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(
		fmt.Sprintf(
			"View details of your current template or browse a list of curated sample templates. %s",
			output.WithWarningFormat("(Beta)")),
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

func getCmdTemplateSourceHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(
		fmt.Sprintf(
			"View and manage azd template sources used within %s and %s experiences. %s",
			output.WithHighLightFormat("azd template list"),
			output.WithHighLightFormat("azd init"),
			output.WithWarningFormat("(Beta)")),
		[]string{
			formatHelpNote("Template sources allow customizing the list of available templates to include additional" +
				" local or remote files and urls."),
			formatHelpNote(fmt.Sprintf("Running %s without a template will prompt you to start with a minimal"+
				" template or select from a template from your registered template sources.",
				output.WithHighLightFormat("azd init"))),
		})
}

// templateSourceActions creates the 'source' command group with child actions
func templateSourceActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("source", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short: fmt.Sprintf("View and manage template sources. %s", output.WithWarningFormat("(Beta)")),
		},
		HelpOptions: actions.ActionHelpOptions{
			Description: getCmdTemplateSourceHelpDescription,
			Footer:      getCmdTemplateSourceHelpFooter,
		},
	})

	group.Add("list", &actions.ActionDescriptorOptions{
		Command:        newTemplateSourceListCmd(),
		ActionResolver: newTemplateSourceListAction,
		OutputFormats:  []output.Format{output.JsonFormat, output.TableFormat},
		DefaultFormat:  output.TableFormat,
	})

	group.Add("add", &actions.ActionDescriptorOptions{
		Command:        newTemplateSourceAddCmd(),
		ActionResolver: newTemplateSourceAddAction,
		FlagsResolver:  newTemplateSourceAddFlags,
		OutputFormats:  []output.Format{output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
	})

	group.Add("remove", &actions.ActionDescriptorOptions{
		Command:        newTemplateSourceRemoveCmd(),
		ActionResolver: newTemplateSourceRemoveAction,
		OutputFormats:  []output.Format{output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
	})

	return group
}

func newTemplateSourceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   fmt.Sprintf("Lists the configured azd template sources. %s", output.WithWarningFormat("(Beta)")),
		Aliases: []string{"ls"},
	}
}

type templateSourceListAction struct {
	formatter     output.Formatter
	writer        io.Writer
	sourceManager templates.SourceManager
}

func newTemplateSourceListAction(
	formatter output.Formatter,
	writer io.Writer,
	sourceManager templates.SourceManager,
) actions.Action {
	return &templateSourceListAction{
		formatter:     formatter,
		writer:        writer,
		sourceManager: sourceManager,
	}
}

func (a *templateSourceListAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	sourceConfigs, err := a.sourceManager.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list template sources: %w", err)
	}

	if a.formatter.Kind() == output.TableFormat {
		columns := []output.Column{
			{
				Heading:       "Key",
				ValueTemplate: "{{.Key}}",
			},
			{
				Heading:       "Name",
				ValueTemplate: "{{.Name}}",
			},
			{
				Heading:       "Type",
				ValueTemplate: "{{.Type}}",
			},
			{
				Heading:       "Location",
				ValueTemplate: "{{.Location}}",
			},
		}

		err = a.formatter.Format(sourceConfigs, a.writer, output.TableFormatterOptions{
			Columns: columns,
		})
	} else {
		err = a.formatter.Format(sourceConfigs, a.writer, nil)
	}

	return nil, err
}

func newTemplateSourceAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <key>",
		Short: fmt.Sprintf("Adds an azd template source at the specified key %s", output.WithWarningFormat("(Beta)")),
		Args:  cobra.ExactArgs(1),
	}
}

type templateSourceAddFlags struct {
	name     string
	location string
	kind     string
}

func newTemplateSourceAddFlags(cmd *cobra.Command) *templateSourceAddFlags {
	flags := &templateSourceAddFlags{}

	cmd.Flags().StringVarP(&flags.kind, "type", "t", "", "Kind of the template source.")
	cmd.Flags().StringVarP(&flags.location, "location", "l", "", "Location of the template source.")
	cmd.Flags().StringVarP(&flags.name, "name", "n", "", "Display name of the template source.")

	return flags
}

type templateSourceAddAction struct {
	flags         *templateSourceAddFlags
	console       input.Console
	sourceManager templates.SourceManager
	args          []string
}

func newTemplateSourceAddAction(
	flags *templateSourceAddFlags,
	console input.Console,
	sourceManager templates.SourceManager,
	args []string,
) actions.Action {
	return &templateSourceAddAction{
		flags:         flags,
		console:       console,
		sourceManager: sourceManager,
		args:          args,
	}
}

func (a *templateSourceAddAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Add template source (azd template source add)",
	})

	var key = a.args[0]
	sourceConfig := &templates.SourceConfig{}

	spinnerMessage := "Validating template source"
	a.console.ShowSpinner(ctx, spinnerMessage, input.Step)

	// Don't allow source type since they can only be added with known key like 'default' or 'awesome-azd'
	for _, wellKnownSource := range templates.WellKnownSources {
		if wellKnownSource.Type == templates.SourceKind(a.flags.kind) {
			a.console.StopSpinner(ctx, spinnerMessage, input.StepFailed)
			return nil, fmt.Errorf(
				"template source type '%s' is not supported. Supported types are 'file' and 'url'",
				a.flags.kind,
			)
		}
	}

	if _, ok := templates.WellKnownSources[key]; !ok {
		sourceConfig = &templates.SourceConfig{
			Key:      key,
			Type:     templates.SourceKind(a.flags.kind),
			Location: a.flags.location,
			Name:     a.flags.name,
		}

		// Validate the custom source config
		_, err := a.sourceManager.CreateSource(ctx, sourceConfig)
		a.console.StopSpinner(ctx, spinnerMessage, input.GetStepResultFormat(err))
		if err != nil {
			if errors.Is(err, templates.ErrSourceTypeInvalid) {
				return nil, fmt.Errorf(
					"template source type '%s' is not supported. Supported types are 'file' and 'url'",
					a.flags.kind,
				)
			}

			return nil, fmt.Errorf("template source validation failed: %w", err)
		}
	}

	spinnerMessage = "Saving template source"
	a.console.ShowSpinner(ctx, spinnerMessage, input.Step)

	err := a.sourceManager.Add(ctx, key, sourceConfig)
	a.console.StopSpinner(ctx, spinnerMessage, input.GetStepResultFormat(err))
	if err != nil {
		return nil, fmt.Errorf("failed adding template source: %w", err)
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   fmt.Sprintf("Added azd template source %s", key),
			FollowUp: "Run `azd template list` to see the available set of azd templates.",
		},
	}, nil
}

func newTemplateSourceRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <key>",
		Short: fmt.Sprintf("Removes the specified azd template source %s", output.WithWarningFormat("(Beta)")),
		Args:  cobra.ExactArgs(1),
	}
}

type templateSourceRemoveAction struct {
	sourceManager templates.SourceManager
	console       input.Console
	args          []string
}

func newTemplateSourceRemoveAction(
	sourceManager templates.SourceManager,
	console input.Console,
	args []string,
) actions.Action {
	return &templateSourceRemoveAction{
		sourceManager: sourceManager,
		console:       console,
		args:          args,
	}
}

func (a *templateSourceRemoveAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Remove template source (azd template source remove)",
	})

	var key = a.args[0]
	spinnerMessage := fmt.Sprintf("Removing template source (%s)", key)
	a.console.ShowSpinner(ctx, spinnerMessage, input.Step)
	err := a.sourceManager.Remove(ctx, key)
	a.console.StopSpinner(ctx, spinnerMessage, input.GetStepResultFormat(err))
	if err != nil {
		return nil, fmt.Errorf("failed removing template source: %w", err)
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf("Removed azd template source %s", key),
			FollowUp: fmt.Sprintf(
				"Add more template sources by running %s",
				output.WithHighLightFormat("azd template source add <key>"),
			),
		},
	}, nil
}

func getCmdTemplateHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"View a list of all azd templates across template sources.": output.WithHighLightFormat(
			"azd template list",
		),
		"View a list of azd templates for a specific template source.": output.WithHighLightFormat(
			"azd template list --source <key>",
		),
		"View the details of an azd template.": output.WithHighLightFormat(
			"azd template show <template-name>",
		),
	})
}

func getCmdTemplateSourceHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"View a list of registered azd template sources.": output.WithHighLightFormat(
			"azd template source list",
		),
		"Enable the Awesome Azd template source.": output.WithHighLightFormat(
			"azd template source add awesome-azd",
		),
		"Add a new file template source.": output.WithHighLightFormat(
			"azd template source add <key> --type file --location <path>",
		),
		"Add a new url template source.": output.WithHighLightFormat(
			"azd template source add <key> --type url --location <url>",
		),
		"Remove a previously registered template source.": output.WithHighLightFormat(
			"azd template source remove <key>",
		),
	})
}
