// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"go.opentelemetry.io/otel/attribute"
)

type apphostPublishMode string

const (
	// legacy initial deployment mode where infrastructure is managed by azd (ACE and ACA)
	publishModeFullAzd apphostPublishMode = "fullAzd"
	// azd handles ACE while the AppHost can manage ACA
	publishModeHybrid apphostPublishMode = "hybrid"
	// fullApphost mode where the AppHost manages all infra
	publishModeFullApphost apphostPublishMode = "fullAspire"
)

// detectConfirmAppHost handles prompting for confirming the detected project with an app host.
type detectConfirmAppHost struct {
	// The app host we found
	AppHost appdetect.Project

	// the root directory of the project
	root string

	// internal state and components
	console input.Console

	// publish mode for the app host
	publishMode apphostPublishMode
}

// Init initializes state from initial detection output
func (d *detectConfirmAppHost) Init(appHost appdetect.Project, root string, manifest *apphost.Manifest) {
	d.AppHost = appHost

	d.captureUsage(
		fields.AppInitDetectedServices)

	d.publishMode = resolvePublishMode(manifest)
}

// Inspect the apphost manifest to resolve the publish mode
// Full azd -> if project.v0, container.v0 or dockerfile.v0 is found in the manifest.
// Hybrid -> makes reference to a global object like "{.outputs.FOO}" - if such a reference is found, the mode is hybrid
// Full apphost -> if project.v1 or container.v1 is found in the manifest with no references to {.outputs.}
// Notes:
// Manifests without projects, containers or dockerfiles (like a lonely key vault) are considered full app host.
func resolvePublishMode(manifest *apphost.Manifest) apphostPublishMode {
	for _, comp := range manifest.Resources {
		switch comp.Type {
		case "project.v0", "container.v0", "dockerfile.v0":
			return publishModeFullAzd
		}
	}
	// marshall the full manifest back to string
	b, err := json.Marshal(manifest)
	if err != nil {
		// should never happen b/c the manifest was previously unmarshalled
		panic(fmt.Sprintf("failed to marshal app host manifest: %v", err))
	}
	if strings.Contains(string(b), "{.outputs.") {
		return publishModeHybrid
	}

	// manifests with project.v1 or container.v1 with no references to {.outputs.} are considered full app host
	// in full apphost mode, references changed from global object to a resource ref like {infra.outputs.<name>}
	return publishModeFullApphost
}

func (d *detectConfirmAppHost) captureUsage(
	services attribute.Key) {

	tracing.SetUsageAttributes(
		services.StringSlice([]string{string(d.AppHost.Language)}),
	)
}

// Confirm prompts the user to confirm the detected services and databases,
// providing modifications to the detected services and databases.
func (d *detectConfirmAppHost) Confirm(ctx context.Context) error {
	for {
		if err := d.render(ctx); err != nil {
			return err
		}

		defaultConfirmation := "Confirm and continue initializing my app"
		continueOption, err := d.console.Select(ctx, input.ConsoleOptions{
			Message: "Select an option",
			Options: []string{
				defaultConfirmation,
				"Cancel and exit",
			},
			DefaultValue: defaultConfirmation,
		})
		if err != nil {
			return err
		}

		switch continueOption {
		case 0:
			d.captureUsage(
				fields.AppInitConfirmedServices)
			return nil
		case 1:
			return fmt.Errorf("cancelled due to user input")
		}
	}
}

func (d *detectConfirmAppHost) render(ctx context.Context) error {
	d.console.Message(ctx, "\n"+output.WithBold("Detected services:")+"\n")

	d.console.Message(ctx, "  "+output.WithHighLightFormat(projectDisplayName(d.AppHost)))
	d.console.Message(ctx, "  "+"Detected in: "+output.WithHighLightFormat(relSafe(d.root, d.AppHost.Path)))
	d.console.Message(ctx, "")

	if d.publishMode == publishModeFullAzd {
		d.console.Message(
			ctx, fmt.Sprintf("  %s Your Aspire project is delegating the services' host infrastructure to azd.",
				output.WithWarningFormat("Limited mode Warning:")))
		d.console.Message(
			ctx, fmt.Sprintf("  This mode is limited. "+
				"You will not be able to manage the host infrastructure from your AppHost. You need to use %s to "+
				"make changes to the Azure Container Environment and/or Azure Container Apps",
				output.WithBackticks("azd infra gen")))
		d.console.Message(
			ctx, fmt.Sprintf("  See more: %s",
				output.WithLinkFormat("https://learn.microsoft.com/dotnet/aspire/azure/configure-aca-environments")))
		d.console.Message(ctx, "")
	}

	if d.publishMode == publishModeHybrid {
		d.console.Message(
			ctx, fmt.Sprintf("  %s Your Aspire project is on hybrid mode. While your AppHost code can define the "+
				"Azure Container App, azd defines the Azure Container Environment.",
				output.WithWarningFormat("Deprecation Warning:")))
		d.console.Message(
			ctx, "  This mode is deprecated since Aspire 9.4.")
		//nolint:lll
		d.console.Message(ctx, fmt.Sprintf("  See more: %s", output.WithLinkFormat("https://learn.microsoft.com/dotnet/aspire/whats-new/dotnet-aspire-9.4#-azure-container-apps-hybrid-mode-removal")))
		d.console.Message(ctx, "")
	}

	d.console.Message(ctx, "azd will generate the files necessary to host your app on Azure.")

	return nil
}
