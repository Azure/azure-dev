// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "slices"

// No API available to query supported regions for hosted agents, so keep hardcoded list based on public documentation:
// https://learn.microsoft.com/azure/foundry/agents/concepts/hosted-agents#region-availability
// TODO: List of supported regions for vNext may be different
var supportedHostedAgentRegions = []string{
	"australiaeast",
	"brazilsouth",
	"canadacentral",
	"canadaeast",
	"eastus",
	"eastus2",
	"francecentral",
	"germanywestcentral",
	"italynorth",
	"japaneast",
	"koreacentral",
	"northcentralus",
	"norwayeast",
	"polandcentral",
	"southafricanorth",
	"southcentralus",
	"southeastasia",
	"southindia",
	"spaincentral",
	"swedencentral",
	"switzerlandnorth",
	"uaenorth",
	"uksouth",
	"westus",
	"westus3",
}

func supportedRegionsForInit() []string {
	return slices.Clone(supportedHostedAgentRegions)
}

// supportedModelLocations returns the intersection of a model's available locations
// with the supported hosted agent regions.
func supportedModelLocations(modelLocations []string) []string {
	supported := supportedRegionsForInit()
	return slices.DeleteFunc(slices.Clone(modelLocations), func(loc string) bool {
		return !locationAllowed(loc, supported)
	})
}
