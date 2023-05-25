// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/messaging"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/progress"
)

const (
	SucceededState        DisplayedResourceState = "Succeeded"
	FailedState           DisplayedResourceState = "Failed"
	DisplayedResourceKind messaging.MessageKind  = "DisplayedResource"
)

type DisplayedResourceState string

type DisplayedResource struct {
	Type  string
	Name  string
	State DisplayedResourceState
}

func (cr *DisplayedResource) Print(ctx context.Context, printer *progress.Printer) {
	prefix := ux.DonePrefix

	switch cr.State {
	case SucceededState:
		prefix = ux.DonePrefix
	case FailedState:
		prefix = ux.FailedPrefix
	}

	printer.Message(ctx, fmt.Sprintf("%s %s: %s", prefix, cr.Type, cr.Name))
}

func NewDisplayedResourceMessage(resource *DisplayedResource) *messaging.Message {
	return messaging.NewMessage(DisplayedResourceKind, resource)
}
