// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ai

import "errors"

var (
	// ErrQuotaLocationRequired indicates quota checks were requested without exactly one location.
	ErrQuotaLocationRequired = errors.New("quota checking requires exactly one location")
	// ErrModelNotFound indicates the requested model was not found in the effective model catalog.
	ErrModelNotFound = errors.New("model not found")
	// ErrNoDeploymentMatch indicates no deployment candidate matched provided filters/constraints.
	ErrNoDeploymentMatch = errors.New("no deployment match")
)
