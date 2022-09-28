// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package httputil

import (
	"context"
	"net/http"
)

type HttpClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type contextKey string

const (
	httpClientContextKey contextKey = "httpclient"
)

// GetHttpClient attempts to retrieve a HttpUtil instance from the specified context.
// Will return the context if found or create a new instance
func GetHttpClient(ctx context.Context) HttpClient {
	value := ctx.Value(httpClientContextKey)
	client, ok := value.(HttpClient)

	if !ok {
		return &http.Client{}
	}

	return client
}

func WithHttpClient(ctx context.Context, httpClient HttpClient) context.Context {
	return context.WithValue(ctx, httpClientContextKey, httpClient)
}
