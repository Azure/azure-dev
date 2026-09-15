// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestPipeline(fn roundTripFunc) runtime.Pipeline {
	return runtime.NewPipeline(
		"test",
		"v0.0.0",
		runtime.PipelineOptions{},
		&policy.ClientOptions{
			Transport: &http.Client{Transport: fn},
		},
	)
}
