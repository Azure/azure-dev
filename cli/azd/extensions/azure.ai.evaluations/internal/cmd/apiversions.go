// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

// API versions used by the Foundry data plane.
const (
	// ProjectEndpointAPIVersion covers datasets, evaluators, and evaluator
	// generation jobs on the project endpoint.
	ProjectEndpointAPIVersion = "2025-11-15-preview"

	// DataGenerationAPIVersion covers dataset generation jobs.
	DataGenerationAPIVersion = "v1"

	// OpenAI-compatible eval and run calls send no api-version, so there
	// is deliberately no constant for them.
)
