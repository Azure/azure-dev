// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azsdk

import (
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// ResponseCapturePolicy is a policy that captures the http.response.
type ResponseCapturePolicy struct {
	// Response is the captured http.response from the latest Do() call.
	Response *http.Response
}

func (p *ResponseCapturePolicy) Do(req *policy.Request) (*http.Response, error) {
	res, err := req.Next()
	if err == nil {
		p.Response = res
	}
	return res, err
}
