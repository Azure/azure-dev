package bicep

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
)

// Compile a template using a given CLI.
func Compile(ctx context.Context, bicepCli bicep.BicepCli, bicepPath string) (CompiledTemplate, error) {
	// Compile the bicep file into an ARM template we can create.
	compiled, err := bicepCli.Build(ctx, bicepPath)
	if err != nil {
		return CompiledTemplate{}, fmt.Errorf("failed to compile bicep template: %w", err)
	}

	// Fetch the parameters from the template and ensure we have a value for each one, otherwise
	// prompt.
	var template CompiledTemplate
	if err := json.Unmarshal([]byte(compiled), &template); err != nil {
		log.Printf("failed un-marshaling compiled arm template to JSON (err: %v), template contents:\n%s", err, compiled)
		return CompiledTemplate{}, fmt.Errorf("error un-marshaling arm template from json: %w", err)
	}

	return template, nil
}

type CompiledTemplate struct {
	Parameters map[string]map[string]interface{}
	Outputs    map[string]interface{}
}

// CanonicalizeDeploymentOutputs constructs a new map based on the value of `deploymentOutputs`, correcting the case
// of output names to match what is in the template (since an ARM Deployment does not preserve the casing of output names). The
// new map is assigned to to the pointer.
func (template *CompiledTemplate) CanonicalizeDeploymentOutputs(deploymentOutputs *map[string]azcli.AzCliDeploymentOutput) {
	canonicalOutputCasings := make(map[string]string, len(template.Outputs))
	newOutputs := make(map[string]azcli.AzCliDeploymentOutput, len(*deploymentOutputs))

	for k := range template.Outputs {
		canonicalOutputCasings[strings.ToLower(k)] = k
	}

	for k, v := range *deploymentOutputs {
		canonicalCasing, found := canonicalOutputCasings[strings.ToLower(k)]
		if found {
			newOutputs[canonicalCasing] = v
		} else {
			newOutputs[k] = v
		}
	}

	*deploymentOutputs = newOutputs
}
