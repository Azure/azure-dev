// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
)

func UpdateEnvironment(env *environment.Environment, outputs *map[string]OutputParameter) error {
	if len(*outputs) > 0 {
		for key, param := range *outputs {
			env.Values[key] = fmt.Sprintf("%v", param.Value)
		}

		if err := env.Save(); err != nil {
			return fmt.Errorf("writing environment: %w", err)
		}
	}

	return nil
}
