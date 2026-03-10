// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/stretchr/testify/require"
)

func TestExtensionReportedError(t *testing.T) {
	t.Run("NoErrorReported", func(t *testing.T) {
		ext := &extensions.Extension{Id: "test.ext"}
		require.Nil(t, ext.GetReportedError())
	})

	t.Run("LocalErrorReported", func(t *testing.T) {
		ext := &extensions.Extension{Id: "test.ext"}
		localErr := &azdext.LocalError{
			Message:    "invalid config",
			Code:       "invalid_config",
			Category:   azdext.LocalErrorCategoryValidation,
			Suggestion: "Fix the config file",
		}
		ext.SetReportedError(localErr)

		reported := ext.GetReportedError()
		require.NotNil(t, reported)

		var gotLocal *azdext.LocalError
		require.True(t, errors.As(reported, &gotLocal))
		require.Equal(t, "invalid_config", gotLocal.Code)
		require.Equal(t, azdext.LocalErrorCategoryValidation, gotLocal.Category)
		require.Equal(t, "Fix the config file", gotLocal.Suggestion)
	})

	t.Run("ServiceErrorReported", func(t *testing.T) {
		ext := &extensions.Extension{Id: "test.ext"}
		svcErr := &azdext.ServiceError{
			Message:     "not found",
			ErrorCode:   "NotFound",
			StatusCode:  404,
			ServiceName: "test.service.com",
		}
		ext.SetReportedError(svcErr)

		reported := ext.GetReportedError()
		require.NotNil(t, reported)

		var gotSvc *azdext.ServiceError
		require.True(t, errors.As(reported, &gotSvc))
		require.Equal(t, "NotFound", gotSvc.ErrorCode)
		require.Equal(t, 404, gotSvc.StatusCode)
	})
}
