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
	Operation      OperationType
	Name           string
	Type           string
	PropertyDeltas []PropertyDelta
}

// PropertyDelta represents a property-level change in a resource
type PropertyDelta struct {
	Path       string
	ChangeType string
	Before     interface{}
	After      interface{}
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

	var output []string
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
		resourceLine := fmt.Sprintf("%s%s %s %s",
			currentIndentation,
			colorType(op.Operation)(actions[index]),
			resources[index],
			op.Name,
		)
		output = append(output, resourceLine)

		// Add property-level changes if available
		if len(op.PropertyDeltas) > 0 {
			for _, delta := range op.PropertyDeltas {
				propertyLine := formatPropertyChange(currentIndentation+"    ", delta)
				output = append(output, propertyLine)
			}
		}
	}

	return fmt.Sprintf("%s\n\n%s", title, strings.Join(output, "\n"))
}

// formatPropertyChange formats a single property change for display
func formatPropertyChange(indent string, delta PropertyDelta) string {
	changeSymbol := ""
	changeColor := output.WithGrayFormat

	switch delta.ChangeType {
	case "Create":
		changeSymbol = "+"
		changeColor = output.WithGrayFormat
	case "Delete":
		changeSymbol = "-"
		changeColor = color.RedString
	case "Modify":
		changeSymbol = "~"
		changeColor = color.YellowString
	case "Array":
		changeSymbol = "*"
		changeColor = color.YellowString
	}

	// Format values for display
	beforeStr := formatValue(delta.Before)
	afterStr := formatValue(delta.After)

	if delta.ChangeType == "Modify" {
		return changeColor("%s%s %s: %s => %s", indent, changeSymbol, delta.Path, beforeStr, afterStr)
	} else if delta.ChangeType == "Create" {
		return changeColor("%s%s %s: %s", indent, changeSymbol, delta.Path, afterStr)
	} else if delta.ChangeType == "Delete" {
		return changeColor("%s%s %s", indent, changeSymbol, delta.Path)
	} else {
		// Array or other types
		return changeColor("%s%s %s", indent, changeSymbol, delta.Path)
	}
}

// formatValue formats a value for display (handling various types)
func formatValue(value interface{}) string {
	if value == nil {
		return "(null)"
	}

	switch v := value.(type) {
	case string:
		return fmt.Sprintf("\"%s\"", v)
	case map[string]interface{}, []interface{}:
		// For complex types, use a JSON-like representation
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(data)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func (pp *PreviewProvision) MarshalJSON() ([]byte, error) {
	return json.Marshal(contracts.EventEnvelope{
		Type:      contracts.ConsoleMessageEventDataType,
		Timestamp: time.Now(),
		Data:      pp.Operations,
	})
}
