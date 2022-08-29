// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureutil

import (
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

type Subs []azcli.AzCliSubscriptionInfo

func (s Subs) Len() int { return len(s) }
func (s Subs) Less(i, j int) bool {
	return strings.Compare(strings.ToLower(s[i].Name), strings.ToLower(s[j].Name)) < 0
}
func (s Subs) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
