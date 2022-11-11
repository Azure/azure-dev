// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"fmt"
)

type CreatedResource struct {
	// The name of the created resource
	Type string
	Name string
}

func (cr *CreatedResource) ToString(currentIndentation string) string {
	return fmt.Sprintf("%s%s Creating %s: %s", currentIndentation, donePrefix, cr.Type, cr.Name)
}

func (cr *CreatedResource) ToJson() []byte {
	return nil
}

func (cr *CreatedResource) ToTable() string {
	return ""
}
