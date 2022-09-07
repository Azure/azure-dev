// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

func GetResourceGroupNameFromEnvVar(env *Environment) string {
	resourceGroupName, ok := env.values[ResourceGroupEnvVarName]
	if ok {
		return resourceGroupName
	}
	return ""
}
