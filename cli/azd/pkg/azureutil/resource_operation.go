// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureutil

import (
	"time"
)

type DeployedAzureResource struct {
	Id                      string
	ResourceType            string
	ResourceTypeDisplayName string
	ResourceName            string
	DeployedTimestamp       time.Time
}

type DeployedAzureResourceByTimestamp []DeployedAzureResource

func (s DeployedAzureResourceByTimestamp) Len() int { return len(s) }
func (s DeployedAzureResourceByTimestamp) Less(i, j int) bool {
	return time.Time.Before(s[i].DeployedTimestamp, s[j].DeployedTimestamp)
}
func (s DeployedAzureResourceByTimestamp) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
