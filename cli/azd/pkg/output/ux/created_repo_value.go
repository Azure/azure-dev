// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type GitHubValueKind string

const (
	GitHubSecret   GitHubValueKind = "secret"
	GitHubVariable GitHubValueKind = "variable"
)

type CreatedRepoValue struct {
	Name string
	Kind GitHubValueKind
}

func (cr *CreatedRepoValue) ToString(currentIndentation string) string {
	return fmt.Sprintf("%s%s Setting %s repo %s", currentIndentation, donePrefix, cr.Name, cr.Kind)
}

func (cr *CreatedRepoValue) MarshalJSON() ([]byte, error) {
	// reusing the same envelope from console messages
	return json.Marshal(output.EventForMessage(
		fmt.Sprintf("%s Setting %s repo %s", donePrefix, cr.Name, cr.Kind)))
}
