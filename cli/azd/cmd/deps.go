// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !record

package cmd

import (
	"net/http"

	"github.com/benbjohnson/clock"
)

func createHttpClient() *http.Client {
	return http.DefaultClient
}

func createClock() clock.Clock {
	return clock.New()
}
