// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type UxItem interface {
	// Defines how the object is transformed into a printable string.
	// The current indentation can be used to make the string to be aligned to the previous lines.
	ToString(currentIndentation string) string

	// Envelope returns the UX message as an event envelope suitable for machine parsing.
	Envelope() contracts.EventEnvelope
}

var donePrefix string = output.WithSuccessFormat("(✓) Done:")
