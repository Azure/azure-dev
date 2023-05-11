// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resource

import (
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/fields"
)

// Returns a hash of the content of `.installed-by.txt` file in the same directory as
// the executable. If the file does not exist, returns empty string.
func getInstalledBy() string {

	installedBy := internal.GetRawInstalledBy()
	return fields.Sha256Hash(installedBy)
}
