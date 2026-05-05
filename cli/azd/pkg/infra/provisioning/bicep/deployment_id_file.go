// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
)

// deploymentIdFileEnvVar is the name of the environment variable consumers can set to
// receive the ARM deployment ID as soon as it is available during `azd provision`
// (or `azd up`). The value is the absolute path of a file that azd will write/overwrite
// with a JSON document describing the deployment.
//
// The current document shape is intentionally minimal so it can be extended in a
// backwards compatible way:
//
//	{ "deploymentId": "/subscriptions/.../providers/Microsoft.Resources/deployments/<name>" }
const deploymentIdFileEnvVar = "AZD_DEPLOYMENT_ID_FILE"

// deploymentIdFile is the JSON document written to the path identified by
// AZD_DEPLOYMENT_ID_FILE. New fields may be added in the future; consumers MUST
// ignore unknown fields.
type deploymentIdFile struct {
	// DeploymentId is the ARM resource ID of the deployment, for example
	// /subscriptions/{sub}/providers/Microsoft.Resources/deployments/{name}
	// or /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Resources/deployments/{name}.
	DeploymentId string `json:"deploymentId"`
}

// deploymentResourceID returns the ARM resource ID for the supplied deployment, or
// an error if the deployment scope is not recognized.
func deploymentResourceID(d infra.Deployment) (string, error) {
	switch dep := d.(type) {
	case *infra.SubscriptionDeployment:
		return azure.SubscriptionDeploymentRID(dep.SubscriptionId(), dep.Name()), nil
	case *infra.ResourceGroupDeployment:
		return azure.ResourceGroupDeploymentRID(dep.SubscriptionId(), dep.ResourceGroupName(), dep.Name()), nil
	default:
		return "", fmt.Errorf("unsupported deployment type: %T", d)
	}
}

// writeDeploymentIdFile writes the ARM deployment ID for the supplied deployment to
// the path identified by AZD_DEPLOYMENT_ID_FILE. If the environment variable is not
// set, the function is a no-op.
//
// The containing directory is assumed to exist and be writable. If the target file
// already exists it is overwritten. Failures to write the file are logged but never
// returned because the file is purely informational and must not abort provisioning.
func writeDeploymentIdFile(deployment infra.Deployment) {
	path := os.Getenv(deploymentIdFileEnvVar)
	if path == "" {
		return
	}

	id, err := deploymentResourceID(deployment)
	if err != nil {
		log.Printf("skipping %s: %v", deploymentIdFileEnvVar, err)
		return
	}

	data, err := json.Marshal(deploymentIdFile{DeploymentId: id})
	if err != nil {
		log.Printf("failed to marshal %s payload: %v", deploymentIdFileEnvVar, err)
		return
	}

	// Trailing newline keeps the file well-formed for line-based tooling.
	data = append(data, '\n')

	// The path comes from an environment variable that the operator explicitly sets to opt
	// in to this feature, so trusting the value is by design (G703). The directory is
	// expected to exist and be writable per the documented contract.
	if err := os.WriteFile(path, data, 0o600); err != nil { //nolint:gosec // G703: path is operator-provided env var
		// G706: path is operator-provided env var; logging it back is the whole point.
		log.Printf("failed to write %s=%q: %v", deploymentIdFileEnvVar, path, err) //nolint:gosec
		return
	}
}
