// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !record

package httputil

import (
	"net/http"
)

// By default, PollHeader is a no-op function that returns nil.
// This function is overridden in the record mode to return header for polling fast-forwarding.
func PollHeader() http.Header {
	return nil
}
