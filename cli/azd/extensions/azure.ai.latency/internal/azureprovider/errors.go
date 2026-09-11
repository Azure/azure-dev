// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureprovider

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

var safeErrorCode = regexp.MustCompile(`^[A-Za-z0-9._-]{1,80}$`)

type serviceError struct {
	operation  string
	statusCode int
	errorCode  string
	cause      error
}

func (e *serviceError) Error() string {
	switch {
	case e.statusCode != 0 && e.errorCode != "":
		return fmt.Sprintf("%s failed (status %d, code %s)", e.operation, e.statusCode, e.errorCode)
	case e.statusCode != 0:
		return fmt.Sprintf("%s failed (status %d)", e.operation, e.statusCode)
	default:
		return e.operation + " failed"
	}
}

func (e *serviceError) Unwrap() error {
	return e.cause
}

// safeServiceError intentionally excludes service messages and request URLs.
// Azure errors can contain authenticated URLs, query strings, or submitted KQL.
func safeServiceError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	safe := &serviceError{operation: operation, cause: err}
	if responseError, ok := errors.AsType[*azcore.ResponseError](err); ok {
		safe.statusCode = responseError.StatusCode
		if safeErrorCode.MatchString(responseError.ErrorCode) {
			safe.errorCode = responseError.ErrorCode
		}
	}
	return safe
}
