// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
)

// reportUsage records one usage event and never changes the command result.
//
// The connection is opened per event rather than held for the process, because
// commands here open an azd connection only where they need one, and a run that
// reports nothing should not open one at all.
func reportUsage(ctx context.Context, event foundryTelemetry.Event) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		azdext.NewLogger("eval.telemetry").Debug("telemetry client unavailable", "event", event.Name)
		return
	}
	defer azdClient.Close()

	foundryTelemetry.NewReporter(azdClient.Telemetry(), nil).Report(ctx, event)
}
