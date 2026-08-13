// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build record

package recordproxy

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"os"
)

// Transport routes requests through the recording proxy.
var Transport http.RoundTripper

func init() {
	value, ok := os.LookupEnv("AZD_TEST_HTTPS_PROXY")
	if !ok {
		return
	}

	proxyURL, err := url.Parse(value)
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
		InsecureSkipVerify: true, //nolint:gosec
	}
	transport.Proxy = http.ProxyURL(proxyURL)

	http.DefaultTransport = transport
	Transport = transport
}
