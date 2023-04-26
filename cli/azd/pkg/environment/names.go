// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import "os"

func GetResourceGroupNameFromEnvVar(env *Environment) string {
	// First check azd environment
	resourceGroupName, ok := env.Values[ResourceGroupEnvVarName]
	if ok {
		return resourceGroupName
	}

	// Next check OS environment
	resourceGroupName = os.Getenv(ResourceGroupEnvVarName)
	if resourceGroupName != "" {
		return resourceGroupName
	}

	return ""
}
