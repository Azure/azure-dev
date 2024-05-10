// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build record

package httputil

import (
	"math/rand"
	"net/http"
	"strconv"
)

// Provider headers for polling fast-forwarding.
func PollHeader() http.Header {
	return map[string][]string{
		//nolint:gosec
		//See usage in poll_discard.go
		"Recording-Poll-Id": {strconv.Itoa(rand.Int())},
	}
}
