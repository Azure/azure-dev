// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"errors"
	"net/http"

	"azureaieval/internal/messages"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

type noEvaluatorVersionsError struct{ name string }

func (e *noEvaluatorVersionsError) Error() string {
	return messages.EvaluatorHasNoVersions(e.name).Error()
}

// IsEvaluatorAbsent recognizes a 404 or a valid, complete empty version listing.
// Failures part-way through pagination are never evidence of absence.
func IsEvaluatorAbsent(err error) bool {
	if _, walking := errors.AsType[pageWalkError](err); walking {
		return false
	}
	if _, empty := errors.AsType[*noEvaluatorVersionsError](err); empty {
		return true
	}
	return IsNotFound(err)
}

// IsConflict reports whether the service refused because the resource is busy.
func IsConflict(err error) bool {
	respErr, ok := errors.AsType[*azcore.ResponseError](err)
	if !ok {
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
	respErr, ok := errors.AsType[*azcore.ResponseError](err)
	if !ok {
		return false
	}
	return respErr.StatusCode == http.StatusNotFound
}
