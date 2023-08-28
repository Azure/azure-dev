// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/fatih/color"
)

// PreviewProvision defines a ux item for displaying a provision preview.
type PreviewProvision struct {
	Operations []*Resource
}

// OperationType defines the valid options for a resource change.
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

func (op OperationType) String() (displayName string) {
	switch op {
	case OperationTypeIgnore,
		OperationTypeNoChange:
		displayName = "Skip"
	default:
		displayName = string(op)
	}

	return displayName
}

// Resource provides a basic structure for an Azure resource.
type Resource struct {
	Operation OperationType
	Name      string
	Type      string
}

func colorType(opType OperationType) func(string, ...interface{}) string {
	var final func(format string, a ...interface{}) string
	switch opType {
	case OperationTypeCreate,
		OperationTypeNoChange,
		OperationTypeIgnore:
		final = output.WithGrayFormat
	case OperationTypeDelete:
		final = color.RedString
	case OperationTypeModify:
		final = color.YellowString
	default:
		final = color.YellowString
	}
	return final
}

func (pp *PreviewProvision) ToString(currentIndentation string) string {
	if len(pp.Operations) == 0 {
		// no output when there are no operations
		return ""
	}

	title := currentIndentation + "Resources:"

	changes := make([]string, len(pp.Operations))
	actions := make([]string, len(pp.Operations))
	resources := make([]string, len(pp.Operations))

	var maxActionLen int
	var maxResourceLen int
	// get max
	for _, op := range pp.Operations {
		if actionLen := len(op.Operation); actionLen > maxActionLen {
			maxActionLen = actionLen
		}
		if resourceLen := len(op.Type); resourceLen > maxResourceLen {
			maxResourceLen = resourceLen
		}
	}

	// Align
	for index, op := range pp.Operations {
		displayNameOp := op.Operation.String()
		opGapToFill := strings.Repeat(" ", maxActionLen-len(displayNameOp))
		typeGapToFill := strings.Repeat(" ", maxResourceLen-len(op.Type))
		actions[index] = displayNameOp + opGapToFill + " :"
		resources[index] = op.Type + typeGapToFill + " :"
	}

	for index, op := range pp.Operations {
		changes[index] = fmt.Sprintf("%s%s %s %s",
			currentIndentation,
			colorType(op.Operation)(actions[index]),
			resources[index],
			op.Name,
		)
	}

	return fmt.Sprintf("%s\n\n%s", title, strings.Join(changes, "\n"))
}

func (pp *PreviewProvision) MarshalJSON() ([]byte, error) {
	return json.Marshal(contracts.EventEnvelope{
		Type:      contracts.ConsoleMessageEventDataType,
		Timestamp: time.Now(),
		Data:      pp.Operations,
	})
}
