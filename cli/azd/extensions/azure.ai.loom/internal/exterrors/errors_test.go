// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestServiceFromAzureClassifiesCancellation(t *testing.T) {
	for _, err := range []error{
		context.Canceled,
		fmt.Errorf("send request: %w", context.Canceled),
		status.Error(codes.Canceled, "cancelled"),
	} {
		result := ServiceFromAzure(err, OpExperimentRequest)

		localErr, ok := errors.AsType[*azdext.LocalError](result)
		require.True(t, ok)
		assert.Equal(t, azdext.LocalErrorCategoryUser, localErr.Category)
		assert.Equal(t, CodeCancelled, localErr.Code)
	}
}
