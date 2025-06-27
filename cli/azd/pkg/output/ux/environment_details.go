// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type EnvironmentDetails struct {
	Subscription string
	Location     string
	Stage        string
}

func (t *EnvironmentDetails) ToString(currentIndentation string) string {
	var location string
	if t.Location != "" {
		location = fmt.Sprintf("\nLocation: %s", output.WithHighLightFormat(t.Location))
	}

	var stage string
	if t.Stage != "" {
		stage = fmt.Sprintf("\nStage: %s", output.WithHighLightFormat(t.Stage))
	}

	return fmt.Sprintf(
		"Subscription: %s%s%s\n",
		output.WithHighLightFormat(t.Subscription),
		location,
		stage,
	)
}

func (t *EnvironmentDetails) MarshalJSON() ([]byte, error) {
	// reusing the same envelope from console messages
	return json.Marshal(output.EventForMessage(fmt.Sprintf("\n%s\n%s\n", t.Subscription, t.Location)))
}
