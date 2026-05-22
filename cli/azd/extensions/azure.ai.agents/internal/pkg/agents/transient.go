// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agents

import (
	"errors"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// IsTransientError checks whether an error represents a transient HTTP failure
// (429 Too Many Requests, 5xx Server Error, or connection-level errors) that
// is safe to retry.
func IsTransientError(err error) bool {
	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		return respErr.StatusCode == 429 || respErr.StatusCode >= 500
	}
	// Connection resets and similar I/O errors are also transient.
	msg := err.Error()
	return strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "EOF")
}
