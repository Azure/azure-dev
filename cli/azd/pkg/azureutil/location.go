// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureutil

import (
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

type Locs []azcli.AzCliLocation

func (s Locs) Len() int { return len(s) }
func (s Locs) Less(i, j int) bool {
	return strings.Compare(strings.ToLower(s[i].RegionalDisplayName), strings.ToLower(s[j].RegionalDisplayName)) < 0
}
func (s Locs) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
