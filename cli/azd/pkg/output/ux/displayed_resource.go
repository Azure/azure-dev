// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

const (
	SucceededState DisplayedResourceState = "Succeeded"
	FailedState    DisplayedResourceState = "Failed"
)

type DisplayedResourceState string

type DisplayedResource struct {
	Type  string
	Name  string
	State DisplayedResourceState
}

func (cr *DisplayedResource) ToString(currentIndentation string) string {
	var prefix string

	switch cr.State {
	case SucceededState:
		prefix = donePrefix
	case FailedState:
		prefix = failedPrefix
	default:
		prefix = donePrefix
	}

	return fmt.Sprintf("%s%s %s: %s", currentIndentation, prefix, cr.Type, cr.Name)
}

func (cr *DisplayedResource) MarshalJSON() ([]byte, error) {
	// reusing the same envelope from console messages
	return json.Marshal(output.EventForMessage(
		fmt.Sprintf("%s: Creating %s: %s", cr.State, cr.Type, cr.Name)))
}
