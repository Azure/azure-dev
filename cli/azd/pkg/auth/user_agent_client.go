// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import "net/http"

// userAgentClient wraps an HttpClient to inject a User-Agent header on all requests.
type userAgentClient struct {
	inner     HttpClient
	userAgent string
}

func newUserAgentClient(inner HttpClient, userAgent string) HttpClient {
	if userAgent == "" {
		return inner
	}
	return &userAgentClient{inner: inner, userAgent: userAgent}
}

func (c *userAgentClient) Do(req *http.Request) (*http.Response, error) {
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", c.userAgent)
	} else {
		req.Header.Set("User-Agent", req.Header.Get("User-Agent")+","+c.userAgent)
	}
	return c.inner.Do(req)
}

func (c *userAgentClient) CloseIdleConnections() {
	c.inner.CloseIdleConnections()
}
