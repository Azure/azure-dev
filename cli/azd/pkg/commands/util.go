package commands

import (
	"context"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

func GetAzCliFromContext(ctx context.Context) azcli.AzCli {
	// Check to see if we already have an az cli in the context
	azCli, ok := ctx.Value(environment.AzdCliContextKey).(azcli.AzCli)

	// We don't have one - create a new one
	if !ok {
		azCliArgs := azcli.NewAzCliArgs{
			EnableDebug:     false,
			EnableTelemetry: true,
		}

		options := globalCommandOptionsFromContext(ctx)

		azCliArgs.EnableDebug = options.EnableDebugLogging
		azCliArgs.EnableTelemetry = options.EnableTelemetry

		azCli = azcli.NewAzCli(azCliArgs)
	}

	selectedTemplate := ""

	// Set the user agent if a template has been selected
	if template, ok := ctx.Value(environment.TemplateContextKey).(string); ok && strings.TrimSpace(template) != "" {
		selectedTemplate = template
	}

	azCli.SetUserAgent(internal.MakeUserAgentString(selectedTemplate))

	return azCli
}
