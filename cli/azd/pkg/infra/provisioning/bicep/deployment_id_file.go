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
// receive ARM deployment IDs as they become available during `azd provision`
// (or `azd up`). The value is the absolute path of a file that azd will write
// NDJSON (newline-delimited JSON) lines to — one per layer deployment.
//
// Each line has the shape:
//
//	{"deploymentId":"/subscriptions/.../providers/Microsoft.Resources/deployments/<name>","layer":"<name>"}
//
// The file is truncated at the start of each provisioning run so it only contains
// deployments from the current invocation. Consumers should tail/watch the file and
// parse each line independently.
const deploymentIdFileEnvVar = "AZD_DEPLOYMENT_ID_FILE"

// deploymentIdFileMu serializes writes from sibling provisioning layers that may run
// concurrently and target the same path. This ensures each NDJSON line is written
// atomically without interleaving with other layers' writes. It also guards the
// truncation-state fields below.
var deploymentIdFileMu sync.Mutex

// deploymentIdFileTruncateAttempted is true once the truncation step has been run
// in this process invocation (regardless of whether it succeeded). The first write
// attempts truncation; subsequent writes do not.
//
// Both this flag and deploymentIdFileTruncateErr MUST only be read or written while
// holding deploymentIdFileMu so concurrent layers observe a consistent state.
var deploymentIdFileTruncateAttempted bool

// deploymentIdFileTruncateErr persists the result of the truncation attempt so that
// every subsequent caller sees the same outcome. If the first attempt failed we must
// not silently append to a file that was never truncated (which would mix stale
// content from a previous run with current-run lines).
var deploymentIdFileTruncateErr error

// resetDeploymentIdFileTruncation resets the truncation state so subsequent calls
// to writeDeploymentIdFile will truncate the file again. Used by tests to ensure
// each subtest starts fresh.
func resetDeploymentIdFileTruncation() {
	deploymentIdFileMu.Lock()
	defer deploymentIdFileMu.Unlock()
	deploymentIdFileTruncateAttempted = false
	deploymentIdFileTruncateErr = nil
}

// deploymentIdLine is a single NDJSON line written to the file identified by
// AZD_DEPLOYMENT_ID_FILE. New fields may be added in the future; consumers MUST
// ignore unknown fields.
type deploymentIdLine struct {
	// DeploymentId is the ARM resource ID of the deployment, for example
	// /subscriptions/{sub}/providers/Microsoft.Resources/deployments/{name}
	// or /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Resources/deployments/{name}.
	DeploymentId string `json:"deploymentId"`
	// Layer is the provisioning layer name that produced this deployment. It is
	// empty for single-layer (non-layered) provisioning.
	Layer string `json:"layer"`
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

// writeDeploymentIdFile appends an NDJSON line containing the ARM deployment ID and
// layer name to the file identified by AZD_DEPLOYMENT_ID_FILE. If the environment
// variable is not set, the function is a no-op.
//
// The path must be absolute; relative paths are rejected to avoid writing the file
// to an unexpected location relative to the process working directory. The
// containing directory is assumed to exist and be writable.
//
// On the first invocation in a process, the file is truncated so it only contains
// deployments from the current provisioning run. Subsequent invocations (e.g., from
// parallel layers) append to the file. A process-wide mutex serializes writes so
// each NDJSON line is always complete.
//
// Failures are not returned because the file is purely informational and must not
// abort provisioning. They are written via the standard log package, which only
// surfaces output when --debug or AZD_DEBUG_LOG is enabled.
func writeDeploymentIdFile(deployment infra.Deployment, layer string) {
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

	data, err := json.Marshal(deploymentIdLine{DeploymentId: id, Layer: layer})
	if err != nil {
		log.Printf("failed to marshal %s payload: %v", deploymentIdFileEnvVar, err)
		return
	}

	// Trailing newline makes this a valid NDJSON line.
	data = append(data, '\n')

	// Serialize across sibling provisioning layers in this process.
	deploymentIdFileMu.Lock()
	defer deploymentIdFileMu.Unlock()

	// Truncate on first write in this process so the file only contains deployments
	// from the current provisioning run. We persist both the attempted flag and the
	// error so that if truncation fails on the first call, every subsequent caller
	// observes the failure and bails out — preventing appends to a file that still
	// holds stale content from a previous run.
	if !deploymentIdFileTruncateAttempted {
		deploymentIdFileTruncateAttempted = true
		// The path comes from an environment variable that the operator explicitly sets to opt
		// in to this feature, so trusting the value is by design (G304).
		err := os.Truncate(path, 0) //nolint:gosec // G304: operator-provided env var
		if err != nil && !os.IsNotExist(err) {
			// File doesn't exist yet — that's fine, we'll create it on append.
			deploymentIdFileTruncateErr = err
		}
	}
	if deploymentIdFileTruncateErr != nil {
		//nolint:gosec // G706: path comes from operator-provided env var
		log.Printf(
			"failed to truncate %s=%q: %v",
			deploymentIdFileEnvVar, path, deploymentIdFileTruncateErr)
		return
	}

	// Append the NDJSON line. O_APPEND ensures the write is atomic at the OS level
	// even without the mutex (but we keep the mutex for the truncate-then-append
	// sequencing on the first call).
	// The path comes from an environment variable that the operator explicitly sets to opt
	// in to this feature, so trusting the value is by design (G304).
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // G304: operator-provided env var
	if err != nil {
		log.Printf("failed to open %s=%q: %v", deploymentIdFileEnvVar, path, err) //nolint:gosec
		return
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		log.Printf("failed to write %s=%q: %v", deploymentIdFileEnvVar, path, err) //nolint:gosec
	}
}
