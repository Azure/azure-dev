// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureutil

import (
	"fmt"
)

type ResourceNotFoundError struct {
	err error
}

func (e *ResourceNotFoundError) Error() string {
	if e.err == nil {
		return "resource not found: <nil>"
	}

	return fmt.Sprintf("resource not found: %s", e.err.Error())
}

func ResourceNotFound(err error) error {
	return &ResourceNotFoundError{err: err}
}
