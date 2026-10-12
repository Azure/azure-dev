// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build record

// Package recordproxy routes HTTP traffic through the test recording proxy.
package recordproxy

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"os"
)

// Transport routes HTTP traffic through AZD_TEST_HTTPS_PROXY when configured.
var Transport http.RoundTripper

func init() {
	proxyValue, ok := os.LookupEnv("AZD_TEST_HTTPS_PROXY")
	if !ok {
		return
	}
	proxyURL, err := url.Parse(proxyValue)
	if err != nil {
		panic("recordproxy: invalid AZD_TEST_HTTPS_PROXY URL: " + err.Error())
	}

	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		panic("recordproxy: http.DefaultTransport is not *http.Transport")
	}
	transport := base.Clone()
	transport.TLSClientConfig = &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, //nolint:gosec // test proxy uses a self-signed certificate
	}
	transport.Proxy = http.ProxyURL(proxyURL)

	http.DefaultTransport = transport
	Transport = transport
}
