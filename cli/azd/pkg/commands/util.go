package commands

import (
	"context"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

func GetAzCliFromContext(ctx context.Context) tools.AzCli {
	// Check to see if we already have an az cli in the context
	azCli, ok := ctx.Value(environment.AzdCliContextKey).(tools.AzCli)

	// We don't have one - create a new one
	if !ok {
		azCliArgs := tools.NewAzCliArgs{
			EnableDebug:     false,
			EnableTelemetry: true,
		}

		options, ok := ctx.Value(environment.OptionsContextKey).(*GlobalCommandOptions)
		if !ok {
			panic("GlobalCommandOptions were not found in the context")
		}

		azCliArgs.EnableDebug = options.EnableDebugLogging
		azCliArgs.EnableTelemetry = options.EnableTelemetry

		azCli = tools.NewAzCli(azCliArgs)
	}

	// Set the user agent if a template has been selected
	template, ok := ctx.Value(environment.TemplateContextKey).(string)
	if ok && strings.TrimSpace(template) != "" {
		userAgent := internal.FormatTemplateForUserAgent(template)
		azCli.SetUserAgent([]string{userAgent})
	}

	return azCli
}
