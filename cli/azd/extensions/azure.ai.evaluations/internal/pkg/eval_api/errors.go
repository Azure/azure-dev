// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"errors"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// IsConflict reports whether the service refused because the resource is busy.
func IsConflict(err error) bool {
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		return false
	}
	return respErr.StatusCode == http.StatusConflict
}

// IsNotFound reports whether the service answered 404.
//
// A 404 raised part-way through a page walk is refused: the first page
// answered, so the asset is there, and reading the break as "no such asset"
// would have a caller create what already exists.
func IsNotFound(err error) bool {
	if _, walking := errors.AsType[pageWalkError](err); walking {
		return false
	}
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		return false
	}
	return respErr.StatusCode == http.StatusNotFound
}
