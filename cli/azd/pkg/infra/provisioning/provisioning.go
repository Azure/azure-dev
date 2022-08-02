package provisioning

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
)

func UpdateEnvironment(env *environment.Environment, outputs *map[string]PreviewOutputParameter) error {
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
