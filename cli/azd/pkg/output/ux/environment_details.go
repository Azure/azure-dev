// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/fatih/color"
)

type EnvironmentDetails struct {
	Subscription string
	Location     string
}

func (t *EnvironmentDetails) ToString(currentIndentation string) string {
	return fmt.Sprintf(
		"%sSubscription: %s\n%sLocation: %s\n",
		currentIndentation,
		color.BlueString(t.Subscription),
		currentIndentation,
		color.BlueString(t.Location),
	)
}

func (t *EnvironmentDetails) MarshalJSON() ([]byte, error) {
	// reusing the same envelope from console messages
	return json.Marshal(output.EventForMessage(fmt.Sprintf("\n%s\n%s\n", t.Subscription, t.Location)))
}
