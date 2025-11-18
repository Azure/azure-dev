// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

const (
	SucceededState DisplayedResourceState = "Succeeded"
	FailedState    DisplayedResourceState = "Failed"
)

type DisplayedResourceState string

type DisplayedResource struct {
	Type     string
	Name     string
	State    DisplayedResourceState
	Duration time.Duration
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

	result := fmt.Sprintf("%s%s %s: %s", currentIndentation, prefix, cr.Type, cr.Name)
	if cr.Duration > 0 {
		result += output.WithGrayFormat(" (%s)", cr.Duration.String())
	}

	return result
}

func (cr *DisplayedResource) MarshalJSON() ([]byte, error) {
	// reusing the same envelope from console messages
	return json.Marshal(output.EventForMessage(
		fmt.Sprintf("%s: Creating %s: %s", cr.State, cr.Type, cr.Name)))
}
