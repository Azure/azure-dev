// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

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

// deploymentIdFileMu serializes writes from sibling provisioning layers that may run
// concurrently and target the same path. Without this, partial/interleaved writes
// could corrupt the JSON document (for example when one deployment ID is shorter
// than another). Combined with a temp-file-then-rename strategy, readers either see
// the previous file or a complete new one, never a torn document.
var deploymentIdFileMu sync.Mutex

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
// The path must be absolute; relative paths are rejected to avoid writing the file
// to an unexpected location relative to the process working directory. The
// containing directory is assumed to exist and be writable. If the target file
// already exists it is overwritten atomically (temp-file + rename) so concurrent
// readers always observe either the previous content or a complete new document.
//
// Failures are not returned because the file is purely informational and must not
// abort provisioning. They are written via the standard log package, which only
// surfaces output when --debug or AZD_DEBUG_LOG is enabled.
func writeDeploymentIdFile(deployment infra.Deployment) {
	path := os.Getenv(deploymentIdFileEnvVar)
	if path == "" {
		return
	}

	if !filepath.IsAbs(path) {
		log.Printf("ignoring %s=%q: path must be absolute", deploymentIdFileEnvVar, path) //nolint:gosec
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

	// Serialize across sibling provisioning layers in this process, then write to a
	// temp file in the same directory and rename into place so the swap is atomic on
	// POSIX (and best-effort on Windows). Readers therefore never observe a partially
	// written or interleaved document.
	deploymentIdFileMu.Lock()
	defer deploymentIdFileMu.Unlock()

	dir := filepath.Dir(path)
	// The path comes from an environment variable that the operator explicitly sets to opt
	// in to this feature, so trusting the value is by design (G304/G706). The directory is
	// expected to exist and be writable per the documented contract.
	tmp, err := os.CreateTemp(dir, ".azd-deployment-id-*.json.tmp") //nolint:gosec // G304: operator-provided env var
	if err != nil {
		log.Printf("failed to create temp file for %s=%q: %v", deploymentIdFileEnvVar, path, err) //nolint:gosec
		return
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = os.Remove(tmpPath) //nolint:gosec // G304: tmpPath was just created above by os.CreateTemp.
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		log.Printf("failed to write %s=%q: %v", deploymentIdFileEnvVar, path, err) //nolint:gosec
		return
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		log.Printf("failed to close %s temp file for %q: %v", deploymentIdFileEnvVar, path, err) //nolint:gosec
		return
	}
	if err := os.Rename(tmpPath, path); err != nil { //nolint:gosec // G304: paths are operator-provided env var
		cleanup()
		log.Printf("failed to rename %s temp file into %q: %v", deploymentIdFileEnvVar, path, err) //nolint:gosec
		return
	}
}
