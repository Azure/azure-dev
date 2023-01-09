// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/spf13/cobra"
)

const TelemetryCommandFlag = "telemetry"
const TelemetryUploadCommandFlag = "upload"

func telemetryActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add(TelemetryCommandFlag, &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short:  "Manage telemetry",
			Long:   "Manage telemetry",
			Hidden: true,
		},
	})

	group.Add(TelemetryUploadCommandFlag, &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short:  "Upload telemetry",
			Long:   "Upload telemetry",
			Hidden: true,
		},
		ActionResolver:   newUploadAction,
		DisableTelemetry: true,
	})

	return group
}

type uploadAction struct {
	rootOptions *internal.GlobalCommandOptions
}

func newUploadAction(global *internal.GlobalCommandOptions) actions.Action {
	return &uploadAction{
		rootOptions: global,
	}
}

func (a *uploadAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	telemetrySystem := telemetry.GetTelemetrySystem()

	if telemetrySystem == nil {
		return nil, nil
	}

	return nil, telemetrySystem.RunBackgroundUpload(ctx, a.rootOptions.EnableDebugLogging)
}
