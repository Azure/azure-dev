// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
)

func validateExtensionTelemetryUsages(
	usages []telemetryUsage,
	declarations map[string]fieldDeclaration,
) []string {
	var diagnostics []string
	for _, usage := range usages {
		finalKey := fields.ExtensionAttributePrefix + usage.key
		if _, ok := declarations[finalKey]; ok {
			continue
		}

		diagnostics = append(diagnostics, fmt.Sprintf(
			"%s:%d: %s uses extension telemetry attribute %q, but %q is not declared in "+
				"cli/azd/extensions/telemetry/fields.go",
			usage.path,
			usage.line,
			usage.extension,
			usage.key,
			finalKey,
		))
	}
	return diagnostics
}
