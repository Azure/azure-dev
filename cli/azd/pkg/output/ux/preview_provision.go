// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/fatih/color"
)

type PreviewProvision struct {
	Operations []*Resource
}

type OperationType string

const (
	OperationTypeCreate      OperationType = "Create"
	OperationTypeDelete      OperationType = "Delete"
	OperationTypeDeploy      OperationType = "Deploy"
	OperationTypeIgnore      OperationType = "Ignore"
	OperationTypeModify      OperationType = "Modify"
	OperationTypeNoChange    OperationType = "NoChange"
	OperationTypeUnsupported OperationType = "Unsupported"
)

type Resource struct {
	Operation OperationType
	Name      string
	Type      string
}

func colorType(opType OperationType) string {
	final := string(opType)
	switch opType {
	case OperationTypeCreate:
		final = color.GreenString(final)
	case OperationTypeDelete:
		final = color.RedString(final)
	case OperationTypeModify:
		final = color.YellowString(final)
	default:
		final = color.YellowString(final)
	}
	return output.WithBold(final)
}

func (pp *PreviewProvision) ToString(currentIndentation string) string {
	title := currentIndentation + "List of changes:"
	separator := currentIndentation + "\n" + strings.Repeat("─", 10) + "\n"

	changes := make([]string, len(pp.Operations))
	for index, op := range pp.Operations {
		changes[index] = fmt.Sprintf("%s\n%s (%s)", colorType(op.Operation), op.Name, op.Type)
	}

	return fmt.Sprintf("%s\n\n%s", title, strings.Join(changes, separator))
}

func (pp *PreviewProvision) MarshalJSON() ([]byte, error) {
	// reusing the same envelope from console messages
	return json.Marshal(output.EventForMessage("provisioning preview"))
}
