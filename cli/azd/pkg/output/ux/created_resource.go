// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type CreatedResource struct {
	Type string
	Name string
}

func (cr *CreatedResource) ToString(currentIndentation string) string {
	return fmt.Sprintf("%s%s %s: %s", currentIndentation, donePrefix, cr.Type, cr.Name)
}

func (cr *CreatedResource) MarshalJSON() ([]byte, error) {
	// reusing the same envelope from console messages
	return json.Marshal(output.EventForMessage(
		fmt.Sprintf("%s Creating %s: %s", donePrefix, cr.Type, cr.Name)))
}
