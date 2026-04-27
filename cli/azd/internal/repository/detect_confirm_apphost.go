// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package repository

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// detectConfirmAppHost handles prompting for confirming the detected project with an app host.
type detectConfirmAppHost struct {
	// The app host we found
	AppHost appdetect.Project

	// the root directory of the project
	root string

	// internal state and components
	console input.Console

	warningMessage string
}

// Init initializes state from initial detection output
func (d *detectConfirmAppHost) Init(appHost appdetect.Project, root string, manifest *apphost.Manifest) {
	d.AppHost = appHost
	d.warningMessage = manifest.Warnings()
	d.captureUsage(
		fields.AppInitDetectedServices)
}

func (d *detectConfirmAppHost) captureUsage(
	services fields.AttributeKey) {

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

	if d.warningMessage != "" {
		d.console.Message(ctx, d.warningMessage)
		d.console.Message(ctx, "")
	}

	d.console.Message(ctx, "azd will generate the files necessary to host your app on Azure.")

	return nil
}
