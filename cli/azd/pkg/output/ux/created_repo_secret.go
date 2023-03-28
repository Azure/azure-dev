// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type CreatedRepoSecret struct {
	Name string
}

func (cr *CreatedRepoSecret) ToString(currentIndentation string) string {
	return fmt.Sprintf("%s%s Setting %s repo secret", currentIndentation, donePrefix, cr.Name)
}

func (cr *CreatedRepoSecret) MarshalJSON() ([]byte, error) {
	// reusing the same envelope from console messages
	return json.Marshal(output.EventForMessage(
		fmt.Sprintf("%s Setting %s repo secret", donePrefix, cr.Name)))
}
