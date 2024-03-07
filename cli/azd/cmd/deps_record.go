// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build record

package cmd

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/benbjohnson/clock"
)

func createHttpClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Allow for self-signed certificates, which is what the recording proxy uses.
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	// AZD_TEST_HTTPS_PROXY is the proxy setting that only affects azd in record mode.
	// This is useful since the recording proxy server isn't trusted by other processes currently.
	if val, ok := os.LookupEnv("AZD_TEST_HTTPS_PROXY"); ok {
		proxyUrl, err := url.Parse(val)
		if err != nil {
			panic(err)
		}

		transport.Proxy = http.ProxyURL(proxyUrl)
		http.DefaultTransport = transport
	}

	http.DefaultClient.Transport = transport

	return http.DefaultClient
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
