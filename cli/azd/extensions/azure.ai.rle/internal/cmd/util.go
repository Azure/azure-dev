// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	noEnvironmentsMessage        = "No environments found."
	noEnvironmentVersionsMessage = "No environment versions found."
)

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func renderTableOrNoResults(output *azdext.Output, headers []string, rows [][]string, emptyMessage string) {
	output.Message("")
	if len(rows) == 0 {
		output.Message(emptyMessage)
	} else {
		output.Table(headers, rows)
	}
	output.Message("")
}
