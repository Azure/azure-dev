// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type UxItem interface {
	// Defines how the object is transformed into a printable string.
	// The current indentation can be used to make the string to be aligned to the previous lines.
	ToString(currentIndentation string) string
	json.Marshaler
}

var DonePrefix string = output.WithSuccessFormat("(✓) Done:")
var FailedPrefix string = output.WithErrorFormat("(x) Failed:")
var WarningPrefix string = output.WithWarningFormat("(!) Warning:")
var SkippedPrefix string = output.WithGrayFormat("(-) Skipped:")
