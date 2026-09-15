// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

func extensionTaskMessage(action string, extensionId string) string {
	return fmt.Sprintf("%s %s", action, output.WithHighLightFormat(extensionId))
}

func extensionTaskMessageWithVersion(action string, extensionId string, version string) string {
	return extensionTaskMessage(action, extensionId) + output.WithGrayFormat(" (%s)", version)
}
