// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type EnvironmentDetails struct {
	Subscription string
	Location     string
}

func (t *EnvironmentDetails) ToString(currentIndentation string) string {
	details := []string{}
	if t.Subscription != "" {
		details = append(details, fmt.Sprintf(
			"Subscription: %s",
			output.WithHighLightFormat(t.Subscription),
		))
	}

	if t.Location != "" {
		details = append(details, fmt.Sprintf(
			"Location: %s",
			output.WithHighLightFormat(t.Location),
		))
	}

	return strings.Join(details, "\n") + "\n"
}

func (t *EnvironmentDetails) MarshalJSON() ([]byte, error) {
	// reusing the same envelope from console messages
	return json.Marshal(output.EventForMessage(fmt.Sprintf("\n%s\n%s\n", t.Subscription, t.Location)))
}
