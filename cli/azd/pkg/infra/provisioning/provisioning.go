package provisioning

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
)

func UpdateEnvironment(env *environment.Environment, outputs *[]InfraDeploymentOutputParameter) error {
	if len(*outputs) > 0 {
		for _, param := range *outputs {
			env.Values[param.Name] = fmt.Sprintf("%v", param.Value)
		}

		if err := env.Save(); err != nil {
			return fmt.Errorf("writing environment: %w", err)
		}
	}

	return nil
}
