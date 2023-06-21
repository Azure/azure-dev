// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build record

package cmd

import (
	"crypto/tls"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/benbjohnson/clock"
)

func createHttpClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Allow for self-signed certificates, which is what the recording proxy uses.
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	client := &http.Client{
		Transport: transport,
	}

	http.DefaultClient = client
	return client
}

func createClock() clock.Clock {
	if fixed, ok := fixedClock(); ok {
		return fixed
	}

	return clock.New()
}

func fixedClock() (clock.Clock, bool) {
	if _, ok := os.LookupEnv("AZD_TEST_FIXED_CLOCK_UNIX_TIME"); !ok {
		return nil, false
	}

	mockClock := clock.NewMock()
	val := os.Getenv("AZD_TEST_FIXED_CLOCK_UNIX_TIME")
	unixSec, err := strconv.ParseInt(val, 0, 64)
	if err != nil {
		panic(err)
	}

	mockClock.Set(time.Unix(unixSec, 0))
	return mockClock, true
}
