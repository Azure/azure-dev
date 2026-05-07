// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !record

package cmd

import (
	"net/http"

	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/benbjohnson/clock"
)

func createHttpClient() *http.Client {
	return &http.Client{Transport: httputil.TunedTransport()}
}

func createClock() clock.Clock {
	return clock.New()
}
