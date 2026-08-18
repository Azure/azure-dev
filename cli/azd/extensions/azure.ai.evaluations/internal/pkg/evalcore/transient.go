// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package evalcore

import (
	"errors"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// IsTransientError reports whether err is worth retrying: throttling, a server
// fault, or a dropped connection.
func IsTransientError(err error) bool {
	if err == nil {
		return false
	}

	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode == 429 || respErr.StatusCode >= 500
	}

	msg := err.Error()
	return strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "EOF")
}
