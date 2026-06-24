// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build record

// Package recordproxy intercepts ALL outbound HTTP traffic when the "record"
// build tag is active, routing it through the recording proxy (azd-record).
// This includes http.DefaultTransport (covers http.Client{} and similar) and
// Azure SDK clients (via client_options.go). Third-party SDKs that accept a
// custom transport parameter should also use Transport from this package to
// ensure their traffic is captured during recording/playback.
package recordproxy

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"os"
)

// Transport is the proxy-aware transport for record/playback mode.
// It routes all HTTP traffic through the recording proxy (AZD_TEST_HTTPS_PROXY).
// nil when the env var is not set.
var Transport http.RoundTripper

func init() {
	val, ok := os.LookupEnv("AZD_TEST_HTTPS_PROXY")
	if !ok {
		return
	}
	proxyUrl, err := url.Parse(val)
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
		InsecureSkipVerify: true, //nolint:gosec // recording proxy uses self-signed cert
	}
	transport.Proxy = http.ProxyURL(proxyUrl)

	// This affects extension's own http.Client{} usage which relies on DefaultTransport.
	// Azure SDK ARM clients are handled separately via ClientOptions.Transport in client_options.go.
	http.DefaultTransport = transport
	Transport = transport
}
